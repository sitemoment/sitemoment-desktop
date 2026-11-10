// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import "testing"

func countFacts(t *testing.T, st *Store, siteID int64, slice string) int {
	t.Helper()
	var n int
	if err := st.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM gsc_facts WHERE site_id = ? AND slice = ?", siteID, slice).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestReplaceGSC(t *testing.T) {
	st := open(t)
	site, _ := st.AddSite(ctx, "example.com", 1)
	rows := []GSCRow{
		{Day: "2026-11-01", Query: "a", Clicks: 3, Impressions: 10, Position: 4},
		{Day: "2026-11-01", Query: "b", Clicks: 1, Impressions: 5, Position: 8},
	}
	if err := st.ReplaceGSC(ctx, site.ID, "query", "2026-11-01", "2026-11-02", rows, 100); err != nil {
		t.Fatal(err)
	}
	days, err := st.SyncDays(ctx, site.ID, "gsc", "query")
	if err != nil || len(days) != 2 || days["2026-11-01"] != 100 || days["2026-11-02"] != 100 {
		t.Fatalf("SyncDays = %v, %v (an empty day still counts as fetched)", days, err)
	}
	if n := countFacts(t, st, site.ID, "query"); n != 2 {
		t.Fatalf("facts = %d", n)
	}
	// Re-fetching a day replaces its rows.
	if err := st.ReplaceGSC(ctx, site.ID, "query", "2026-11-01", "2026-11-01", []GSCRow{{Day: "2026-11-01", Query: "c", Clicks: 9, Impressions: 9, Position: 1}}, 200); err != nil {
		t.Fatal(err)
	}
	if n := countFacts(t, st, site.ID, "query"); n != 1 {
		t.Fatalf("after replace facts = %d", n)
	}
	if latest, err := st.LatestDay(ctx, site.ID, "gsc", "query"); err != nil || latest != "2026-11-02" {
		t.Fatalf("LatestDay = %q, %v", latest, err)
	}
	if latest, _ := st.LatestDay(ctx, site.ID, "gsc", "page"); latest != "" {
		t.Fatalf("LatestDay(empty) = %q", latest)
	}
	// Deleting the site removes its data.
	if err := st.DeleteSite(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	if n := countFacts(t, st, site.ID, "query"); n != 0 {
		t.Fatalf("facts after delete = %d", n)
	}
}
