// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"errors"
	"testing"
)

func TestGSCQuery(t *testing.T) {
	st := open(t)
	site, err := st.AddSite(ctx, "example.com", 1)
	ok(t, err)
	ok(t, st.ReplaceGSC(ctx, site.ID, "query", "2026-11-01", "2026-11-02", []GSCRow{
		{Day: "2026-11-01", Query: "thumbnail maker", Clicks: 10, Impressions: 100, Position: 4},
		{Day: "2026-11-02", Query: "thumbnail maker", Clicks: 5, Impressions: 300, Position: 8},
		{Day: "2026-11-01", Query: "ai thumbnail", Clicks: 20, Impressions: 50, Position: 2},
		{Day: "2026-11-01", Query: "logo maker", Clicks: 1, Impressions: 10, Position: 30},
	}, 1))
	got, err := st.GSCQuery(ctx, FactQuery{SiteID: site.ID, Dim: "query", From: "2026-11-01", To: "2026-11-02", Contains: "THUMB"})
	ok(t, err)
	if len(got) != 2 || got[0].Key != "ai thumbnail" || got[1].Key != "thumbnail maker" {
		t.Fatalf("by clicks: %+v", got)
	}
	if tm := got[1]; tm.Clicks != 15 || tm.Impressions != 400 || tm.Position != 7 || tm.CTR != 15.0/400 {
		t.Fatalf("aggregate: %+v", tm)
	}
	got, err = st.GSCQuery(ctx, FactQuery{SiteID: site.ID, Dim: "query", From: "2026-11-01", To: "2026-11-02", OrderBy: "impressions", Exclude: "logo", Limit: 1})
	ok(t, err)
	if len(got) != 1 || got[0].Key != "thumbnail maker" {
		t.Fatalf("by impressions: %+v", got)
	}
	got, err = st.GSCQuery(ctx, FactQuery{SiteID: site.ID, Dim: "query", From: "2026-11-01", To: "2026-11-02", OrderBy: "ctr"})
	ok(t, err)
	if got[0].Key != "ai thumbnail" {
		t.Fatalf("by ctr: %+v", got)
	}
	ok(t, st.ReplaceGSC(ctx, site.ID, "date", "2026-11-01", "2026-11-02", []GSCRow{
		{Day: "2026-11-02", Clicks: 5, Impressions: 300, Position: 8},
		{Day: "2026-11-01", Clicks: 31, Impressions: 160, Position: 5},
	}, 1))
	got, err = st.GSCQuery(ctx, FactQuery{SiteID: site.ID, Dim: "date", From: "2026-11-01", To: "2026-11-30"})
	ok(t, err)
	if len(got) != 2 || got[0].Key != "2026-11-01" {
		t.Fatalf("by date: %+v", got)
	}
	if _, err := st.GSCQuery(ctx, FactQuery{SiteID: site.ID, Dim: "country"}); !errors.Is(err, ErrBadQuery) {
		t.Fatalf("bad dim: %v", err)
	}
	if _, err := st.GSCQuery(ctx, FactQuery{SiteID: site.ID, Dim: "query", OrderBy: "sessions"}); !errors.Is(err, ErrBadQuery) {
		t.Fatalf("bad metric: %v", err)
	}
}

func TestGAAndBingQuery(t *testing.T) {
	st := open(t)
	site, err := st.AddSite(ctx, "example.com", 1)
	ok(t, err)
	ok(t, st.ReplaceGA(ctx, site.ID, "channel", "2026-11-01", "2026-11-02", []GARow{
		{Day: "2026-11-01", Dim: "Organic Search", Sessions: 10, Users: 8, EngagedSessions: 6, KeyEvents: 1},
		{Day: "2026-11-02", Dim: "Organic Search", Sessions: 5, Users: 4, EngagedSessions: 3},
		{Day: "2026-11-01", Dim: "Direct", Sessions: 7, Users: 7},
	}, 1))
	ga, err := st.GAQuery(ctx, FactQuery{SiteID: site.ID, Dim: "channel", From: "2026-11-01", To: "2026-11-02"})
	ok(t, err)
	if len(ga) != 2 || ga[0].Key != "Organic Search" || ga[0].Sessions != 15 || ga[0].KeyEvents != 1 {
		t.Fatalf("ga: %+v", ga)
	}
	ok(t, st.ReplaceBingDaily(ctx, site.ID, []BingDay{{Day: "2026-11-01", Clicks: 3, Impressions: 30}}, 1))
	ok(t, st.ReplaceBingStats(ctx, site.ID, "query", []BingStat{
		{Week: "2026-10-27", Key: "thumbnail maker", Clicks: 2, Impressions: 20, Position: 5},
		{Week: "2026-11-03", Key: "thumbnail maker", Clicks: 1, Impressions: 60, Position: 9},
	}, 1))
	b, err := st.BingQuery(ctx, FactQuery{SiteID: site.ID, Dim: "query", From: "2026-10-01", To: "2026-11-30"})
	ok(t, err)
	if len(b) != 1 || b[0].Clicks != 3 || b[0].Impressions != 80 || b[0].Position != 8 {
		t.Fatalf("bing query: %+v", b)
	}
	b, err = st.BingQuery(ctx, FactQuery{SiteID: site.ID, Dim: "date", From: "2026-11-01", To: "2026-11-01"})
	ok(t, err)
	if len(b) != 1 || b[0].Key != "2026-11-01" || b[0].Clicks != 3 {
		t.Fatalf("bing date: %+v", b)
	}
}
