// SPDX-License-Identifier: AGPL-3.0-or-later

// Package bingsync copies a site's Bing Webmaster data into the local
// database. Bing returns its whole (short) history on every call, so a run
// refreshes everything when the last successful fetch is older than
// RefreshAfter, and stores stats first and the daily traffic (which marks
// the fetch) last.
package bingsync

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/bing"
	"github.com/sitemoment/sitemoment-desktop/internal/gscsync"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

// Limiter additionally paces requests per key (a Bing API key); nil means
// no extra pacing. *ratelimit.Limiter satisfies this.
type Limiter interface {
	Acquire(ctx context.Context, key string) (release func(), err error)
}

// Keys looks up a Bing API key by its name.
type Keys interface {
	BingKey(account string) (string, error)
}

// Source is the Bing Webmaster API (bing.Client).
type Source interface {
	Traffic(ctx context.Context, key, siteURL string) ([]bing.Traffic, error)
	Queries(ctx context.Context, key, siteURL string) ([]bing.Stat, error)
	Pages(ctx context.Context, key, siteURL string) ([]bing.Stat, error)
}

// ErrNotBound means the site has no Bing site yet.
var ErrNotBound = errors.New("bingsync: site has no Bing site")

// Syncer copies one site's Bing data into the store.
type Syncer struct {
	Store        *store.Store
	Keys         Keys
	Bing         Source
	Now          func() time.Time // default time.Now
	RefreshAfter time.Duration    // default 12h
	// Limiter paces requests per Bing API key, across every site sharing
	// it; nil means no extra pacing.
	Limiter Limiter
}

// call runs fetch under Limiter's pacing for key (a no-op when Limiter is nil).
func (s *Syncer) call(ctx context.Context, key string, fetch func() error) error {
	var release func()
	if s.Limiter != nil {
		r, err := s.Limiter.Acquire(ctx, key)
		if err != nil {
			return err
		}
		release = r
	}
	err := fetch()
	if release != nil {
		release()
	}
	return err
}

// SyncSite refreshes the site's Bing data unless it was fetched recently.
func (s *Syncer) SyncSite(ctx context.Context, site store.Site) (gscsync.Result, error) {
	var res gscsync.Result
	if site.BingSite == "" || site.BingAccount == "" {
		return res, ErrNotBound
	}
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	every := s.RefreshAfter
	if every <= 0 {
		every = 12 * time.Hour
	}
	last, err := s.Store.LastFetched(ctx, site.ID, "bing", "fetch")
	if err != nil {
		return res, err
	}
	if last > now.Add(-every).Unix() {
		return res, nil
	}
	key, err := s.Keys.BingKey(site.BingAccount)
	if err != nil {
		return res, err
	}
	for _, kind := range []string{"query", "page"} {
		fetch := s.Bing.Queries
		if kind == "page" {
			fetch = s.Bing.Pages
		}
		var rows []bing.Stat
		err := s.call(ctx, key, func() error {
			r, err := fetch(ctx, key, site.BingSite)
			rows = r
			return err
		})
		res.Requests++
		if err != nil {
			return res, fmt.Errorf("Bing %s stats: %w", kind, err)
		}
		stats := make([]store.BingStat, 0, len(rows))
		for _, r := range rows {
			stats = append(stats, store.BingStat{Week: r.Week, Key: r.Key, Clicks: r.Clicks, Impressions: r.Impressions, Position: r.Position})
		}
		if err := s.Store.ReplaceBingStats(ctx, site.ID, kind, stats, now.Unix()); err != nil {
			return res, err
		}
	}
	var traffic []bing.Traffic
	err = s.call(ctx, key, func() error {
		t, err := s.Bing.Traffic(ctx, key, site.BingSite)
		traffic = t
		return err
	})
	res.Requests++
	if err != nil {
		return res, fmt.Errorf("Bing traffic: %w", err)
	}
	days := make([]store.BingDay, 0, len(traffic))
	for _, t := range traffic {
		days = append(days, store.BingDay{Day: t.Day, Clicks: t.Clicks, Impressions: t.Impressions})
	}
	return res, s.Store.ReplaceBingDaily(ctx, site.ID, days, now.Unix())
}
