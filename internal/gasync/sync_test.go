// SPDX-License-Identifier: AGPL-3.0-or-later
package gasync

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/google"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

var ctx = context.Background()

type fakeTokens struct{}

func (fakeTokens) AccessToken(context.Context, string) (string, error) { return "tok", nil }

// fakeGA returns one row per day in the range; second dimensions get "/".
type fakeGA struct {
	mu    sync.Mutex
	calls []google.GAReport
	err   error
}

func (f *fakeGA) Report(_ context.Context, token, property string, r google.GAReport) ([]google.GARow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r)
	if f.err != nil {
		return nil, f.err
	}
	start, _ := time.Parse("2006-01-02", r.Start)
	end, _ := time.Parse("2006-01-02", r.End)
	var rows []google.GARow
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		dims := []string{d.Format("20060102")}
		if len(r.Dimensions) == 2 {
			dims = append(dims, "/")
		}
		rows = append(rows, google.GARow{Dims: dims, Metrics: []float64{5, 4, 3, 1}})
	}
	return rows, nil
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
	if err := st.BindGA(ctx, site.ID, "me@example.com", "properties/123"); err != nil {
		t.Fatal(err)
	}
	site, _ = st.Site(ctx, site.ID)
	return st, site
}

func TestThroughIsYesterdayLocal(t *testing.T) {
	if got := Through(time.Date(2026, 11, 10, 1, 0, 0, 0, time.UTC)); !got.Equal(time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("Through = %v", got)
	}
	shanghai := time.FixedZone("CST", 8*3600)
	if got := Through(time.Date(2026, 11, 10, 0, 30, 0, 0, shanghai)); !got.Equal(time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("Through(Shanghai) = %v", got)
	}
}

func TestSyncSiteBackfillsThenRefreshesTail(t *testing.T) {
	st, site := setup(t)
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC) // newest day: Nov 9
	ga := &fakeGA{}
	s := &Syncer{Store: st, Tokens: fakeTokens{}, GA: ga, Months: 1, Now: func() time.Time { return now }}

	res, err := s.SyncSite(ctx, site)
	if err != nil || res.Remaining != 0 {
		t.Fatalf("first run = %+v, %v", res, err)
	}
	// Oct 9 .. Nov 9 = 32 days: date in 1 request (90-day chunks), landing in 5 (7-day), channel in 2 (30-day).
	if res.Requests != 8 {
		t.Fatalf("requests = %d, want 8", res.Requests)
	}
	tot, _ := st.GATotals(ctx, site.ID, "2026-10-09", "2026-11-09")
	if tot.Sessions != 32*5 || tot.KeyEvents != 32 {
		t.Fatalf("totals = %+v", tot)
	}
	if days, _ := st.SyncDays(ctx, site.ID, "ga", "landing"); len(days) != 32 {
		t.Fatalf("landing days = %d", len(days))
	}
	for _, c := range ga.calls {
		if c.Metrics[0] != "sessions" || c.Metrics[3] != "keyEvents" {
			t.Fatalf("metrics = %v", c.Metrics)
		}
	}

	if res, _ = s.SyncSite(ctx, site); res.Requests != 0 {
		t.Fatalf("immediate re-run made %d requests", res.Requests)
	}
	now = now.Add(7 * time.Hour) // still Nov 10 (newest day Nov 9); the tail Nov 7..9 is stale
	if res, err = s.SyncSite(ctx, site); err != nil || res.Requests != 3 {
		t.Fatalf("tail refresh = %+v, %v (want one 3-day request per slice)", res, err)
	}
}

func TestSyncSiteStopsAtMaxRequests(t *testing.T) {
	st, site := setup(t)
	s := &Syncer{Store: st, Tokens: fakeTokens{}, GA: &fakeGA{}, Months: 1, MaxRequests: 3,
		Now: func() time.Time { return time.Date(2026, 11, 10, 20, 0, 0, 0, time.UTC) }}
	res, err := s.SyncSite(ctx, site)
	if err != nil || res.Requests != 3 || res.Remaining != 5 {
		t.Fatalf("capped run = %+v, %v", res, err)
	}
}

func TestSyncSiteErrors(t *testing.T) {
	st, site := setup(t)
	s := &Syncer{Store: st, Tokens: fakeTokens{}, GA: &fakeGA{err: google.ErrGAScope}, Months: 1}
	if _, err := s.SyncSite(ctx, site); !errors.Is(err, google.ErrGAScope) {
		t.Fatalf("err = %v, want ErrGAScope", err)
	}
	unbound, _ := st.AddSite(ctx, "other.example.com", 1)
	if _, err := s.SyncSite(ctx, unbound); !errors.Is(err, ErrNotBound) {
		t.Fatalf("unbound: %v", err)
	}
}

// spyLimiter is a ratelimit.Limiter stand-in that just records the keys
// Acquire was called with.
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

func TestSyncSiteUsesLimiterPerAccount(t *testing.T) {
	st, site := setup(t)
	lim := &spyLimiter{}
	s := &Syncer{Store: st, Tokens: fakeTokens{}, GA: &fakeGA{}, Months: 1, Limiter: lim,
		Now: func() time.Time { return time.Date(2026, 11, 10, 20, 0, 0, 0, time.UTC) }}
	if _, err := s.SyncSite(ctx, site); err != nil {
		t.Fatalf("SyncSite: %v", err)
	}
	if len(lim.keys) == 0 {
		t.Fatal("Limiter.Acquire was never called")
	}
	for _, k := range lim.keys {
		if k != "me@example.com" {
			t.Fatalf("Acquire key = %q, want the GA account", k)
		}
	}
}
