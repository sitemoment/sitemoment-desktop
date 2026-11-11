// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"testing"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/gasync"
	"github.com/sitemoment/sitemoment-desktop/internal/gscsync"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

// findSource returns the named source's progress row, or fails the test.
func findSource(t *testing.T, sp SiteProgress, name string) SourceProgress {
	t.Helper()
	for _, src := range sp.Sources {
		if src.Source == name {
			return src
		}
	}
	t.Fatalf("site %d has no %q source in Progress(), got %+v", sp.SiteID, name, sp.Sources)
	return SourceProgress{}
}

// TestProgressGSCBackfillingThenDone checks that a freshly bound Search
// Console site starts out 回填中 (nothing covered yet) and becomes 已完成
// once every slice's ledger covers the whole progressMonths window.
func TestProgressGSCBackfillingThenDone(t *testing.T) {
	siteID, st := bindSite(t, "progress.example.com")
	s := &SyncService{Store: st}

	rows, err := s.Progress(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("Progress() = %d sites, want 1", len(rows))
	}
	gsc := findSource(t, rows[0], "gsc")
	if gsc.State != stateBackfilling {
		t.Fatalf("state = %q, want %q", gsc.State, stateBackfilling)
	}
	if len(gsc.Slices) != len(gscsync.Slices) {
		t.Fatalf("slices = %d, want %d", len(gsc.Slices), len(gscsync.Slices))
	}
	for _, sl := range gsc.Slices {
		if sl.DaysCovered != 0 || sl.DaysInRange <= 0 || sl.State != stateBackfilling {
			t.Fatalf("slice %+v, want 0 covered, > 0 in range, state %q", sl, stateBackfilling)
		}
	}

	// Fill every slice's ledger for the whole window.
	now := time.Now()
	through := gscsync.Through(now)
	from := through.AddDate(0, -progressMonths, 0)
	fromStr, throughStr := from.Format(store.DayLayout), through.Format(store.DayLayout)
	for _, sl := range gscsync.Slices {
		if err := st.ReplaceGSC(ctx, siteID, sl.Name, fromStr, throughStr, nil, now.Unix()); err != nil {
			t.Fatal(err)
		}
	}

	rows, err = s.Progress(ctx)
	if err != nil {
		t.Fatal(err)
	}
	gsc = findSource(t, rows[0], "gsc")
	if gsc.State != stateDone {
		t.Fatalf("state after filling the ledger = %q, want %q", gsc.State, stateDone)
	}
	for _, sl := range gsc.Slices {
		if sl.DaysCovered != sl.DaysInRange || sl.State != stateDone || sl.NewestDay != throughStr {
			t.Fatalf("slice %+v, want fully covered, state %q, newest %s", sl, stateDone, throughStr)
		}
	}
}

// TestProgressDoesNotFlipToBackfillingForRecentLag checks that a site
// fully covered everywhere except the newest couple of days (the normal
// day-by-day lag of a caught-up site: a tail refresh not due yet, or
// Search Console not having finalized today's data) still shows 已完成,
// not 回填中 — only a gap older than the recent window counts as a real
// backfill shortfall.
func TestProgressDoesNotFlipToBackfillingForRecentLag(t *testing.T) {
	siteID, st := bindSite(t, "recent-lag.example.com")
	s := &SyncService{Store: st}

	now := time.Now()
	through := gscsync.Through(now)
	from := through.AddDate(0, -progressMonths, 0)
	// Cover everything except the newest day of each slice.
	laggingThrough := through.AddDate(0, 0, -1)
	fromStr, laggingThroughStr := from.Format(store.DayLayout), laggingThrough.Format(store.DayLayout)
	for _, sl := range gscsync.Slices {
		if err := st.ReplaceGSC(ctx, siteID, sl.Name, fromStr, laggingThroughStr, nil, now.Unix()); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := s.Progress(ctx)
	if err != nil {
		t.Fatal(err)
	}
	gsc := findSource(t, rows[0], "gsc")
	if gsc.State != stateDone {
		t.Fatalf("state = %q, want %q (missing only the newest day, within the recent window)", gsc.State, stateDone)
	}
	for _, sl := range gsc.Slices {
		if sl.State != stateDone {
			t.Fatalf("slice %+v, want state %q", sl, stateDone)
		}
	}
}

// TestProgressNextBackfillAtUsesTheLoopsOwnSchedule checks that a
// backfilling source's NextBackfillAt comes from RunBackfillLoop's own
// tracked next-tick time, not status.At plus a fixed offset (which could
// be stale or just wrong if the run finished long before the loop's next
// real tick).
func TestProgressNextBackfillAtUsesTheLoopsOwnSchedule(t *testing.T) {
	siteID, st := bindSite(t, "next-backfill.example.com")
	s := &SyncService{Store: st}

	// A status.At far in the past: status.At + backfillLoopInterval would
	// give a very different (and, for a stale status, even past) answer
	// than the loop's own schedule.
	old := time.Now().Add(-24 * time.Hour).Unix()
	s.setStatus(SyncStatus{SiteID: siteID, State: "done", At: old})
	s.mu.Lock()
	s.backfillLeft = map[int64]bool{siteID: true}
	want := time.Now().Add(37 * time.Minute).Unix()
	s.nextBackfillAt = want
	s.mu.Unlock()

	rows, err := s.Progress(ctx)
	if err != nil {
		t.Fatal(err)
	}
	gsc := findSource(t, rows[0], "gsc")
	if gsc.State != stateBackfilling {
		t.Fatalf("state = %q, want %q", gsc.State, stateBackfilling)
	}
	if gsc.NextBackfillAt != want {
		t.Fatalf("NextBackfillAt = %d, want %d (the loop's own schedule, not status.At + an offset)", gsc.NextBackfillAt, want)
	}
}

// TestProgressSuppressesNextBackfillAtWhenSkipped checks that a site Stop
// just marked skipBackfill on does not show a next-backfill time: nothing
// is actually scheduled for it (RunBackfillLoop leaves it alone until a
// fresh SyncNow), so showing the loop's ordinary next tick there would be
// misleading.
func TestProgressSuppressesNextBackfillAtWhenSkipped(t *testing.T) {
	siteID, st := bindSite(t, "skip-next-backfill.example.com")
	s := &SyncService{Store: st}
	s.mu.Lock()
	s.backfillLeft = map[int64]bool{siteID: true}
	s.nextBackfillAt = time.Now().Add(37 * time.Minute).Unix()
	s.mu.Unlock()

	rows, err := s.Progress(ctx)
	if err != nil {
		t.Fatal(err)
	}
	gsc := findSource(t, rows[0], "gsc")
	if gsc.State != stateBackfilling {
		t.Fatalf("state = %q, want %q", gsc.State, stateBackfilling)
	}
	if gsc.NextBackfillAt == 0 {
		t.Fatal("NextBackfillAt = 0 before Stop, want the loop's schedule")
	}

	s.mu.Lock()
	s.skipBackfill = map[int64]bool{siteID: true}
	s.mu.Unlock()

	rows, err = s.Progress(ctx)
	if err != nil {
		t.Fatal(err)
	}
	gsc = findSource(t, rows[0], "gsc")
	if gsc.NextBackfillAt != 0 {
		t.Fatalf("NextBackfillAt = %d after Stop (skipBackfill), want 0 (shown as \"—\")", gsc.NextBackfillAt)
	}
}

// TestProgressStatesFollowRunFlags checks that Progress reflects paused,
// then a run in progress, then a failed run's error, in that priority
// order, for a GA4-bound site.
func TestProgressStatesFollowRunFlags(t *testing.T) {
	st := openStore(t)
	site, err := st.AddSite(ctx, "progress-ga.example.com", time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.BindGA(ctx, site.ID, "me@example.com", "properties/1"); err != nil {
		t.Fatal(err)
	}
	s := &SyncService{Store: st}

	if err := s.SetPaused(true); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Progress(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := findSource(t, rows[0], "ga").State; got != statePausedLabel {
		t.Fatalf("paused: state = %q, want %q", got, statePausedLabel)
	}

	if err := s.SetPaused(false); err != nil {
		t.Fatal(err)
	}
	s.setStatus(SyncStatus{SiteID: site.ID, State: "running", At: time.Now().Unix()})
	rows, err = s.Progress(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := findSource(t, rows[0], "ga").State; got != stateSyncing {
		t.Fatalf("running: state = %q, want %q", got, stateSyncing)
	}

	s.setStatus(SyncStatus{SiteID: site.ID, State: "failed", Error: "GA4：没有权限", At: time.Now().Unix()})
	s.mu.Lock()
	s.sourceErr = map[int64]map[string]string{site.ID: {"ga": "没有权限"}}
	s.mu.Unlock()
	rows, err = s.Progress(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ga := findSource(t, rows[0], "ga")
	if ga.State != stateFailedLabel || ga.Error != "没有权限" {
		t.Fatalf("failed: %+v", ga)
	}
	_ = gasync.Slices // documents which slices ga.Slices should enumerate
	if len(ga.Slices) != len(gasync.Slices) {
		t.Fatalf("ga slices = %d, want %d", len(ga.Slices), len(gasync.Slices))
	}
}

// TestProgressBing checks the simplified (no per-slice ledger) Bing row:
// 回填中 before the first fetch, 已完成 after.
func TestProgressBing(t *testing.T) {
	st := openStore(t)
	site, err := st.AddSite(ctx, "progress-bing.example.com", time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.BindBing(ctx, site.ID, "bing-k1", "https://progress-bing.example.com/"); err != nil {
		t.Fatal(err)
	}
	s := &SyncService{Store: st}

	rows, err := s.Progress(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := findSource(t, rows[0], "bing").State; got != stateBackfilling {
		t.Fatalf("before any fetch: state = %q, want %q", got, stateBackfilling)
	}

	now := time.Now().Unix()
	if err := st.ReplaceBingDaily(ctx, site.ID, []store.BingDay{{Day: "2026-11-01", Clicks: 1, Impressions: 10}}, now); err != nil {
		t.Fatal(err)
	}
	rows, err = s.Progress(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bing := findSource(t, rows[0], "bing")
	if bing.State != stateDone {
		t.Fatalf("after a fetch: state = %q, want %q", bing.State, stateDone)
	}
	if len(bing.Slices) != 1 || bing.Slices[0].LastFetched != now {
		t.Fatalf("bing slices = %+v, want 1 with LastFetched %d", bing.Slices, now)
	}
}
