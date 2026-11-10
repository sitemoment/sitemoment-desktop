// SPDX-License-Identifier: AGPL-3.0-or-later
package gscsync

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

// fakeGSC returns one row per day in the range for every slice.
type fakeGSC struct {
	mu    sync.Mutex
	calls []google.Query
	err   error
}

func (f *fakeGSC) Query(_ context.Context, token, property string, q google.Query) ([]google.Row, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, q)
	if f.err != nil {
		return nil, f.err
	}
	var rows []google.Row
	for d := day(q.Start); !d.After(day(q.End)); d = d.AddDate(0, 0, 1) {
		keys := []string{d.Format("2006-01-02")}
		switch len(q.Dimensions) {
		case 2:
			if q.Dimensions[1] == "query" {
				keys = append(keys, "thumbnail maker")
			} else {
				keys = append(keys, "https://example.com/")
			}
		}
		rows = append(rows, google.Row{Keys: keys, Clicks: 2, Impressions: 10, Position: 5})
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
	if err := st.BindGSC(ctx, site.ID, "me@example.com", "sc-domain:example.com"); err != nil {
		t.Fatal(err)
	}
	site, _ = st.Site(ctx, site.ID)
	return st, site
}

func TestSyncSiteBackfillsThenRefreshesTail(t *testing.T) {
	st, site := setup(t)
	now := time.Date(2026, 11, 10, 20, 0, 0, 0, time.UTC) // newest day: Nov 9
	gsc := &fakeGSC{}
	s := &Syncer{Store: st, Tokens: fakeTokens{}, GSC: gsc, Months: 1, Now: func() time.Time { return now }}

	res, err := s.SyncSite(ctx, site)
	if err != nil || res.Remaining != 0 {
		t.Fatalf("first run = %+v, %v", res, err)
	}
	// Oct 9 .. Nov 9 = 32 days: 1 request for "date", 32 each for "query" and "page".
	if res.Requests != 65 {
		t.Fatalf("requests = %d, want 65", res.Requests)
	}
	if days, _ := st.SyncDays(ctx, site.ID, "gsc", "query"); len(days) != 32 {
		t.Fatalf("query days = %d", len(days))
	}

	res, _ = s.SyncSite(ctx, site)
	if res.Requests != 0 {
		t.Fatalf("immediate re-run made %d requests", res.Requests)
	}

	now = now.Add(7 * time.Hour) // still Nov 9 as newest day; tail is stale
	res, err = s.SyncSite(ctx, site)
	if err != nil || res.Requests != 7 {
		t.Fatalf("tail refresh = %+v, %v (want 1 + 3 + 3)", res, err)
	}
}

func TestSyncSiteStopsAtMaxRequests(t *testing.T) {
	st, site := setup(t)
	s := &Syncer{Store: st, Tokens: fakeTokens{}, GSC: &fakeGSC{}, Months: 1, MaxRequests: 10,
		Now: func() time.Time { return time.Date(2026, 11, 10, 20, 0, 0, 0, time.UTC) }}
	res, err := s.SyncSite(ctx, site)
	if err != nil || res.Requests != 10 || res.Remaining != 55 {
		t.Fatalf("capped run = %+v, %v", res, err)
	}
}

func TestSyncSiteErrors(t *testing.T) {
	st, site := setup(t)
	s := &Syncer{Store: st, Tokens: fakeTokens{}, GSC: &fakeGSC{err: google.ErrReauth}, Months: 1}
	if _, err := s.SyncSite(ctx, site); !errors.Is(err, google.ErrReauth) {
		t.Fatalf("err = %v, want ErrReauth", err)
	}
	unbound, _ := st.AddSite(ctx, "other.example.com", 1)
	if _, err := s.SyncSite(ctx, unbound); !errors.Is(err, ErrNotBound) {
		t.Fatalf("unbound: %v", err)
	}
}
