// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"reflect"
	"testing"
	"time"
)

func TestBingDailyAndStats(t *testing.T) {
	st := open(t)
	site, _ := st.AddSite(ctx, "example.com", 1)
	at := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC).Unix()
	if last, err := st.LastFetched(ctx, site.ID, "bing", "fetch"); err != nil || last != 0 {
		t.Fatalf("LastFetched before = %d, %v", last, err)
	}
	if err := st.ReplaceBingDaily(ctx, site.ID, []BingDay{
		{Day: "2026-11-01", Clicks: 3, Impressions: 40},
		{Day: "2026-11-03", Clicks: 1, Impressions: 10},
	}, at); err != nil {
		t.Fatal(err)
	}
	if last, _ := st.LastFetched(ctx, site.ID, "bing", "fetch"); last != at {
		t.Fatalf("LastFetched = %d, want %d", last, at)
	}
	if latest, _ := st.LatestDay(ctx, site.ID, "bing", "traffic"); latest != "2026-11-03" {
		t.Fatalf("LatestDay = %q", latest)
	}
	// A later fetch overwrites the days it returns and keeps older ones.
	if err := st.ReplaceBingDaily(ctx, site.ID, []BingDay{{Day: "2026-11-03", Clicks: 2, Impressions: 12}}, at+3600); err != nil {
		t.Fatal(err)
	}
	tot, err := st.BingTotals(ctx, site.ID, "2026-11-01", "2026-11-03")
	if err != nil || tot != (BingTotals{Clicks: 5, Impressions: 52}) {
		t.Fatalf("BingTotals = %+v, %v", tot, err)
	}
	daily, err := st.BingDaily(ctx, site.ID, "2026-11-01", "2026-11-03")
	if err != nil || !reflect.DeepEqual(daily, []DayValue{{Day: "2026-11-01", Value: 3}, {Day: "2026-11-03", Value: 2}}) {
		t.Fatalf("BingDaily = %+v, %v", daily, err)
	}

	if err := st.ReplaceBingStats(ctx, site.ID, "query", []BingStat{
		{Week: "2026-11-02", Key: "thumbnail maker", Clicks: 2, Impressions: 30, Position: 7},
		{Week: "2026-11-02", Key: "thumbnail maker", Clicks: 2, Impressions: 30, Position: 7}, // duplicates collapse
		{Week: "2026-10-26", Key: "logo maker", Clicks: 0, Impressions: 5, Position: 20},
	}, at); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceBingStats(ctx, site.ID, "page", []BingStat{{Week: "2026-11-02", Key: "https://example.com/", Clicks: 1, Impressions: 9}}, at); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, st, "SELECT COUNT(*) FROM bing_queries WHERE site_id = ?", site.ID); n != 2 {
		t.Fatalf("query rows = %d", n)
	}
	if n := countRows(t, st, "SELECT COUNT(*) FROM bing_pages WHERE site_id = ?", site.ID); n != 1 {
		t.Fatalf("page rows = %d", n)
	}
	if err := st.ReplaceBingStats(ctx, site.ID, "nope", nil, at); err == nil {
		t.Fatal("unknown kind accepted")
	}
}

func TestBindBingDropsOldSiteData(t *testing.T) {
	st := open(t)
	site, _ := st.AddSite(ctx, "example.com", 1)
	if err := st.BindBing(ctx, site.ID, "bing-abcd", "https://example.com/"); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Site(ctx, site.ID); got.BingAccount != "bing-abcd" || got.BingSite != "https://example.com/" {
		t.Fatalf("site = %+v", got)
	}
	st.ReplaceBingDaily(ctx, site.ID, []BingDay{{Day: "2026-11-01", Clicks: 1}}, 1)
	st.ReplaceBingStats(ctx, site.ID, "query", []BingStat{{Week: "2026-11-02", Key: "a"}}, 1)
	if err := st.BindBing(ctx, site.ID, "bing-abcd", "https://www.example.com/"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"bing_daily", "bing_queries", "bing_pages"} {
		if n := countRows(t, st, "SELECT COUNT(*) FROM "+table+" WHERE site_id = ?", site.ID); n != 0 {
			t.Fatalf("%s rows after rebind = %d", table, n)
		}
	}
	if last, _ := st.LastFetched(ctx, site.ID, "bing", "fetch"); last != 0 {
		t.Fatalf("ledger survived rebind: %d", last)
	}
}
