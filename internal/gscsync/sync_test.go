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
	// Oct 9 .. Nov 9 = 32 days, every day with impressions (the fake gives
	// every day a row): 1 request for "date"; for "query" and "page", the
	// 28-day recent window merges into 4 seven-day requests and the
	// remaining 4 older days merge into 1 more: 5 each.
	if res.Requests != 11 {
		t.Fatalf("requests = %d, want 11", res.Requests)
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
	// The 3 stale tail days (Nov 7-9) all have impressions, so each slice
	// re-fetches them merged into a single request: 1 (date) + 1 (query) + 1 (page).
	if err != nil || res.Requests != 3 {
		t.Fatalf("tail refresh = %+v, %v (want 3)", res, err)
	}
}

func TestSyncSiteStopsAtMaxRequests(t *testing.T) {
	st, site := setup(t)
	s := &Syncer{Store: st, Tokens: fakeTokens{}, GSC: &fakeGSC{}, Months: 1, MaxRequests: 10,
		Now: func() time.Time { return time.Date(2026, 11, 10, 20, 0, 0, 0, time.UTC) }}
	res, err := s.SyncSite(ctx, site)
	if err != nil || res.Requests != 10 || res.Remaining != 1 {
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

// fakeGSCDays returns rows only for days impressions(day) says have data
// (matching real Search Console: a day with no impressions has no row at
// all, not a zero-value row), and counts how many requests it served.
type fakeGSCDays struct {
	mu          sync.Mutex
	calls       int
	impressions func(d time.Time) float64
}

func (f *fakeGSCDays) Query(_ context.Context, token, property string, q google.Query) ([]google.Row, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	var rows []google.Row
	for d := day(q.Start); !d.After(day(q.End)); d = d.AddDate(0, 0, 1) {
		imp := f.impressions(d)
		if imp <= 0 {
			continue
		}
		keys := []string{d.Format("2006-01-02")}
		if len(q.Dimensions) == 2 {
			if q.Dimensions[1] == "query" {
				keys = append(keys, "thumbnail maker")
			} else {
				keys = append(keys, "https://example.com/")
			}
		}
		rows = append(rows, google.Row{Keys: keys, Clicks: imp / 5, Impressions: imp, Position: 5})
	}
	return rows, nil
}

// TestSyncSiteSkipsAndMergesMostlyEmptyHistory is the regression test for
// the slow first backfill: a 32-day window where only the newest 15 days
// have any Search Console data (the rest, like a brand-new property,
// return zero rows) must make far fewer requests than one per day.
func TestSyncSiteSkipsAndMergesMostlyEmptyHistory(t *testing.T) {
	st, site := setup(t)
	now := time.Date(2026, 11, 10, 20, 0, 0, 0, time.UTC) // newest day: Nov 9
	newestWithData := day("2026-10-26")                   // 15 days through Nov 9
	gsc := &fakeGSCDays{impressions: func(d time.Time) float64 {
		if d.Before(newestWithData) {
			return 0
		}
		return 10
	}}
	s := &Syncer{Store: st, Tokens: fakeTokens{}, GSC: gsc, Months: 1, Now: func() time.Time { return now }}

	res, err := s.SyncSite(ctx, site)
	if err != nil {
		t.Fatalf("SyncSite: %v", err)
	}
	// Before this feature: 1 (date) + 32 (query, one request per day) + 32
	// (page) = 65 requests. After: 1 (date, unchanged) + 3 + 3 (the 15
	// non-empty days merge into ceil(15/7) = 3 requests each for query and
	// page; the 17 empty days cost no request at all).
	if res.Requests != 7 {
		t.Fatalf("requests = %d, want 7 (was 65 before skip+merge)", res.Requests)
	}
	if res.Remaining != 0 {
		t.Fatalf("remaining = %d, want 0", res.Remaining)
	}
	// Every day is still recorded as fetched, including the empty ones.
	days, err := st.SyncDays(ctx, site.ID, "gsc", "query")
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 32 {
		t.Fatalf("query ledger has %d days, want 32", len(days))
	}
}

// TestSyncSiteBoundsConcurrency checks that SyncSite never runs more than
// Parallel GSC requests at once, and does use all of them when there is
// enough work.
func TestSyncSiteBoundsConcurrency(t *testing.T) {
	st, site := setup(t)
	var mu sync.Mutex
	var active, maxSeen int
	gsc := queryFunc(func(_ context.Context, _, _ string, q google.Query) ([]google.Row, error) {
		mu.Lock()
		active++
		if active > maxSeen {
			maxSeen = active
		}
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		mu.Lock()
		active--
		mu.Unlock()
		// Big impressions on every day defeat merging, so every day needs
		// its own request: enough fan-out to saturate Parallel.
		var rows []google.Row
		for d := day(q.Start); !d.After(day(q.End)); d = d.AddDate(0, 0, 1) {
			rows = append(rows, google.Row{Keys: []string{d.Format("2006-01-02")}, Clicks: 100, Impressions: 9999, Position: 1})
		}
		return rows, nil
	})
	s := &Syncer{Store: st, Tokens: fakeTokens{}, GSC: gsc, Months: 1, MaxRequests: 1000,
		Now: func() time.Time { return time.Date(2026, 11, 10, 20, 0, 0, 0, time.UTC) }}

	if _, err := s.SyncSite(ctx, site); err != nil {
		t.Fatalf("SyncSite: %v", err)
	}
	if maxSeen != 4 {
		t.Fatalf("max concurrent requests = %d, want 4", maxSeen)
	}
}

// queryFunc adapts a function to Querier.
type queryFunc func(ctx context.Context, token, property string, q google.Query) ([]google.Row, error)

func (f queryFunc) Query(ctx context.Context, token, property string, q google.Query) ([]google.Row, error) {
	return f(ctx, token, property, q)
}

// TestSyncSiteBatchesBackfill checks that history older than the recent
// window is fetched a little at a time (backfillBatch per call), newest
// first, resuming where the previous call left off, instead of in one long
// run.
func TestSyncSiteBatchesBackfill(t *testing.T) {
	st, site := setup(t)
	gsc := &fakeGSCDays{impressions: func(time.Time) float64 { return 9999 }} // never merges
	s := &Syncer{Store: st, Tokens: fakeTokens{}, GSC: gsc, Months: 6, MaxRequests: 100000,
		Now: func() time.Time { return time.Date(2026, 11, 10, 20, 0, 0, 0, time.UTC) }}

	res, err := s.SyncSite(ctx, site)
	if err != nil {
		t.Fatalf("SyncSite: %v", err)
	}
	if res.Remaining == 0 {
		t.Fatal("first call finished all of history in one go, want it capped")
	}
	firstRequests := res.Requests

	rounds := 1
	for res.Remaining > 0 && rounds < 50 {
		res, err = s.SyncSite(ctx, site)
		if err != nil {
			t.Fatalf("SyncSite round %d: %v", rounds, err)
		}
		rounds++
	}
	if res.Remaining != 0 {
		t.Fatalf("backfill never finished after %d rounds", rounds)
	}
	if rounds < 3 {
		t.Fatalf("backfill finished in %d round(s), want several (it is meant to be gradual); first call alone made %d requests", rounds, firstRequests)
	}

	days, err := st.SyncDays(ctx, site.ID, "gsc", "query")
	if err != nil {
		t.Fatal(err)
	}
	through := Through(s.Now())
	from := through.AddDate(0, -6, 0)
	want := Span{From: from, Through: through}.days()
	if len(days) != want {
		t.Fatalf("query ledger has %d days, want %d (the whole history)", len(days), want)
	}
}

// TestSyncSiteSharesBackfillBudgetBetweenQueryAndPage checks that
// BackfillBatch (20) bounds the *combined* number of older-history requests
// across query and page in one call — not 20 each (40 total) — and that
// the budget is spent newest-first across both.
func TestSyncSiteSharesBackfillBudgetBetweenQueryAndPage(t *testing.T) {
	st, site := setup(t)
	// Big impressions every day defeat merging, so every older day needs
	// its own request in each slice: comfortably more than BackfillBatch
	// candidates in query alone, let alone combined with page.
	gsc := &fakeGSCDays{impressions: func(time.Time) float64 { return 9999 }}
	s := &Syncer{Store: st, Tokens: fakeTokens{}, GSC: gsc, Months: 6, MaxRequests: 100000,
		Now: func() time.Time { return time.Date(2026, 11, 10, 20, 0, 0, 0, time.UTC) }}

	res, err := s.SyncSite(ctx, site)
	if err != nil {
		t.Fatalf("SyncSite: %v", err)
	}
	// 1 for "date" + RecentDays (28) each for query and page (unbatched) +
	// at most BackfillBatch (20) shared older requests.
	wantMax := 10 /* slack for the date slice's own chunked requests */ + 2*RecentDays + BackfillBatch
	if res.Requests > wantMax {
		t.Fatalf("requests = %d, want <= %d (BackfillBatch shared, not doubled)", res.Requests, wantMax)
	}

	queryDays, err := st.SyncDays(ctx, site.ID, "gsc", "query")
	if err != nil {
		t.Fatal(err)
	}
	pageDays, err := st.SyncDays(ctx, site.ID, "gsc", "page")
	if err != nil {
		t.Fatal(err)
	}
	through := Through(s.Now())
	recentFrom := through.AddDate(0, 0, -(RecentDays - 1))
	olderFetched := func(days map[string]int64) int {
		n := 0
		for d := range days {
			parsed, _ := time.Parse(store.DayLayout, d)
			if parsed.Before(recentFrom) {
				n++
			}
		}
		return n
	}
	older := olderFetched(queryDays) + olderFetched(pageDays)
	if older > BackfillBatch {
		t.Fatalf("older-history days actually fetched (query+page combined) = %d, want <= %d", older, BackfillBatch)
	}
}

// TestSyncSitePartialFailureRecordsOnlySuccessfulDays checks that when one
// of several concurrent requests fails, the days whose requests already
// succeeded are still recorded (so a retry does not redo them), while the
// failing day (and anything not yet attempted) is not.
func TestSyncSitePartialFailureRecordsOnlySuccessfulDays(t *testing.T) {
	st, site := setup(t)
	failDay := "2026-11-05"
	gsc := queryFunc(func(_ context.Context, _, _ string, q google.Query) ([]google.Row, error) {
		if q.Start == failDay {
			return nil, errors.New("boom")
		}
		// Big impressions on every day (including in the one "date" slice
		// request that covers the whole range) defeat merging, so query
		// and page each make one request per day — otherwise failDay could
		// end up merged together with other days and this test would not
		// be exercising a single failing request among independent ones.
		var rows []google.Row
		for d := day(q.Start); !d.After(day(q.End)); d = d.AddDate(0, 0, 1) {
			rows = append(rows, google.Row{Keys: []string{d.Format("2006-01-02")}, Clicks: 1, Impressions: 9999, Position: 1})
		}
		return rows, nil
	})
	s := &Syncer{Store: st, Tokens: fakeTokens{}, GSC: gsc, Months: 1, Parallel: 1,
		Now: func() time.Time { return time.Date(2026, 11, 10, 20, 0, 0, 0, time.UTC) }}

	if _, err := s.SyncSite(ctx, site); err == nil {
		t.Fatal("SyncSite succeeded, want the failing day's error")
	}
	// The "date" slice's single chunked request (Start Oct 9) succeeds
	// regardless (it never hits failDay); the failure is in the "query"
	// slice, where big impressions keep every day its own request.
	days, err := st.SyncDays(ctx, site.ID, "gsc", "query")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := days[failDay]; ok {
		t.Fatalf("the failing day %s was recorded as fetched", failDay)
	}
	// With Parallel: 1, everything newer than the failing day (fetched
	// first, newest-first) must have succeeded and been recorded.
	if _, ok := days["2026-11-09"]; !ok {
		t.Fatal("a day newer than the failing one was not recorded, want it recorded before the failure")
	}
}

// TestSyncSiteSplitsAMergedSpanIntoPerDayRecords checks that a query/page
// request covering several merged days stores each day's rows under its
// own day, not conflated into one.
func TestSyncSiteSplitsAMergedSpanIntoPerDayRecords(t *testing.T) {
	st, site := setup(t)
	gsc := queryFunc(func(_ context.Context, _, _ string, q google.Query) ([]google.Row, error) {
		var rows []google.Row
		for d := day(q.Start); !d.After(day(q.End)); d = d.AddDate(0, 0, 1) {
			// A distinct, day-dependent impression count, small enough to
			// stay well under the merge cap across a few days.
			imp := float64(100 + d.Day())
			rows = append(rows, google.Row{Keys: []string{d.Format("2006-01-02"), "thumbnail maker"}, Clicks: 1, Impressions: imp, Position: 1})
		}
		return rows, nil
	})
	s := &Syncer{Store: st, Tokens: fakeTokens{}, GSC: gsc, Months: 1,
		Now: func() time.Time { return time.Date(2026, 11, 10, 20, 0, 0, 0, time.UTC) }}
	if _, err := s.SyncSite(ctx, site); err != nil {
		t.Fatalf("SyncSite: %v", err)
	}

	for _, d := range []string{"2026-11-07", "2026-11-08", "2026-11-09"} {
		got, err := st.GSCQuery(ctx, store.FactQuery{SiteID: site.ID, Dim: "query", From: d, To: d})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 {
			t.Fatalf("day %s: %d rows, want 1", d, len(got))
		}
		want := 100 + day(d).Day()
		if got[0].Impressions != float64(want) {
			t.Fatalf("day %s: impressions = %v, want %d (merged days must not bleed into each other)", d, got[0].Impressions, want)
		}
	}
}

// TestSyncSiteRefetchesTailDayOnceDataArrives checks that a day within the
// tail that first shows zero impressions keeps being requested (not
// permanently skipped) until it ages out of the tail or real data shows up.
func TestSyncSiteRefetchesTailDayOnceDataArrives(t *testing.T) {
	st, site := setup(t)
	target := "2026-11-09" // the newest day: always within the tail
	hasData := false
	gsc := queryFunc(func(_ context.Context, _, _ string, q google.Query) ([]google.Row, error) {
		targetDay, start, end := day(target), day(q.Start), day(q.End)
		if !hasData || targetDay.Before(start) || targetDay.After(end) {
			return nil, nil // before data arrives, or target is not in this request's range: no rows
		}
		return []google.Row{{Keys: []string{target, "thumbnail maker"}, Clicks: 3, Impressions: 50, Position: 2}}, nil
	})
	now := time.Date(2026, 11, 10, 20, 0, 0, 0, time.UTC)
	s := &Syncer{Store: st, Tokens: fakeTokens{}, GSC: gsc, Months: 1, RefreshAfter: time.Hour,
		Now: func() time.Time { return now }}

	if _, err := s.SyncSite(ctx, site); err != nil {
		t.Fatalf("first run: %v", err)
	}
	got, err := st.GSCQuery(ctx, store.FactQuery{SiteID: site.ID, Dim: "query", From: target, To: target})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("before data arrives: %+v, want no rows yet", got)
	}

	hasData = true
	now = now.Add(2 * time.Hour) // past RefreshAfter: the tail day is due again
	if _, err := s.SyncSite(ctx, site); err != nil {
		t.Fatalf("second run: %v", err)
	}
	got, err = st.GSCQuery(ctx, store.FactQuery{SiteID: site.ID, Dim: "query", From: target, To: target})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Impressions != 50 {
		t.Fatalf("after data arrives: %+v, want one row with 50 impressions", got)
	}
}
