// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"strconv"
	"strings"
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

func (f tokenFunc) AccessToken(ctx context.Context, account string) (string, error) {
	return f(ctx, account)
}

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

// gscOnly is the source list of the M1 tests: Search Console only.
func gscOnly(g *gscsync.Syncer) []Source { return []Source{GSCSource{Syncer: g}} }

// fakeSource is a Source with a fixed binding and result.
type fakeSource struct {
	name  string
	bound bool
	res   gscsync.Result
	err   error
	calls atomic.Int64
}

func (f *fakeSource) Name() string          { return f.name }
func (f *fakeSource) Bound(store.Site) bool { return f.bound }
func (f *fakeSource) Sync(context.Context, store.Site) (gscsync.Result, error) {
	f.calls.Add(1)
	return f.res, f.err
}

// TestSyncNowRunsOncePerSite guards against SyncNow's check-then-set race:
// many callers racing SyncNow for the same site while a run is in flight
// start exactly one run, plus exactly one follow-up run after it ("again"),
// never more.
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
	finished := make(chan SyncStatus, 10)
	emit := func(name string, data any) {
		if st, ok := data.(SyncStatus); ok && name == "sync:status" && st.State != "running" {
			finished <- st
		}
	}
	s := &SyncService{Sources: gscOnly(&gscsync.Syncer{Store: st, Tokens: tokens, GSC: fakeQuerier{}}), Store: st, Emit: emit}

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
	close(start)
	wg.Wait()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("no run started")
	}
	time.Sleep(20 * time.Millisecond)
	if n := calls.Load(); n != 1 {
		t.Fatalf("AccessToken called %d times while the first run was blocked, want 1", n)
	}
	close(release)
	for i := 1; i <= 2; i++ {
		select {
		case <-finished:
		case <-time.After(2 * time.Second):
			t.Fatalf("run %d did not finish", i)
		}
	}
	select {
	case st := <-finished:
		t.Fatalf("a third run happened: %+v", st)
	case <-time.After(100 * time.Millisecond):
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("AccessToken called %d times, want 2 (the run and one follow-up)", n)
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
	s := &SyncService{Sources: gscOnly(&gscsync.Syncer{Store: st, Tokens: tokens, GSC: fakeQuerier{}}), Store: st, Ctx: syncCtx}

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
	s := &SyncService{Sources: gscOnly(&gscsync.Syncer{Store: st, Tokens: tokens, GSC: fakeQuerier{}}), Store: st, Concurrency: 2}
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
	s := &SyncService{Sources: gscOnly(&gscsync.Syncer{Store: st, Tokens: tokens, GSC: gsc}), Store: st, Emit: emit,
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

// TestSyncRunsEveryBoundSource checks that a run syncs each bound source,
// skips unbound ones, keeps going after one fails, and names the source
// that failed.
func TestSyncRunsEveryBoundSource(t *testing.T) {
	siteID, st := bindSite(t, "sources.example.com")
	gsc := &fakeSource{name: "gsc", bound: true, res: gscsync.Result{Requests: 4}}
	ga := &fakeSource{name: "ga", bound: true, res: gscsync.Result{Requests: 1}, err: google.ErrGAScope}
	bg := &fakeSource{name: "bing", bound: false}
	finished := make(chan SyncStatus, 4)
	s := &SyncService{Sources: []Source{gsc, ga, bg}, Store: st, Emit: func(name string, data any) {
		if st, ok := data.(SyncStatus); ok && st.State != "running" {
			finished <- st
		}
	}}
	s.SyncNow(siteID)
	select {
	case got := <-finished:
		if got.State != "failed" || got.Requests != 5 || !strings.HasPrefix(got.Error, "GA4：") || strings.Contains(got.Error, "Search Console") {
			t.Fatalf("status = %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("sync did not finish")
	}
	if gsc.calls.Load() != 1 || ga.calls.Load() != 1 || bg.calls.Load() != 0 {
		t.Fatalf("calls gsc=%d ga=%d bing=%d", gsc.calls.Load(), ga.calls.Load(), bg.calls.Load())
	}
}

func TestSyncCallsAfterWhenDone(t *testing.T) {
	siteID, st := bindSite(t, "example.com")
	after := make(chan int64, 1)
	src := &fakeSource{name: "gsc", bound: true}
	s := &SyncService{Sources: []Source{src}, Store: st, After: func(id int64) { after <- id }}
	s.SyncNow(siteID)
	select {
	case id := <-after:
		if id != siteID {
			t.Fatalf("After(%d), want %d", id, siteID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("After was not called")
	}
}

// TestStopCancelsRunningSync checks that Stop ends a run in progress (its
// context is canceled) and settles its status to "stopped", not "failed".
func TestStopCancelsRunningSync(t *testing.T) {
	siteID, st := bindSite(t, "stop.example.com")
	started := make(chan struct{})
	tokens := tokenFunc(func(ctx context.Context, account string) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	})
	finished := make(chan SyncStatus, 4)
	emit := func(name string, data any) {
		if st, ok := data.(SyncStatus); ok && name == "sync:status" && st.State != "running" {
			finished <- st
		}
	}
	s := &SyncService{Sources: gscOnly(&gscsync.Syncer{Store: st, Tokens: tokens, GSC: fakeQuerier{}}), Store: st, Emit: emit}
	s.SyncNow(siteID)
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("run did not start")
	}

	s.Stop(siteID)
	select {
	case got := <-finished:
		if got.State != "stopped" {
			t.Fatalf("state = %q, want %q", got.State, "stopped")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run did not finish after Stop")
	}
}

// TestStopDropsPendingRetry checks that Stop also cancels a retry
// scheduled (but not yet fired) after a rate-limited run.
func TestStopDropsPendingRetry(t *testing.T) {
	siteID, st := bindSite(t, "stopretry.example.com")
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
	s := &SyncService{Sources: gscOnly(&gscsync.Syncer{Store: st, Tokens: tokens, GSC: gsc}), Store: st, Emit: emit,
		RetryAfterRateLimit: 200 * time.Millisecond}

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
	callsAfterFirstRun := calls.Load() // the run itself may issue a few concurrent requests
	s.Stop(siteID)                     // drop the pending retry before its delay elapses

	select {
	case got := <-statuses:
		t.Fatalf("a retry ran after Stop: %+v", got)
	case <-time.After(400 * time.Millisecond):
	}
	if n := calls.Load(); n != callsAfterFirstRun {
		t.Fatalf("GSC.Query called %d more times after Stop, want 0 (no retry)", n-callsAfterFirstRun)
	}
}

// TestPausedBlocksSyncAndPersists checks that SetPaused(true) makes both
// SyncNow and RunSyncLoop no-ops, that unpausing lets them run again, and
// that the setting is visible to another SyncService sharing the store
// (it survives a restart).
func TestPausedBlocksSyncAndPersists(t *testing.T) {
	siteID, st := bindSite(t, "paused.example.com")
	var calls atomic.Int64
	tokens := tokenFunc(func(ctx context.Context, account string) (string, error) {
		calls.Add(1)
		return "", errors.New("stop")
	})
	s := &SyncService{Sources: gscOnly(&gscsync.Syncer{Store: st, Tokens: tokens, GSC: fakeQuerier{}}), Store: st}

	if s.Paused() {
		t.Fatal("Paused() = true before SetPaused was ever called")
	}
	if err := s.SetPaused(true); err != nil {
		t.Fatal(err)
	}
	if !s.Paused() {
		t.Fatal("Paused() = false after SetPaused(true)")
	}

	s.SyncNow(siteID)
	time.Sleep(50 * time.Millisecond)
	if n := calls.Load(); n != 0 {
		t.Fatalf("SyncNow ran while paused: %d calls", n)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	cancel() // let RunSyncLoop run exactly one (paused, so skipped) iteration
	RunSyncLoop(runCtx, s, time.Hour)
	if n := calls.Load(); n != 0 {
		t.Fatalf("RunSyncLoop ran while paused: %d calls", n)
	}

	other := &SyncService{Store: st}
	if !other.Paused() {
		t.Fatal("a second SyncService sharing the store sees Paused() = false, want true (not persisted)")
	}

	if err := s.SetPaused(false); err != nil {
		t.Fatal(err)
	}
	s.SyncNow(siteID)
	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := calls.Load(); n == 0 {
		t.Fatal("SyncNow never ran after unpausing")
	}
}

// perSiteSource is a Source whose result depends on which site is being
// synced, for tests that need two sites to behave differently.
type perSiteSource struct {
	name string
	res  map[int64]gscsync.Result

	mu    sync.Mutex
	calls map[int64]int
}

func (p *perSiteSource) Name() string          { return p.name }
func (p *perSiteSource) Bound(store.Site) bool { return true }
func (p *perSiteSource) Sync(_ context.Context, site store.Site) (gscsync.Result, error) {
	p.mu.Lock()
	if p.calls == nil {
		p.calls = map[int64]int{}
	}
	p.calls[site.ID]++
	p.mu.Unlock()
	return p.res[site.ID], nil
}

func (p *perSiteSource) count(id int64) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls[id]
}

// TestRunBackfillLoopResyncsOnlySitesWithBackfillLeft checks that
// RunBackfillLoop re-syncs a site whose last run left Search Console
// history incomplete (Result.Remaining > 0), leaves a fully caught-up site
// alone, and does nothing at all while paused.
func TestRunBackfillLoopResyncsOnlySitesWithBackfillLeft(t *testing.T) {
	old := backfillLoopInterval
	backfillLoopInterval = 10 * time.Millisecond
	t.Cleanup(func() { backfillLoopInterval = old })

	st := openStore(t)
	siteA, err := st.AddSite(ctx, "backfill-a.example.com", time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	siteB, err := st.AddSite(ctx, "backfill-b.example.com", time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}

	src := &perSiteSource{name: "gsc", res: map[int64]gscsync.Result{
		siteA.ID: {Requests: 1, Remaining: 5}, // backfill not done
		siteB.ID: {Requests: 1},               // fully caught up
	}}
	finished := make(chan SyncStatus, 10)
	s := &SyncService{Sources: []Source{src}, Store: st, Emit: func(name string, data any) {
		if st, ok := data.(SyncStatus); ok && st.State != "running" {
			finished <- st
		}
	}}

	s.SyncNow(siteA.ID)
	s.SyncNow(siteB.ID)
	for i := 0; i < 2; i++ {
		select {
		case <-finished:
		case <-time.After(2 * time.Second):
			t.Fatal("initial runs did not finish")
		}
	}
	if src.count(siteA.ID) != 1 || src.count(siteB.ID) != 1 {
		t.Fatalf("initial calls = %d, %d, want 1, 1", src.count(siteA.ID), src.count(siteB.ID))
	}

	// Paused: the loop must not touch siteA even though it has backfill left.
	if err := s.SetPaused(true); err != nil {
		t.Fatal(err)
	}
	pausedCtx, cancelPaused := context.WithCancel(context.Background())
	go RunBackfillLoop(pausedCtx, s)
	time.Sleep(60 * time.Millisecond)
	cancelPaused()
	if n := src.count(siteA.ID); n != 1 {
		t.Fatalf("siteA was resynced %d times while paused, want 1", n)
	}
	if err := s.SetPaused(false); err != nil {
		t.Fatal(err)
	}

	bctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go RunBackfillLoop(bctx, s)

	select {
	case got := <-finished:
		if got.SiteID != siteA.ID {
			t.Fatalf("backfill loop resynced site %d, want siteA (%d)", got.SiteID, siteA.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("backfill loop never resynced the site with backfill left")
	}
	cancel()
	if n := src.count(siteB.ID); n != 1 {
		t.Fatalf("siteB (no backfill left) was resynced %d times, want 1", n)
	}
}
