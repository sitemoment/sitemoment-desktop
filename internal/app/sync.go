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

	mu       sync.Mutex
	status   map[int64]SyncStatus
	sem      chan struct{}
	retrying map[int64]bool
	again    map[int64]bool
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
// concurrent callers can never both start a run.
func (s *SyncService) SyncNow(siteID int64) {
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
	s.mu.Unlock()

	if s.Emit != nil {
		s.Emit("sync:status", st)
	}
	go s.run(s.context(), siteID)
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

func (s *SyncService) run(ctx context.Context, siteID int64) {
	sem := s.semaphore()
	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		return
	}

	total, retry := 0, false
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
		for {
			res, serr := src.Sync(ctx, site)
			total += res.Requests
			if serr != nil {
				problems = append(problems, sourceLabels[src.Name()]+"："+syncMessage(serr))
				retry = retry || errors.Is(serr, google.ErrRateLimited) || errors.Is(serr, bing.ErrThrottled)
				break
			}
			if res.Remaining == 0 {
				break
			}
		}
	}
	<-sem

	st := SyncStatus{SiteID: siteID, State: "done", Requests: total, At: time.Now().Unix()}
	if len(problems) > 0 {
		st.State, st.Error = "failed", strings.Join(problems, "；")
	}
	s.setStatus(st)

	s.mu.Lock()
	again := s.again[siteID]
	delete(s.again, siteID)
	s.mu.Unlock()
	switch {
	case again && ctx.Err() == nil:
		s.SyncNow(siteID)
	case retry:
		s.scheduleRetry(ctx, siteID)
	}
}

// scheduleRetry arranges exactly one automatic retry of siteID after
// RetryAfterRateLimit, unless one is already pending for that site. The
// retry respects ctx: if the service is shutting down, it is dropped.
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
	s.mu.Unlock()

	delay := s.RetryAfterRateLimit
	if delay <= 0 {
		delay = defaultRetryAfterRateLimit
	}
	go func() {
		t := time.NewTimer(delay)
		defer t.Stop()
		select {
		case <-ctx.Done():
			s.mu.Lock()
			delete(s.retrying, siteID)
			s.mu.Unlock()
		case <-t.C:
			s.mu.Lock()
			delete(s.retrying, siteID)
			s.mu.Unlock()
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
// interval until ctx ends. It is a function, not a method, so it is not
// exposed to the frontend.
func RunSyncLoop(ctx context.Context, s *SyncService, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		sites, err := s.Store.Sites(ctx)
		if err != nil {
			log.Printf("app: list sites for sync: %v", err)
		}
		for _, site := range sites {
			if s.anyBound(site) {
				s.SyncNow(site.ID)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
