// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"testing"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

func TestOverviewTiles(t *testing.T) {
	st := openStore(t)
	site, _ := st.AddSite(ctx, "example.com", 1)
	o := &OverviewService{Store: st}
	if ov, err := o.Get(ctx, site.ID, 7); err != nil || ov.Tiles == nil || len(ov.Tiles) != 0 {
		t.Fatalf("no sources = %+v, %v", ov.Tiles, err)
	}
	st.BindGA(ctx, site.ID, "me@example.com", "properties/1")
	st.BindBing(ctx, site.ID, "bing-1234", "https://example.com/")
	var ga []store.GARow
	for d := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC); !d.After(time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)); d = d.AddDate(0, 0, 1) {
		ga = append(ga, store.GARow{Day: d.Format(store.DayLayout), Sessions: 2})
	}
	if err := st.ReplaceGA(ctx, site.ID, "date", "2026-10-20", "2026-11-08", ga, 1); err != nil {
		t.Fatal(err)
	}
	var days []store.BingDay
	for d := 1; d <= 7; d++ {
		if d != 4 { // Bing leaves out days without data
			days = append(days, store.BingDay{Day: "2026-11-0" + string(rune('0'+d)), Clicks: 1})
		}
	}
	if err := st.ReplaceBingDaily(ctx, site.ID, days, 1); err != nil {
		t.Fatal(err)
	}

	ov, err := o.Get(ctx, site.ID, 7)
	if err != nil || ov.Synced || len(ov.Tiles) != 2 {
		t.Fatalf("Get = %+v, %v (tiles show without Search Console)", ov, err)
	}
	g, b := ov.Tiles[0], ov.Tiles[1]
	if g.Source != "ga" || g.Label != "会话" || g.From != "2026-11-02" || g.To != "2026-11-08" || g.Current != 14 || g.Previous != 14 || len(g.Daily) != 7 {
		t.Fatalf("GA tile = %+v", g)
	}
	if b.Source != "bing" || b.To != "2026-11-07" || b.Current != 6 || b.Previous != 0 || len(b.Daily) != 7 || b.Daily[3] != 0 || b.Daily[0] != 1 {
		t.Fatalf("Bing tile = %+v", b)
	}
}
