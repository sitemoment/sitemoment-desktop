// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
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
