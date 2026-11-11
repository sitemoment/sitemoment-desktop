// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"errors"
	"reflect"
	"testing"
)

func countRows(t *testing.T, st *Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := st.db.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestReplaceGAAndTotals(t *testing.T) {
	st := open(t)
	site, _ := st.AddSite(ctx, "example.com", 1)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.ReplaceGA(ctx, site.ID, "date", "2026-11-01", "2026-11-03", []GARow{
		{Day: "2026-11-01", Sessions: 10, Users: 8, EngagedSessions: 6, KeyEvents: 1},
		{Day: "2026-11-02", Sessions: 20, Users: 15, EngagedSessions: 12, KeyEvents: 2},
	}, 100))
	must(st.ReplaceGA(ctx, site.ID, "landing", "2026-11-01", "2026-11-01", []GARow{
		{Day: "2026-11-01", Dim: "/", Sessions: 7},
		{Day: "2026-11-01", Dim: "/pricing", Sessions: 3},
	}, 100))
	if days, err := st.SyncDays(ctx, site.ID, "ga", "date"); err != nil || len(days) != 3 || days["2026-11-03"] != 100 {
		t.Fatalf("SyncDays = %v, %v", days, err)
	}
	if latest, _ := st.LatestDay(ctx, site.ID, "ga", "date"); latest != "2026-11-03" {
		t.Fatalf("LatestDay = %q", latest)
	}
	if latest, _ := st.LatestDay(ctx, site.ID, "gsc", "date"); latest != "" {
		t.Fatalf("GA days leaked into the GSC ledger: %q", latest)
	}
	tot, err := st.GATotals(ctx, site.ID, "2026-11-01", "2026-11-03")
	if err != nil || tot != (GATotals{Sessions: 30, Users: 23, EngagedSessions: 18, KeyEvents: 3}) {
		t.Fatalf("GATotals = %+v, %v", tot, err)
	}
	daily, err := st.GADaily(ctx, site.ID, "2026-11-01", "2026-11-03")
	if err != nil || !reflect.DeepEqual(daily, []DayValue{{Day: "2026-11-01", Value: 10}, {Day: "2026-11-02", Value: 20}}) {
		t.Fatalf("GADaily = %+v, %v", daily, err)
	}
	// Re-fetching a day replaces its rows.
	must(st.ReplaceGA(ctx, site.ID, "landing", "2026-11-01", "2026-11-01", []GARow{{Day: "2026-11-01", Dim: "/", Sessions: 9}}, 200))
	if n := countRows(t, st, "SELECT COUNT(*) FROM ga_facts WHERE site_id = ? AND slice = 'landing'", site.ID); n != 1 {
		t.Fatalf("landing rows = %d", n)
	}
	if err := st.ReplaceGA(ctx, site.ID, "date", "nope", "2026-11-01", nil, 1); err == nil {
		t.Fatal("bad day accepted")
	}
}

func TestBindGADropsOldPropertyData(t *testing.T) {
	st := open(t)
	site, _ := st.AddSite(ctx, "example.com", 1)
	if err := st.BindGA(ctx, site.ID, "me@example.com", "properties/1"); err != nil {
		t.Fatal(err)
	}
	got, _ := st.Site(ctx, site.ID)
	if got.GAAccount != "me@example.com" || got.GAProperty != "properties/1" || got.BingSite != "" {
		t.Fatalf("site = %+v", got)
	}
	st.ReplaceGA(ctx, site.ID, "date", "2026-11-01", "2026-11-01", []GARow{{Day: "2026-11-01", Sessions: 1}}, 1)
	st.ReplaceGSC(ctx, site.ID, "date", "2026-11-01", "2026-11-01", []GSCRow{{Day: "2026-11-01", Clicks: 1}}, 1)

	// Same property again (e.g. another account that can read it): data kept.
	if err := st.BindGA(ctx, site.ID, "other@example.com", "properties/1"); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, st, "SELECT COUNT(*) FROM ga_facts WHERE site_id = ?", site.ID); n != 1 {
		t.Fatalf("ga rows after same bind = %d", n)
	}
	// Another property: GA data and ledger gone, GSC untouched.
	if err := st.BindGA(ctx, site.ID, "me@example.com", "properties/2"); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, st, "SELECT COUNT(*) FROM ga_facts WHERE site_id = ?", site.ID); n != 0 {
		t.Fatalf("ga rows after rebind = %d", n)
	}
	if n := countRows(t, st, "SELECT COUNT(*) FROM sync_days WHERE site_id = ? AND source = 'ga'", site.ID); n != 0 {
		t.Fatalf("ga ledger after rebind = %d", n)
	}
	if n := countRows(t, st, "SELECT COUNT(*) FROM sync_days WHERE site_id = ? AND source = 'gsc'", site.ID); n != 1 {
		t.Fatalf("gsc ledger after GA rebind = %d", n)
	}
	// Unbind.
	if err := st.BindGA(ctx, site.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Site(ctx, site.ID); got.GAProperty != "" || got.GAAccount != "" {
		t.Fatalf("after unbind = %+v", got)
	}
	if err := st.BindGA(ctx, 999, "a", "properties/1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown site: %v", err)
	}
}
