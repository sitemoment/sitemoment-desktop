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

	"github.com/sitemoment/sitemoment-desktop/internal/bing"
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
	// Regression: Stop canceling a source mid-request used to record that
	// cancellation as the source's own error (context canceled), which
	// Progress would then show as 失败 for up to RunSyncLoop's whole
	// interval.
	if msg := s.sourceError(siteID, "gsc"); msg != "" {
		t.Fatalf("gsc source error = %q after Stop, want \"\" (a cancellation is not a real failure)", msg)
	}
}

// TestStopDoesNotOverwriteAGenuineEarlierSourceError checks that when Stop
// cancels a later source mid-request, an earlier source's genuine (not
// cancellation-related) failure in the same run is still recorded —
// dropping *only* the spurious "context canceled" error, not every
// source's.
func TestStopDoesNotOverwriteAGenuineEarlierSourceError(t *testing.T) {
	siteID, st := bindSite(t, "mixed-stop.example.com")
	failing := &fakeSource{name: "gsc", bound: true, err: errors.New("boom")}
	hold := &holdingSource{name: "bing", entered: make(chan struct{}, 1), release: make(chan struct{})}
	finished := make(chan SyncStatus, 4)
	s := &SyncService{Sources: []Source{failing, hold}, Store: st, Emit: func(name string, data any) {
		if st, ok := data.(SyncStatus); ok && st.State != "running" {
			finished <- st
		}
	}}

	s.SyncNow(siteID)
	select {
	case <-hold.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("bing did not start")
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

	if msg := s.sourceError(siteID, "gsc"); msg == "" {
		t.Fatal("gsc's genuine earlier failure was dropped")
	}
	if msg := s.sourceError(siteID, "bing"); msg != "" {
		t.Fatalf("bing source error = %q, want \"\" (canceled mid-request, not a real failure)", msg)
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
	// Regression: at this point the run itself already finished (status
	// "failed"), so Stop only had a pending retry to cancel — no active
	// run for finish to mark skipBackfill through. Stop must still set it
	// directly, or RunBackfillLoop could resync the site again within
	// backfillLoopInterval despite the user stopping it.
	s.mu.Lock()
	skip := s.skipBackfill[siteID]
	s.mu.Unlock()
	if !skip {
		t.Fatal("Stop on a site with only a pending retry did not set skipBackfill")
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
	// Unpausing itself resyncs every bound site right away (its own
	// behavior, tested separately in TestPausedBlocksSyncAndPersists);
	// drain those two before looking at what the backfill loop does.
	if err := s.SetPaused(false); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-finished:
		case <-time.After(2 * time.Second):
			t.Fatal("the unpause resync did not finish")
		}
	}
	if src.count(siteA.ID) != 2 || src.count(siteB.ID) != 2 {
		t.Fatalf("after unpausing, calls = %d, %d, want 2, 2", src.count(siteA.ID), src.count(siteB.ID))
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
	if n := src.count(siteB.ID); n != 2 {
		t.Fatalf("siteB (no backfill left) was resynced %d times after unpausing, want 2 (unpause only)", n)
	}
	if n := src.count(siteA.ID); n < 3 {
		t.Fatalf("siteA was resynced %d times, want >= 3 (unpause + at least one backfill tick)", n)
	}
}

// holdingSource is a Source whose Sync blocks until release is closed (or
// ctx ends), signaling entered once it starts.
type holdingSource struct {
	name    string
	entered chan struct{}
	release chan struct{}
}

func (h *holdingSource) Name() string          { return h.name }
func (h *holdingSource) Bound(store.Site) bool { return true }
func (h *holdingSource) Sync(ctx context.Context, _ store.Site) (gscsync.Result, error) {
	select {
	case h.entered <- struct{}{}:
	default:
	}
	select {
	case <-h.release:
		return gscsync.Result{}, nil
	case <-ctx.Done():
		return gscsync.Result{}, ctx.Err()
	}
}

// TestStopWhileWaitingForASlot is the regression test for a run that never
// even got to start real work: Stop must still settle it to "stopped"
// (not leave it stuck "running" forever) and clean up its cancel/again
// bookkeeping, and the site must be syncable again afterwards.
func TestStopWhileWaitingForASlot(t *testing.T) {
	st := openStore(t)
	siteA, err := st.AddSite(ctx, "slot-a.example.com", time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	siteB, err := st.AddSite(ctx, "slot-b.example.com", time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}

	hold := &holdingSource{name: "gsc", entered: make(chan struct{}, 1), release: make(chan struct{})}
	finished := make(chan SyncStatus, 10)
	s := &SyncService{Sources: []Source{hold}, Store: st, Concurrency: 1, Emit: func(name string, data any) {
		if st, ok := data.(SyncStatus); ok && st.State != "running" {
			finished <- st
		}
	}}

	s.SyncNow(siteA.ID)
	select {
	case <-hold.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("siteA did not start")
	}
	s.SyncNow(siteB.ID) // queued: Concurrency is 1 and siteA holds the only slot
	time.Sleep(20 * time.Millisecond)
	s.Stop(siteB.ID) // must settle siteB even though it never got a slot

	select {
	case got := <-finished:
		if got.SiteID != siteB.ID || got.State != "stopped" {
			t.Fatalf("got %+v, want siteB stopped", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("siteB never settled after Stop while waiting for a slot")
	}

	close(hold.release) // let siteA finish
	select {
	case got := <-finished:
		if got.SiteID != siteA.ID {
			t.Fatalf("got %+v, want siteA", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("siteA never finished")
	}

	for _, st := range s.Statuses() {
		if st.SiteID == siteB.ID && st.State == "running" {
			t.Fatal("siteB is stuck running")
		}
	}
	s.mu.Lock()
	_, hasCancel := s.cancel[siteB.ID]
	_, hasAgain := s.again[siteB.ID]
	s.mu.Unlock()
	if hasCancel || hasAgain {
		t.Fatalf("siteB leaked bookkeeping: cancel entry present=%v, again present=%v", hasCancel, hasAgain)
	}

	// The site must not be stuck: a later SyncNow works normally.
	s.SyncNow(siteB.ID)
	select {
	case got := <-finished:
		if got.SiteID != siteB.ID {
			t.Fatalf("got %+v, want siteB", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("siteB could not be synced again after being stopped while queued")
	}
}

// TestSetPausedCancelsARunningSync checks that SetPaused(true) cancels a
// sync already in flight, settling it to "stopped" rather than leaving it
// running until it happens to finish on its own.
func TestSetPausedCancelsARunningSync(t *testing.T) {
	siteID, st := bindSite(t, "pause-cancel.example.com")
	hold := &holdingSource{name: "gsc", entered: make(chan struct{}, 1), release: make(chan struct{})}
	finished := make(chan SyncStatus, 4)
	s := &SyncService{Sources: []Source{hold}, Store: st, Emit: func(name string, data any) {
		if st, ok := data.(SyncStatus); ok && st.State != "running" {
			finished <- st
		}
	}}
	s.SyncNow(siteID)
	select {
	case <-hold.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("run did not start")
	}

	if err := s.SetPaused(true); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-finished:
		if got.State != "stopped" {
			t.Fatalf("state = %q, want %q", got.State, "stopped")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SetPaused(true) did not cancel the running sync")
	}
}

// TestRunRechecksPausedAfterAcquiringSlot checks that a run re-checks
// Paused() right after acquiring its concurrency slot, settling to
// "stopped" instead of doing real work — covering the TOCTOU gap between
// SyncNow registering a run and it actually getting a slot, which a plain
// "was my context canceled" check cannot catch on its own.
func TestRunRechecksPausedAfterAcquiringSlot(t *testing.T) {
	st := openStore(t)
	siteA, err := st.AddSite(ctx, "recheck-a.example.com", time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	siteB, err := st.AddSite(ctx, "recheck-b.example.com", time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	hold := &holdingSource{name: "gsc", entered: make(chan struct{}, 1), release: make(chan struct{})}
	finished := make(chan SyncStatus, 10)
	s := &SyncService{Sources: []Source{hold}, Store: st, Concurrency: 1, Emit: func(name string, data any) {
		if st, ok := data.(SyncStatus); ok && st.State != "running" {
			finished <- st
		}
	}}

	s.SyncNow(siteA.ID)
	select {
	case <-hold.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("siteA did not start")
	}
	s.SyncNow(siteB.ID) // queued, waiting for siteA's slot
	time.Sleep(20 * time.Millisecond)

	// Flip Paused() to true directly, without canceling any context — the
	// TOCTOU gap SetPaused's own cancel-everything cannot close on its
	// own, since siteB's cancel func already existed when this happens in
	// a real race.
	s.mu.Lock()
	s.paused, s.pausedLoaded = true, true
	s.mu.Unlock()

	close(hold.release) // siteA finishes normally and frees the slot
	var gotA, gotB SyncStatus
	for i := 0; i < 2; i++ {
		select {
		case got := <-finished:
			if got.SiteID == siteA.ID {
				gotA = got
			} else {
				gotB = got
			}
		case <-time.After(2 * time.Second):
			t.Fatal("did not see both sites settle")
		}
	}
	if gotA.State != "done" {
		t.Fatalf("siteA = %+v, want done", gotA)
	}
	if gotB.State != "stopped" {
		t.Fatalf("siteB = %+v, want stopped (paused was true when it got the slot)", gotB)
	}
}

// TestFinishDoesNotWipeANewerRunsBookkeeping is a focused, deterministic
// check of finish's ownership guard: an old run's finish, arriving after a
// newer run has already taken over the same site's slot, must not delete
// the newer run's cancel entry or consume its "again" flag.
func TestFinishDoesNotWipeANewerRunsBookkeeping(t *testing.T) {
	st := openStore(t)
	site, err := st.AddSite(ctx, "finish-token.example.com", time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	s := &SyncService{Store: st}

	newerCancelCalled := false
	s.mu.Lock()
	s.tokenSeq = 2
	s.cancel = map[int64]context.CancelFunc{site.ID: func() { newerCancelCalled = true }}
	s.cancelTok = map[int64]int64{site.ID: 2}
	s.again = map[int64]bool{site.ID: true} // meant for the newer run (token 2)
	s.mu.Unlock()

	// The old run (token 1) finishes.
	s.finish(site.ID, 1, SyncStatus{SiteID: site.ID, State: "done", At: time.Now().Unix()}, nil, nil, false)

	s.mu.Lock()
	_, stillThere := s.cancel[site.ID]
	tok := s.cancelTok[site.ID]
	again := s.again[site.ID]
	s.mu.Unlock()
	if !stillThere || tok != 2 {
		t.Fatal("the old run (token 1) deleted or altered the newer run's (token 2) cancel entry")
	}
	if !again {
		t.Fatal("the old run consumed the newer run's \"again\" flag")
	}
	if newerCancelCalled {
		t.Fatal("the old run called the newer run's cancel func")
	}
}

// TestBackfillLeftPreservedOnFailure checks that a run which fails before
// it can trust its Remaining count (a non-rate-limit error) does not reset
// backfillLeft to false, so RunBackfillLoop still retries the site later.
func TestBackfillLeftPreservedOnFailure(t *testing.T) {
	st := openStore(t)
	site, err := st.AddSite(ctx, "preserve-backfill.example.com", time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	src := &fakeSource{name: "gsc", bound: true, res: gscsync.Result{Requests: 1, Remaining: 5}}
	finished := make(chan SyncStatus, 10)
	s := &SyncService{Sources: []Source{src}, Store: st, Emit: func(name string, data any) {
		if st, ok := data.(SyncStatus); ok && st.State != "running" {
			finished <- st
		}
	}}

	s.SyncNow(site.ID)
	<-finished
	s.mu.Lock()
	left := s.backfillLeft[site.ID]
	s.mu.Unlock()
	if !left {
		t.Fatal("backfillLeft was not set after a clean run with Remaining > 0")
	}

	// A failing run (not a rate limit) must not silently reset it to false,
	// even though this run's own (untrustworthy) Remaining would say 0.
	src.err = errors.New("boom")
	src.res = gscsync.Result{}
	s.SyncNow(site.ID)
	<-finished
	s.mu.Lock()
	left = s.backfillLeft[site.ID]
	s.mu.Unlock()
	if !left {
		t.Fatal("a failing run reset backfillLeft to false")
	}
}

// TestSourceErrorsAreIsolatedPerSource checks that one source's failure
// (Bing) is recorded only for that source, not for others (GSC) that
// succeeded in the same run.
func TestSourceErrorsAreIsolatedPerSource(t *testing.T) {
	siteID, st := bindSite(t, "iso.example.com")
	gscSrc := &fakeSource{name: "gsc", bound: true, res: gscsync.Result{Requests: 1}}
	bingSrc := &fakeSource{name: "bing", bound: true, err: bing.ErrThrottled}
	finished := make(chan SyncStatus, 4)
	s := &SyncService{Sources: []Source{gscSrc, bingSrc}, Store: st, Emit: func(name string, data any) {
		if st, ok := data.(SyncStatus); ok && st.State != "running" {
			finished <- st
		}
	}}

	s.SyncNow(siteID)
	<-finished
	if msg := s.sourceError(siteID, "gsc"); msg != "" {
		t.Fatalf("gsc error = %q, want \"\" (only bing failed)", msg)
	}
	if msg := s.sourceError(siteID, "bing"); msg == "" {
		t.Fatal("bing error = \"\", want bing's failure recorded")
	}
}
