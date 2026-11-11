// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/bing"
	"github.com/sitemoment/sitemoment-desktop/internal/bingsync"
	"github.com/sitemoment/sitemoment-desktop/internal/gasync"
	"github.com/sitemoment/sitemoment-desktop/internal/google"
	"github.com/sitemoment/sitemoment-desktop/internal/gscsync"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

// Source is one data source a site can be bound to.
type Source interface {
	// Name is "gsc", "ga" or "bing".
	Name() string
	Bound(site store.Site) bool
	Sync(ctx context.Context, site store.Site) (gscsync.Result, error)
}

// GSCSource syncs Search Console.
type GSCSource struct{ Syncer *gscsync.Syncer }

func (GSCSource) Name() string            { return "gsc" }
func (GSCSource) Bound(s store.Site) bool { return s.GSCProperty != "" }
func (g GSCSource) Sync(ctx context.Context, site store.Site) (gscsync.Result, error) {
	return g.Syncer.SyncSite(ctx, site)
}

// GASource syncs GA4.
type GASource struct{ Syncer *gasync.Syncer }

func (GASource) Name() string            { return "ga" }
func (GASource) Bound(s store.Site) bool { return s.GAProperty != "" }
func (g GASource) Sync(ctx context.Context, site store.Site) (gscsync.Result, error) {
	return g.Syncer.SyncSite(ctx, site)
}

// BingSource syncs Bing Webmaster.
type BingSource struct{ Syncer *bingsync.Syncer }

func (BingSource) Name() string            { return "bing" }
func (BingSource) Bound(s store.Site) bool { return s.BingSite != "" }
func (b BingSource) Sync(ctx context.Context, site store.Site) (gscsync.Result, error) {
	return b.Syncer.SyncSite(ctx, site)
}

var sourceLabels = map[string]string{"gsc": "Search Console", "ga": "GA4", "bing": "Bing"}

// SyncStatus is the latest sync state of a site, sent as event "sync:status".
type SyncStatus struct {
	SiteID   int64  `json:"siteId"`
	State    string `json:"state"` // running | done | failed | stopped
	Error    string `json:"error,omitempty"`
	Requests int    `json:"requests"`
	At       int64  `json:"at"`
}

// defaultConcurrency caps how many sites sync at once: backfills are slow
// and bursty, and running every site at once makes rate limiting (and the
// resulting retries) much more likely.
const defaultConcurrency = 2

// defaultRetryAfterRateLimit is how long SyncService waits before
// automatically retrying a site whose run was rate limited.
const defaultRetryAfterRateLimit = 5 * time.Minute

// backfillLoopInterval is how often RunBackfillLoop re-triggers sites whose
// Search Console or GA4 history is not fully caught up yet (see gscsync's
// and gasync's RecentDays/BackfillBatch): often enough to finish in a
// reasonable time, rare enough to stay well under Google's rate limits.
// Not a setting (it is never read from or written to the settings table)
// — a var only so tests can shorten it; production code never changes it.
var backfillLoopInterval = 15 * time.Minute

// pausedSettingKey is the settings-table key for the global "pause every
// sync" switch (SetPaused/Paused).
const pausedSettingKey = "sync.paused"

// SyncService runs a site's data source syncs in the background.
type SyncService struct {
	// Sources are synced in order for every site they are bound to.
	Sources []Source
	Store   *store.Store
	Emit    func(name string, data any)
	// Ctx is the context in-flight syncs run under; main.go sets it to a
	// context cancelled when the app quits. Not set by a frontend call: it
	// is a plain field (Wails binds exported methods, not fields), and
	// nothing exported here hands it back out. Defaults to
	// context.Background() when nil.
	Ctx context.Context
	// Concurrency caps how many sites sync at once; a run beyond the cap
	// waits for a slot (its status stays "running" meanwhile). 0: defaultConcurrency.
	Concurrency int
	// RetryAfterRateLimit is how long to wait before automatically retrying
	// a rate-limited site. 0: defaultRetryAfterRateLimit.
	RetryAfterRateLimit time.Duration
	// After runs once a site's sync has finished (done or failed, not
	// stopped), in the sync's goroutine: main.go evaluates the site's
	// tasks there.
	After func(siteID int64)

	mu       sync.Mutex
	status   map[int64]SyncStatus
	sem      chan struct{}
	retrying map[int64]bool
	again    map[int64]bool
	// tokenSeq assigns each run a unique, increasing token: cancel/cancelTok
	// let a run's own cleanup tell whether it still "owns" siteID's slot
	// (nothing newer has started), so it never wipes a different run's
	// bookkeeping (see finish).
	tokenSeq  int64
	cancel    map[int64]context.CancelFunc
	cancelTok map[int64]int64
	// retryCancel is the cancel func for a pending scheduleRetry timer.
	retryCancel map[int64]context.CancelFunc
	// backfillLeft marks sites whose last fully-completed run reported
	// Search Console or GA4 history still not fully caught up
	// (gscsync/gasync Result.Remaining > 0), for RunBackfillLoop to pick up
	// again. A run that was stopped, or that failed outright, leaves this
	// alone (see finish) rather than resetting it to false.
	backfillLeft map[int64]bool
	// skipBackfill marks a site Stop was just called on: RunBackfillLoop
	// leaves it alone until a fresh SyncNow (manual, or RunSyncLoop's next
	// tick) clears the flag — a user's Stop should not be silently undone
	// by the backfill loop a few minutes later.
	skipBackfill map[int64]bool
	// sourceErr is the last error per (site, source); a source that is not
	// present, or whose value is "", is not currently failing. Sources not
	// attempted in a given run keep their previous entry untouched, so one
	// source's failure (e.g. Bing) never marks another (e.g. GSC) failed.
	sourceErr map[int64]map[string]string
	// nextBackfillAt is when RunBackfillLoop will next check for sites to
	// resync (unix seconds), kept current by RunBackfillLoop itself; 0
	// before it has ever ticked.
	nextBackfillAt int64
	paused         bool
	pausedLoaded   bool
}

func (s *SyncService) context() context.Context {
	if s.Ctx != nil {
		return s.Ctx
	}
	return context.Background()
}

// semaphore lazily creates the concurrency-limiting channel.
func (s *SyncService) semaphore() chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sem == nil {
		n := s.Concurrency
		if n <= 0 {
			n = defaultConcurrency
		}
		s.sem = make(chan struct{}, n)
	}
	return s.sem
}

// setStatus records and emits st directly, without touching any of a
// run's other bookkeeping (cancel, again, backfillLeft...). Used by tests
// to simulate a site's state; production code goes through finish.
func (s *SyncService) setStatus(st SyncStatus) {
	s.mu.Lock()
	if s.status == nil {
		s.status = map[int64]SyncStatus{}
	}
	s.status[st.SiteID] = st
	s.mu.Unlock()
	if s.Emit != nil {
		s.Emit("sync:status", st)
	}
}

// SyncNow starts syncing a site. When a run is already going, it asks for
// one more run right after it (a source may just have been bound). The
// check and the switch to "running" happen in one critical section, so two
// concurrent callers can never both start a run. SyncNow does nothing while
// syncing is paused (SetPaused).
func (s *SyncService) SyncNow(siteID int64) {
	if s.Paused() {
		return
	}
	s.mu.Lock()
	if s.status == nil {
		s.status = map[int64]SyncStatus{}
	}
	if s.status[siteID].State == "running" {
		if s.again == nil {
			s.again = map[int64]bool{}
		}
		s.again[siteID] = true
		s.mu.Unlock()
		return
	}
	s.tokenSeq++
	token := s.tokenSeq
	st := SyncStatus{SiteID: siteID, State: "running", At: time.Now().Unix()}
	s.status[siteID] = st
	runCtx, cancel := context.WithCancel(s.context())
	if s.cancel == nil {
		s.cancel = map[int64]context.CancelFunc{}
	}
	if s.cancelTok == nil {
		s.cancelTok = map[int64]int64{}
	}
	s.cancel[siteID] = cancel
	s.cancelTok[siteID] = token
	// A fresh run supersedes any earlier Stop: let the backfill loop
	// consider this site again once this run (or a later one) finishes.
	delete(s.skipBackfill, siteID)
	s.mu.Unlock()

	if s.Emit != nil {
		s.Emit("sync:status", st)
	}
	go s.run(runCtx, cancel, token, siteID)
}

// Stop cancels siteID's currently running sync, if any, and drops any
// pending automatic retry for it. The run's status settles to "stopped"
// once its goroutine notices; call SyncNow afterwards to start over. Until
// then, RunBackfillLoop leaves the site alone (see skipBackfill).
func (s *SyncService) Stop(siteID int64) {
	s.mu.Lock()
	if cancel, ok := s.cancel[siteID]; ok {
		cancel()
	}
	if cancel, ok := s.retryCancel[siteID]; ok {
		cancel()
	}
	delete(s.again, siteID)
	s.mu.Unlock()
}

// SetPaused turns every sync (manual and automatic) on or off, persisting
// the choice so it survives a restart. Pausing cancels every run (and
// pending retry) currently in flight, the same as calling Stop on each of
// them; unpausing immediately syncs every bound site, mirroring
// RunSyncLoop's on-launch pass, instead of waiting for the next tick.
func (s *SyncService) SetPaused(paused bool) error {
	v := ""
	if paused {
		v = "1"
	}
	if err := s.Store.SetSetting(s.context(), pausedSettingKey, v); err != nil {
		return err
	}
	s.mu.Lock()
	s.paused, s.pausedLoaded = paused, true
	var cancels []context.CancelFunc
	if paused {
		for _, c := range s.cancel {
			cancels = append(cancels, c)
		}
		for _, c := range s.retryCancel {
			cancels = append(cancels, c)
		}
	}
	s.mu.Unlock()
	for _, c := range cancels {
		c()
	}

	if s.Emit != nil {
		s.Emit("sync:paused", paused)
	}
	if !paused {
		if sites, err := s.Store.Sites(s.context()); err != nil {
			log.Printf("app: list sites to resume after unpausing: %v", err)
		} else {
			for _, site := range sites {
				if s.anyBound(site) {
					s.SyncNow(site.ID)
				}
			}
		}
	}
	return nil
}

// Paused reports whether syncing is currently paused, loading the setting
// from the store on first use. It fails closed: if that lookup errors, it
// reports paused (and logs) rather than risk syncing — and retries the
// lookup on every call until one succeeds, rather than caching the
// pessimistic answer forever.
func (s *SyncService) Paused() bool {
	s.mu.Lock()
	if s.pausedLoaded {
		defer s.mu.Unlock()
		return s.paused
	}
	s.mu.Unlock()

	v, err := s.Store.Setting(s.context(), pausedSettingKey)
	if err != nil {
		log.Printf("app: read the sync.paused setting: %v (treating as paused)", err)
		return true
	}
	paused := v == "1"
	s.mu.Lock()
	s.paused, s.pausedLoaded = paused, true
	s.mu.Unlock()
	return paused
}

// Statuses lists the latest status of every site that has synced.
func (s *SyncService) Statuses() []SyncStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SyncStatus, 0, len(s.status))
	for _, st := range s.status {
		out = append(out, st)
	}
	return out
}

// sourceError is the last known error for (siteID, source), or "" if it is
// not currently failing (never attempted, or its last attempt succeeded).
func (s *SyncService) sourceError(siteID int64, source string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sourceErr[siteID][source]
}

// stoppedStatus is the SyncStatus for a run that ends without having
// synced anything, because it was stopped (or the app is quitting) before
// or just after it got a chance to start real work.
func stoppedStatus(siteID int64) SyncStatus {
	return SyncStatus{SiteID: siteID, State: "stopped", At: time.Now().Unix()}
}

// run syncs one source after another for siteID, once each (a source whose
// Result.Remaining is still > 0 after that — typically gscsync's or
// gasync's gradual backfill — is picked up again by RunBackfillLoop or the
// next RunSyncLoop tick, not immediately re-run here: that is what keeps a
// first big backfill from blocking for minutes in one call). cancel is
// SyncNow's per-run context.CancelFunc, released here so Stop never leaks
// it. token identifies this run for finish's ownership check.
func (s *SyncService) run(ctx context.Context, cancel context.CancelFunc, token, siteID int64) {
	defer cancel()
	sem := s.semaphore()
	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		// Stopped (or paused, or shutting down) while still waiting for a
		// concurrency slot: settle the status and release the slot/again
		// bookkeeping now — nothing here must leave the site stuck
		// "running" forever.
		s.finish(siteID, token, stoppedStatus(siteID), nil, nil, false)
		return
	}
	// A long wait for a slot can straddle a Stop or a SetPaused(true) that
	// landed between SyncNow registering this run and it actually getting
	// to run: re-check before doing any real work.
	if ctx.Err() != nil || s.Paused() {
		<-sem
		s.finish(siteID, token, stoppedStatus(siteID), nil, nil, false)
		return
	}

	total, remaining, retry := 0, 0, false
	var problems []string
	sourceErrs := map[string]string{}
	site, err := s.Store.Site(ctx, siteID)
	if err != nil {
		problems = append(problems, syncMessage(err))
	}
	for _, src := range s.Sources {
		if err != nil || ctx.Err() != nil {
			break
		}
		if !src.Bound(site) {
			continue
		}
		res, serr := src.Sync(ctx, site)
		total += res.Requests
		remaining += res.Remaining
		if serr != nil {
			sourceErrs[src.Name()] = syncMessage(serr)
			problems = append(problems, sourceLabels[src.Name()]+"："+sourceErrs[src.Name()])
			retry = retry || errors.Is(serr, google.ErrRateLimited) || errors.Is(serr, bing.ErrThrottled)
		} else {
			sourceErrs[src.Name()] = ""
		}
	}
	<-sem

	st := SyncStatus{SiteID: siteID, State: "done", Requests: total, At: time.Now().Unix()}
	switch {
	case ctx.Err() != nil:
		st.State = "stopped"
	case len(problems) > 0:
		st.State, st.Error = "failed", strings.Join(problems, "；")
	}
	// Only a clean, uninterrupted run's Remaining is trustworthy: a run
	// stopped partway through, or one where a source errored before it
	// could report Remaining, must not reset backfillLeft to "done" just
	// because this run happened not to see any remaining work.
	var backfillKnown *bool
	if ctx.Err() == nil && len(problems) == 0 {
		v := remaining > 0
		backfillKnown = &v
	}
	s.finish(siteID, token, st, sourceErrs, backfillKnown, retry)
}

// finish atomically settles siteID's run (token) status, per-source
// errors and (when known) backfillLeft, and — only if this run still owns
// siteID's slot, i.e. nothing newer has superseded it — its cancel/again
// bookkeeping (and, for a stopped run, marks it to be skipped by
// RunBackfillLoop until the next fresh SyncNow). Emit, After and chaining
// another run happen outside the lock.
func (s *SyncService) finish(siteID, token int64, st SyncStatus, sourceErrs map[string]string, backfillKnown *bool, retry bool) {
	s.mu.Lock()
	owns := s.cancelTok[siteID] == token
	if owns {
		delete(s.cancel, siteID)
		delete(s.cancelTok, siteID)
	}
	if s.status == nil {
		s.status = map[int64]SyncStatus{}
	}
	s.status[siteID] = st
	if backfillKnown != nil {
		if s.backfillLeft == nil {
			s.backfillLeft = map[int64]bool{}
		}
		s.backfillLeft[siteID] = *backfillKnown
	}
	if len(sourceErrs) > 0 {
		if s.sourceErr == nil {
			s.sourceErr = map[int64]map[string]string{}
		}
		if s.sourceErr[siteID] == nil {
			s.sourceErr[siteID] = map[string]string{}
		}
		for name, msg := range sourceErrs {
			s.sourceErr[siteID][name] = msg
		}
	}
	var again bool
	if owns {
		again = s.again[siteID]
		delete(s.again, siteID)
		if st.State == "stopped" {
			if s.skipBackfill == nil {
				s.skipBackfill = map[int64]bool{}
			}
			s.skipBackfill[siteID] = true
		}
	}
	s.mu.Unlock()

	if s.Emit != nil {
		s.Emit("sync:status", st)
	}
	if s.After != nil && st.State != "stopped" {
		s.After(siteID)
	}
	switch {
	case st.State == "stopped":
		// Never auto-chain or auto-retry a run the user (or shutdown, or
		// a pause) stopped; see skipBackfill above for the backfill loop.
	case again:
		s.SyncNow(siteID)
	case retry:
		s.scheduleRetry(s.context(), siteID)
	}
}

// scheduleRetry arranges exactly one automatic retry of siteID after
// RetryAfterRateLimit, unless one is already pending for that site. The
// retry respects ctx (the service's context, not the run's: a per-run Stop
// must not itself end a retry scheduled because of this run, but shutdown
// or another Stop call still can) and can itself be dropped early by Stop.
func (s *SyncService) scheduleRetry(ctx context.Context, siteID int64) {
	s.mu.Lock()
	if s.retrying == nil {
		s.retrying = map[int64]bool{}
	}
	if s.retrying[siteID] {
		s.mu.Unlock()
		return
	}
	s.retrying[siteID] = true
	retryCtx, cancel := context.WithCancel(ctx)
	if s.retryCancel == nil {
		s.retryCancel = map[int64]context.CancelFunc{}
	}
	s.retryCancel[siteID] = cancel
	s.mu.Unlock()

	delay := s.RetryAfterRateLimit
	if delay <= 0 {
		delay = defaultRetryAfterRateLimit
	}
	go func() {
		t := time.NewTimer(delay)
		defer t.Stop()
		defer cancel()
		clear := func() {
			s.mu.Lock()
			delete(s.retrying, siteID)
			delete(s.retryCancel, siteID)
			s.mu.Unlock()
		}
		select {
		case <-retryCtx.Done():
			clear()
		case <-t.C:
			clear()
			s.SyncNow(siteID)
		}
	}()
}

func syncMessage(err error) string {
	switch {
	case errors.Is(err, google.ErrReauth):
		return "Google 授权已失效，请在设置里重新连接"
	case errors.Is(err, google.ErrGAScope):
		return "没有 Google Analytics 读取权限，请在设置里重新连接 Google 账号并勾选 Analytics"
	case errors.Is(err, google.ErrRateLimited):
		return "Google 接口限流，稍后自动重试"
	case errors.Is(err, bing.ErrInvalidKey):
		return "Bing API key 无效，请在设置里重新添加"
	case errors.Is(err, bing.ErrNotAuthorized):
		return "这个 Bing key 读不到该站点，请重新选择"
	case errors.Is(err, bing.ErrThrottled):
		return "Bing 限流，稍后自动重试"
	case errors.Is(err, gscsync.ErrNotBound):
		return "还没有选择 Search Console 资源"
	default:
		return err.Error()
	}
}

// anyBound reports whether any source is bound to site.
func (s *SyncService) anyBound(site store.Site) bool {
	for _, src := range s.Sources {
		if src.Bound(site) {
			return true
		}
	}
	return false
}

// RunSyncLoop syncs every site with a bound source now and then every
// interval until ctx ends, unless syncing is paused (SetPaused), in which
// case it does nothing until the next tick (so a paused app never
// auto-syncs on launch either). It is a function, not a method, so it is
// not exposed to the frontend.
func RunSyncLoop(ctx context.Context, s *SyncService, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if !s.Paused() {
			sites, err := s.Store.Sites(ctx)
			if err != nil {
				log.Printf("app: list sites for sync: %v", err)
			}
			for _, site := range sites {
				if s.anyBound(site) {
					s.SyncNow(site.ID)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// sitesNeedingBackfill lists the sites whose last fully-completed run
// reported history not yet fully caught up (backfillLeft), excluding any
// just Stopped (skipBackfill) until a fresh SyncNow clears that.
func (s *SyncService) sitesNeedingBackfill() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []int64
	for id, left := range s.backfillLeft {
		if left && !s.skipBackfill[id] {
			out = append(out, id)
		}
	}
	return out
}

// NextBackfillAt is when RunBackfillLoop will next check for sites to
// resync (unix seconds), or 0 if it has not ticked yet.
func (s *SyncService) NextBackfillAt() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nextBackfillAt
}

// RunBackfillLoop re-syncs, every backfillLoopInterval until ctx ends, only
// the sites whose history is not fully caught up yet: a big first backfill
// proceeds in small batches (see gscsync's and gasync's
// RecentDays/BackfillBatch) spaced well apart, instead of blocking. It does
// nothing while paused, and sites with nothing left to backfill (or just
// Stopped) are not touched.
func RunBackfillLoop(ctx context.Context, s *SyncService) {
	t := time.NewTicker(backfillLoopInterval)
	defer t.Stop()
	s.mu.Lock()
	s.nextBackfillAt = time.Now().Add(backfillLoopInterval).Unix()
	s.mu.Unlock()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.mu.Lock()
		s.nextBackfillAt = time.Now().Add(backfillLoopInterval).Unix()
		s.mu.Unlock()
		if s.Paused() {
			continue
		}
		for _, id := range s.sitesNeedingBackfill() {
			s.SyncNow(id)
		}
	}
}
