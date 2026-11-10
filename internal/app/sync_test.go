// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/google"
	"github.com/sitemoment/sitemoment-desktop/internal/gscsync"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

// tokenFunc adapts a function to gscsync.TokenSource.
type tokenFunc func(ctx context.Context, account string) (string, error)

func (f tokenFunc) AccessToken(ctx context.Context, account string) (string, error) { return f(ctx, account) }

// fakeQuerier is a gscsync.Querier that always fails, so a run that gets
// past AccessToken stops quickly instead of looping over every slice.
type fakeQuerier struct{}

func (fakeQuerier) Query(ctx context.Context, token, property string, q google.Query) ([]google.Row, error) {
	return nil, errors.New("fakeQuerier: no data")
}

// queryFunc adapts a function to gscsync.Querier.
type queryFunc func(ctx context.Context, token, property string, q google.Query) ([]google.Row, error)

func (f queryFunc) Query(ctx context.Context, token, property string, q google.Query) ([]google.Row, error) {
	return f(ctx, token, property, q)
}

func bindSite(t *testing.T, host string) (int64, *store.Store) {
	t.Helper()
	st := openStore(t)
	site, err := st.AddSite(ctx, host, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.BindGSC(ctx, site.ID, "me@example.com", "sc-domain:"+host); err != nil {
		t.Fatal(err)
	}
	return site.ID, st
}

// TestSyncNowRunsOncePerSite guards against SyncNow's check-then-set race:
// many callers racing SyncNow for the same site must start exactly one run,
// never more. The fake token source blocks so the first run cannot finish
// before every concurrent SyncNow call has had a chance to also start one.
func TestSyncNowRunsOncePerSite(t *testing.T) {
	siteID, st := bindSite(t, "example.com")

	release := make(chan struct{})
	started := make(chan struct{}, 1)
	var calls atomic.Int64
	tokens := tokenFunc(func(ctx context.Context, account string) (string, error) {
		calls.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		return "", errors.New("stop after token")
	})
	done := make(chan SyncStatus, 1)
	emit := func(name string, data any) {
		if name != "sync:status" {
			return
		}
		if st, ok := data.(SyncStatus); ok && st.State != "running" {
			select {
			case done <- st:
			default:
			}
		}
	}
	syncer := &gscsync.Syncer{Store: st, Tokens: tokens, GSC: fakeQuerier{}}
	s := &SyncService{Syncer: syncer, Store: st, Emit: emit}

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			s.SyncNow(siteID)
		}()
	}
	close(start) // release all 20 at once to maximize contention on SyncNow
	wg.Wait()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("no run started")
	}
	// Give any erroneous extra goroutine a chance to also reach AccessToken
	// before we check the count and unblock the one we expect.
	time.Sleep(20 * time.Millisecond)
	if n := calls.Load(); n != 1 {
		t.Fatalf("AccessToken called %d times, want 1", n)
	}
	close(release)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("sync did not finish")
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("AccessToken called %d times after finish, want 1", n)
	}
}

// TestSyncUsesServiceContext checks that SyncService.Ctx (set by main.go so
// in-flight syncs stop when the app quits) is the context threaded into the
// run, not context.Background().
func TestSyncUsesServiceContext(t *testing.T) {
	siteID, st := bindSite(t, "example.org")

	type key struct{}
	seen := make(chan context.Context, 1)
	tokens := tokenFunc(func(ctx context.Context, account string) (string, error) {
		seen <- ctx
		return "", errors.New("stop")
	})
	syncCtx := context.WithValue(context.Background(), key{}, "marker")
	s := &SyncService{Syncer: &gscsync.Syncer{Store: st, Tokens: tokens, GSC: fakeQuerier{}}, Store: st, Ctx: syncCtx}

	s.SyncNow(siteID)
	select {
	case c := <-seen:
		if c.Value(key{}) != "marker" {
			t.Fatal("run did not use SyncService.Ctx")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("sync did not start")
	}
}

// TestRunSyncLoopLogsSitesError checks that a Store.Sites failure is logged
// instead of being silently swallowed.
func TestRunSyncLoopLogsSitesError(t *testing.T) {
	st := openStore(t)
	st.Close() // Store.Sites will now fail with "database is closed"

	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	s := &SyncService{Store: st}
	runCtx, cancel := context.WithCancel(context.Background())
	cancel() // let the loop run exactly one iteration, then stop

	RunSyncLoop(runCtx, s, time.Hour)

	if buf.Len() == 0 {
		t.Fatal("RunSyncLoop did not log the Sites error")
	}
}

// TestSyncConcurrencyLimit checks that no more than Concurrency sites sync
// at once: a third SyncNow must block (never reach AccessToken) until one
// of the first two releases its semaphore slot.
func TestSyncConcurrencyLimit(t *testing.T) {
	st := openStore(t)
	var ids []int64
	for i := 0; i < 3; i++ {
		host := "site" + strconv.Itoa(i) + ".example.com"
		site, err := st.AddSite(ctx, host, time.Now().Unix())
		if err != nil {
			t.Fatal(err)
		}
		if err := st.BindGSC(ctx, site.ID, "me@example.com", "sc-domain:"+host); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, site.ID)
	}

	entered := make(chan struct{}, 3)
	release := make(chan struct{})
	tokens := tokenFunc(func(ctx context.Context, account string) (string, error) {
		entered <- struct{}{}
		<-release
		return "", errors.New("stop")
	})
	s := &SyncService{Syncer: &gscsync.Syncer{Store: st, Tokens: tokens, GSC: fakeQuerier{}}, Store: st, Concurrency: 2}
	for _, id := range ids {
		s.SyncNow(id)
	}

	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d of 2 expected runs started", i)
		}
	}
	select {
	case <-entered:
		t.Fatal("a third sync started before a slot freed")
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("third sync never started after a slot freed")
	}
}

// TestSyncSchedulesOneRetryAfterRateLimit checks that a run failing with
// google.ErrRateLimited is automatically retried once, after the configured
// delay, and that the retry is not stacked more than once per site.
func TestSyncSchedulesOneRetryAfterRateLimit(t *testing.T) {
	siteID, st := bindSite(t, "ratelimited.example.com")

	tokens := tokenFunc(func(ctx context.Context, account string) (string, error) { return "tok", nil })
	var calls atomic.Int64
	gsc := queryFunc(func(ctx context.Context, token, property string, q google.Query) ([]google.Row, error) {
		calls.Add(1)
		return nil, google.ErrRateLimited
	})
	statuses := make(chan SyncStatus, 20)
	emit := func(name string, data any) {
		if name != "sync:status" {
			return
		}
		if st, ok := data.(SyncStatus); ok {
			statuses <- st
		}
	}
	s := &SyncService{Syncer: &gscsync.Syncer{Store: st, Tokens: tokens, GSC: gsc}, Store: st, Emit: emit,
		RetryAfterRateLimit: 10 * time.Millisecond}

	waitFor := func(state string) {
		for {
			select {
			case got := <-statuses:
				if got.State == state {
					return
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("never saw state %q", state)
			}
		}
	}

	s.SyncNow(siteID)
	waitFor("failed")
	// The automatic retry should fire on its own, without another SyncNow call.
	waitFor("running")
	waitFor("failed")

	if n := calls.Load(); n < 2 {
		t.Fatalf("GSC.Query called %d times, want at least 2 (original run + retry)", n)
	}
}
