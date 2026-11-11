// SPDX-License-Identifier: AGPL-3.0-or-later
package bingsync

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/bing"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

var ctx = context.Background()

type fakeKeys map[string]string

func (f fakeKeys) BingKey(account string) (string, error) {
	if k, ok := f[account]; ok {
		return k, nil
	}
	return "", bing.ErrInvalidKey
}

type fakeBing struct {
	calls int
	err   error
}

func (f *fakeBing) Traffic(_ context.Context, key, siteURL string) ([]bing.Traffic, error) {
	f.calls++
	if key != "k1" || siteURL != "https://example.com/" {
		return nil, errors.New("wrong key or site")
	}
	return []bing.Traffic{{Day: "2026-11-01", Clicks: 3, Impressions: 40}, {Day: "2026-11-02", Clicks: 1, Impressions: 9}}, f.err
}

func (f *fakeBing) Queries(_ context.Context, key, siteURL string) ([]bing.Stat, error) {
	f.calls++
	return []bing.Stat{{Week: "2026-10-26", Key: "thumbnail maker", Clicks: 2, Impressions: 30, Position: 7}}, nil
}

func (f *fakeBing) Pages(_ context.Context, key, siteURL string) ([]bing.Stat, error) {
	f.calls++
	return []bing.Stat{{Week: "2026-10-26", Key: "https://example.com/", Clicks: 2, Impressions: 30}}, nil
}

func setup(t *testing.T) (*store.Store, store.Site) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	site, _ := st.AddSite(ctx, "example.com", 1)
	if err := st.BindBing(ctx, site.ID, "bing-k1", "https://example.com/"); err != nil {
		t.Fatal(err)
	}
	site, _ = st.Site(ctx, site.ID)
	return st, site
}

func TestSyncSiteRefreshesEvery12Hours(t *testing.T) {
	st, site := setup(t)
	now := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	b := &fakeBing{}
	s := &Syncer{Store: st, Keys: fakeKeys{"bing-k1": "k1"}, Bing: b, Now: func() time.Time { return now }}

	res, err := s.SyncSite(ctx, site)
	if err != nil || res.Requests != 3 || b.calls != 3 {
		t.Fatalf("first run = %+v, %v (calls %d)", res, err, b.calls)
	}
	if tot, _ := st.BingTotals(ctx, site.ID, "2026-11-01", "2026-11-02"); tot.Clicks != 4 {
		t.Fatalf("totals = %+v", tot)
	}
	now = now.Add(6 * time.Hour)
	if res, _ := s.SyncSite(ctx, site); res.Requests != 0 {
		t.Fatalf("6h later made %d requests", res.Requests)
	}
	now = now.Add(7 * time.Hour)
	if res, err := s.SyncSite(ctx, site); err != nil || res.Requests != 3 {
		t.Fatalf("13h later = %+v, %v", res, err)
	}
}

func TestSyncSiteErrors(t *testing.T) {
	st, site := setup(t)
	s := &Syncer{Store: st, Keys: fakeKeys{"bing-k1": "k1"}, Bing: &fakeBing{err: bing.ErrThrottled}}
	if _, err := s.SyncSite(ctx, site); !errors.Is(err, bing.ErrThrottled) {
		t.Fatalf("err = %v", err)
	}
	// A failed fetch is not marked as fetched, so the next run tries again.
	if last, _ := st.LastFetched(ctx, site.ID, "bing", "fetch"); last != 0 {
		t.Fatalf("failed fetch was recorded: %d", last)
	}
	s.Keys = fakeKeys{}
	if _, err := s.SyncSite(ctx, site); !errors.Is(err, bing.ErrInvalidKey) {
		t.Fatalf("missing key: %v", err)
	}
	unbound, _ := st.AddSite(ctx, "other.example.com", 1)
	if _, err := s.SyncSite(ctx, unbound); !errors.Is(err, ErrNotBound) {
		t.Fatalf("unbound: %v", err)
	}
}

// spyLimiter is a Limiter stand-in that just records the keys Acquire was
// called with.
type spyLimiter struct {
	mu   sync.Mutex
	keys []string
}

func (l *spyLimiter) Acquire(_ context.Context, key string) (func(), error) {
	l.mu.Lock()
	l.keys = append(l.keys, key)
	l.mu.Unlock()
	return func() {}, nil
}

func TestSyncSiteUsesLimiterPerKey(t *testing.T) {
	st, site := setup(t)
	lim := &spyLimiter{}
	s := &Syncer{Store: st, Keys: fakeKeys{"bing-k1": "k1"}, Bing: &fakeBing{}, Limiter: lim,
		Now: func() time.Time { return time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC) }}
	if _, err := s.SyncSite(ctx, site); err != nil {
		t.Fatalf("SyncSite: %v", err)
	}
	if len(lim.keys) != 3 {
		t.Fatalf("Acquire called %d times, want 3 (query, page, traffic)", len(lim.keys))
	}
	for _, k := range lim.keys {
		if k != "k1" {
			t.Fatalf("Acquire key = %q, want the Bing API key", k)
		}
	}
}
