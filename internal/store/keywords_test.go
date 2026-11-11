// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"errors"
	"testing"
)

func TestKeywordKindsAndMetrics(t *testing.T) {
	st := open(t)
	site, _ := st.AddSite(ctx, "example.com", 1)
	k, err := st.AddKeyword(ctx, site.ID, "  AI   Chat ", KindTarget, 10)
	if err != nil || k.Keyword != "ai chat" || k.Kind != KindTarget || k.Volume.Value != nil || k.CreatedAt != 10 {
		t.Fatalf("AddKeyword = %+v, %v", k, err)
	}
	// Search Console finding it again does not demote a user's choice.
	if k2, _ := st.AddKeyword(ctx, site.ID, "ai chat", KindFound, 20); k2.ID != k.ID || k2.Kind != KindTarget {
		t.Fatalf("re-add as found = %+v", k2)
	}
	if k3, _ := st.AddKeyword(ctx, site.ID, "AI chat", KindScene, 30); k3.Kind != KindScene || k3.CreatedAt != 10 {
		t.Fatalf("re-add as scene = %+v", k3)
	}
	if _, err := st.AddKeyword(ctx, site.ID, "   ", KindTarget, 1); !errors.Is(err, ErrBadKeyword) {
		t.Fatalf("empty keyword: %v", err)
	}
	if _, err := st.AddKeyword(ctx, site.ID, "x", "nope", 1); !errors.Is(err, ErrBadKind) {
		t.Fatalf("bad kind: %v", err)
	}

	set := func(metric string, v float64, source string, at int64) bool {
		t.Helper()
		wrote, err := st.SetKeywordMetric(ctx, k.ID, metric, v, source, at)
		if err != nil {
			t.Fatal(err)
		}
		return wrote
	}
	if !set("volume", 100, "bing", 1) || !set("volume", 1900, "sitemoment", 2) || set("volume", 120, "bing", 3) {
		t.Fatal("source precedence: bing < sitemoment")
	}
	if !set("kd", 34, "sitemoment", 2) || !set("volume", 2000, "sitemoment", 4) {
		t.Fatal("same source must overwrite")
	}
	got, _ := st.Keyword(ctx, k.ID)
	if got.Volume.Value == nil || *got.Volume.Value != 2000 || got.Volume.Source != "sitemoment" || got.Volume.At != 4 ||
		got.KD.Value == nil || *got.KD.Value != 34 || got.CPC.Value != nil {
		t.Fatalf("metrics = %+v", got)
	}
	if _, err := st.SetKeywordMetric(ctx, k.ID, "clicks", 1, "bing", 1); err == nil {
		t.Fatal("unknown metric accepted")
	}
	if _, err := st.SetKeywordMetric(ctx, k.ID, "kd", 1, "guess", 1); err == nil {
		t.Fatal("unknown source accepted")
	}
	if _, err := st.SetKeywordMetric(ctx, 999, "kd", 1, "bing", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}

	if err := st.UpdateKeyword(ctx, k.ID, KindTarget, "主攻词"); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Keyword(ctx, k.ID); got.Kind != KindTarget || got.Notes != "主攻词" {
		t.Fatalf("after update = %+v", got)
	}
	if err := st.UpdateKeyword(ctx, 999, KindTarget, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update unknown: %v", err)
	}
	if err := st.DeleteKeyword(ctx, k.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Keyword(ctx, k.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
}

func TestFoundKeywordsAndSearchConsoleRank(t *testing.T) {
	st := open(t)
	site, _ := st.AddSite(ctx, "example.com", 1)
	if err := st.ReplaceGSC(ctx, site.ID, "query", "2026-11-01", "2026-11-02", []GSCRow{
		{Day: "2026-11-01", Query: "ai chat", Clicks: 5, Impressions: 50, Position: 4},
		{Day: "2026-11-02", Query: "ai chat", Clicks: 5, Impressions: 50, Position: 6},
		{Day: "2026-11-02", Query: "logo maker", Clicks: 1, Impressions: 80, Position: 12},
		{Day: "2026-11-02", Query: "rare", Clicks: 0, Impressions: 1, Position: 40},
	}, 1); err != nil {
		t.Fatal(err)
	}
	st.AddKeyword(ctx, site.ID, "ai chat", KindScene, 1)
	st.AddKeyword(ctx, site.ID, "brand new", KindTarget, 1)

	n, err := st.AddFoundKeywords(ctx, site.ID, "2026-11-01", "2026-11-02", 2, 5)
	if err != nil || n != 1 {
		t.Fatalf("AddFoundKeywords = %d, %v (top 2 by impressions, ai chat already there)", n, err)
	}
	list, err := st.Keywords(ctx, site.ID, "", "2026-11-01", "2026-11-02")
	if err != nil || len(list) != 3 {
		t.Fatalf("Keywords = %+v, %v", list, err)
	}
	if list[0].Keyword != "brand new" || list[0].Position != nil || list[0].Impressions != 0 {
		t.Fatalf("first (target, no GSC data) = %+v", list[0])
	}
	if list[1].Keyword != "ai chat" || list[1].Kind != KindScene || list[1].Clicks != 10 || list[1].Impressions != 100 ||
		list[1].Position == nil || !near(*list[1].Position, 5) {
		t.Fatalf("second (scene, weighted position 5) = %+v", list[1])
	}
	if list[2].Keyword != "logo maker" || list[2].Kind != KindFound || !near(*list[2].Position, 12) {
		t.Fatalf("third (found) = %+v", list[2])
	}
	found, err := st.Keywords(ctx, site.ID, KindFound, "2026-11-01", "2026-11-02")
	if err != nil || len(found) != 1 || found[0].Keyword != "logo maker" {
		t.Fatalf("found only = %+v, %v", found, err)
	}
	if none, err := st.Keywords(ctx, site.ID, "", "", ""); err != nil || len(none) != 3 || none[1].Position != nil {
		t.Fatalf("without a GSC window = %+v, %v", none, err)
	}
	if err := st.DeleteSite(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, st, "SELECT COUNT(*) FROM keywords"); n != 0 {
		t.Fatalf("keywords after site delete = %d", n)
	}
}
