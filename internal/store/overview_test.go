// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"math"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestOverviewQueries(t *testing.T) {
	st := open(t)
	site, _ := st.AddSite(ctx, "example.com", 1)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.ReplaceGSC(ctx, site.ID, "date", "2026-11-01", "2026-11-03", []GSCRow{
		{Day: "2026-11-01", Clicks: 10, Impressions: 100, Position: 5},
		{Day: "2026-11-02", Clicks: 20, Impressions: 300, Position: 9},
		{Day: "2026-11-03", Clicks: 0, Impressions: 0, Position: 0},
	}, 1))
	must(st.ReplaceGSC(ctx, site.ID, "query", "2026-10-31", "2026-11-02", []GSCRow{
		{Day: "2026-10-31", Query: "a", Clicks: 1, Impressions: 10, Position: 12},
		{Day: "2026-11-01", Query: "a", Clicks: 5, Impressions: 50, Position: 6},
		{Day: "2026-11-02", Query: "a", Clicks: 5, Impressions: 50, Position: 4},
		{Day: "2026-11-02", Query: "b", Clicks: 7, Impressions: 70, Position: 3},
		{Day: "2026-11-02", Query: "c", Clicks: 0, Impressions: 40, Position: 30},
	}, 1))

	tot, err := st.GSCTotals(ctx, site.ID, "2026-11-01", "2026-11-03")
	if err != nil || tot.Clicks != 30 || tot.Impressions != 400 || !near(tot.CTR, 0.075) || !near(tot.Position, 8) {
		t.Fatalf("GSCTotals = %+v, %v", tot, err) // position weighted by impressions: (5*100+9*300)/400 = 8
	}
	daily, err := st.GSCDaily(ctx, site.ID, "2026-11-01", "2026-11-03")
	if err != nil || len(daily) != 3 || daily[1].Clicks != 20 || daily[2].Day != "2026-11-03" {
		t.Fatalf("GSCDaily = %+v, %v", daily, err)
	}
	qs, err := st.TopQueries(ctx, site.ID, "2026-11-01", "2026-11-02", "2026-10-30", "2026-10-31", 2)
	if err != nil || len(qs) != 2 {
		t.Fatalf("TopQueries = %+v, %v", qs, err)
	}
	if qs[0].Query != "a" || qs[0].Clicks != 10 || !near(qs[0].Position, 5) || qs[0].PrevPosition == nil || *qs[0].PrevPosition != 12 {
		t.Fatalf("first = %+v", qs[0])
	}
	if qs[1].Query != "b" || qs[1].PrevPosition != nil {
		t.Fatalf("second = %+v", qs[1])
	}
	empty, err := st.GSCTotals(ctx, site.ID, "2020-01-01", "2020-01-31")
	if err != nil || empty.Clicks != 0 || empty.CTR != 0 || empty.Position != 0 {
		t.Fatalf("empty totals = %+v, %v", empty, err)
	}
}
