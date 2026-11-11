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
	State    string `json:"state"` // running | done | failed
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
// Search Console history is not fully caught up yet (see gscsync's
// recentDays/backfillBatch): often enough to finish in a reasonable time,
// rare enough to stay well under Google's rate limits. Not a setting (it
// is never read from or written to the settings table) — a var only so
// tests can shorten it; production code never changes it.
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
	// After runs once a site's sync has finished (done or failed), in the
	// sync's goroutine: main.go evaluates the site's tasks there.
	After func(siteID int64)

	mu       sync.Mutex
	status   map[int64]SyncStatus
	sem      chan struct{}
	retrying map[int64]bool
	again    map[int64]bool
	// cancel holds each site's currently running sync's cancel func, so
	// Stop can end it. retryCancel is the same for a pending scheduleRetry
	// timer. backfillLeft marks sites whose last run reported Search
	// Console history still not fully caught up (gscsync.Result.Remaining
	// > 0), for RunBackfillLoop to pick up again.
	cancel       map[int64]context.CancelFunc
	retryCancel  map[int64]context.CancelFunc
	backfillLeft map[int64]bool
	paused       bool
	pausedLoaded bool
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
	st := SyncStatus{SiteID: siteID, State: "running", At: time.Now().Unix()}
	s.status[siteID] = st
	runCtx, cancel := context.WithCancel(s.context())
	if s.cancel == nil {
		s.cancel = map[int64]context.CancelFunc{}
	}
	s.cancel[siteID] = cancel
	s.mu.Unlock()

	if s.Emit != nil {
		s.Emit("sync:status", st)
	}
	go s.run(runCtx, cancel, siteID)
}

// Stop cancels siteID's currently running sync, if any, and drops any
// pending automatic retry for it. The run's status settles to "failed"
// (its context is canceled) once its goroutine notices; call SyncNow
// afterwards to start over.
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
// the choice so it survives a restart. Pausing does not stop a run already
// in flight; call Stop for that.
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
	s.mu.Unlock()
	if s.Emit != nil {
		s.Emit("sync:paused", paused)
	}
	return nil
}

// Paused reports whether syncing is currently paused, loading the setting
// from the store on first use (and on every call while that lookup keeps
// failing, so a storage error never permanently freezes the in-memory
// value at a stale answer).
func (s *SyncService) Paused() bool {
	s.mu.Lock()
	if s.pausedLoaded {
		defer s.mu.Unlock()
		return s.paused
	}
	s.mu.Unlock()

	v, err := s.Store.Setting(s.context(), pausedSettingKey)
	if err != nil {
		return false
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

// run syncs one source after another for siteID, once each (a source whose
// Result.Remaining is still > 0 after that — typically gscsync's gradual
// Search Console backfill — is picked up again by RunBackfillLoop or the
// next RunSyncLoop tick, not immediately re-run here: that is what keeps a
// first big backfill from blocking for minutes in one call). cancel is
// SyncNow's per-run context.CancelFunc, released here so Stop never leaks
// it.
func (s *SyncService) run(ctx context.Context, cancel context.CancelFunc, siteID int64) {
	defer cancel()
	sem := s.semaphore()
	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		return
	}

	total, remaining, retry := 0, 0, false
	var problems []string
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
			problems = append(problems, sourceLabels[src.Name()]+"："+syncMessage(serr))
			retry = retry || errors.Is(serr, google.ErrRateLimited) || errors.Is(serr, bing.ErrThrottled)
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
	s.setStatus(st)
	if s.After != nil && ctx.Err() == nil {
		s.After(siteID)
	}

	s.mu.Lock()
	if s.backfillLeft == nil {
		s.backfillLeft = map[int64]bool{}
	}
	s.backfillLeft[siteID] = remaining > 0
	if s.cancel != nil {
		delete(s.cancel, siteID)
	}
	again := s.again[siteID]
	delete(s.again, siteID)
	s.mu.Unlock()
	switch {
	case ctx.Err() != nil:
		// Stopped (or the app is quitting): never auto-chain or auto-retry.
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

// sitesNeedingBackfill lists the sites whose last run reported Search
// Console history not yet fully caught up (gscsync.Result.Remaining > 0).
func (s *SyncService) sitesNeedingBackfill() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []int64
	for id, left := range s.backfillLeft {
		if left {
			out = append(out, id)
		}
	}
	return out
}

// RunBackfillLoop re-syncs, every backfillLoopInterval until ctx ends, only
// the sites whose Search Console history is not fully caught up yet: a big
// first backfill proceeds in small batches (see gscsync's
// recentDays/backfillBatch) spaced well apart, instead of blocking. It does
// nothing while paused, and sites with nothing left to backfill are not
// touched (so it never makes pointless requests once a site is caught up).
func RunBackfillLoop(ctx context.Context, s *SyncService) {
	t := time.NewTicker(backfillLoopInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if s.Paused() {
			continue
		}
		for _, id := range s.sitesNeedingBackfill() {
			s.SyncNow(id)
		}
	}
}
