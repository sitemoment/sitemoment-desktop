// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/google"
	"github.com/sitemoment/sitemoment-desktop/internal/gscsync"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

// SyncStatus is the latest sync state of a site, sent as event "sync:status".
type SyncStatus struct {
	SiteID   int64  `json:"siteId"`
	State    string `json:"state"` // running | done | failed
	Error    string `json:"error,omitempty"`
	Requests int    `json:"requests"`
	At       int64  `json:"at"`
}

// SyncService runs Search Console syncs in the background.
type SyncService struct {
	Syncer *gscsync.Syncer
	Store  *store.Store
	Emit   func(name string, data any)
	// Ctx is the context in-flight syncs run under; main.go sets it to a
	// context cancelled when the app quits. Not set by a frontend call: it
	// is a plain field (Wails binds exported methods, not fields), and
	// nothing exported here hands it back out. Defaults to
	// context.Background() when nil.
	Ctx context.Context

	mu     sync.Mutex
	status map[int64]SyncStatus
}

func (s *SyncService) context() context.Context {
	if s.Ctx != nil {
		return s.Ctx
	}
	return context.Background()
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

// SyncNow starts syncing a site unless it is already running. The check and
// the switch to "running" happen in one critical section, so two concurrent
// callers for the same site can never both start a run.
func (s *SyncService) SyncNow(siteID int64) {
	s.mu.Lock()
	if s.status == nil {
		s.status = map[int64]SyncStatus{}
	}
	if s.status[siteID].State == "running" {
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
	total := 0
	site, err := s.Store.Site(ctx, siteID)
	for err == nil {
		var res gscsync.Result
		res, err = s.Syncer.SyncSite(ctx, site)
		total += res.Requests
		if err != nil || res.Remaining == 0 {
			break
		}
	}
	st := SyncStatus{SiteID: siteID, State: "done", Requests: total, At: time.Now().Unix()}
	if err != nil {
		st.State, st.Error = "failed", syncMessage(err)
	}
	s.setStatus(st)
}

func syncMessage(err error) string {
	switch {
	case errors.Is(err, google.ErrReauth):
		return "Google 授权已失效，请在设置里重新连接"
	case errors.Is(err, google.ErrRateLimited):
		return "Search Console 限流，下次同步会继续"
	case errors.Is(err, gscsync.ErrNotBound):
		return "还没有选择 Search Console 资源"
	default:
		return err.Error()
	}
}

// RunSyncLoop syncs every bound site now and then every interval until ctx
// ends. It is a function, not a method, so it is not exposed to the frontend.
func RunSyncLoop(ctx context.Context, s *SyncService, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		sites, err := s.Store.Sites(ctx)
		if err != nil {
			log.Printf("app: list sites for sync: %v", err)
		}
		for _, site := range sites {
			if site.GSCProperty != "" {
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
