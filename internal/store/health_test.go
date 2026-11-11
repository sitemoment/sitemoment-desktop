// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"errors"
	"testing"
)

func TestCrawlsAndIssues(t *testing.T) {
	st := open(t)
	site, err := st.AddSite(ctx, "example.com", 1)
	ok(t, err)
	if _, err := st.LatestCrawl(ctx, site.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LatestCrawl on a new site: %v", err)
	}
	id1, err := st.StartCrawl(ctx, site.ID, "schedule", 100)
	ok(t, err)
	ok(t, st.FinishCrawl(ctx, Crawl{ID: id1, FinishedAt: 160, Pages: 10, OKPages: 9, Score: 67, P0: 1, P1: 1, Summary: `{"x":1}`},
		[]Issue{{Code: "lost_pages", Severity: "p0", Title: "以前正常、现在报错的页面", Count: 1, URLs: []string{"https://example.com/a → 404"}, New: true},
			{Code: "missing_title", Severity: "p1", Title: "页面缺少标题", Count: 2}}))
	if err := st.FinishCrawl(ctx, Crawl{ID: id1}, nil); err == nil {
		t.Fatal("finished a crawl twice")
	}
	id2, err := st.StartCrawl(ctx, site.ID, "manual", 200)
	ok(t, err)
	ok(t, st.FailCrawl(ctx, id2, "网站打不开：dns", 210))
	id3, err := st.StartCrawl(ctx, site.ID, "manual", 300)
	ok(t, err)
	ok(t, st.AbortRunningCrawls(ctx, "应用退出，体检中断", 400))

	latest, err := st.LatestCrawl(ctx, site.ID)
	ok(t, err)
	if latest.ID != id1 || latest.Score != 67 || latest.Status != "done" || latest.Summary != `{"x":1}` {
		t.Fatalf("LatestCrawl = %+v", latest)
	}
	list, err := st.Crawls(ctx, site.ID, 10)
	ok(t, err)
	if len(list) != 3 || list[0].ID != id3 || list[0].Status != "failed" || list[0].Error != "应用退出，体检中断" || list[1].Error != "网站打不开：dns" {
		t.Fatalf("Crawls = %+v", list)
	}
	if at, _ := st.LastCrawlAt(ctx, site.ID); at != 300 {
		t.Fatalf("LastCrawlAt = %d", at)
	}
	if c, err := st.CrawlBefore(ctx, site.ID, 150); err != nil || c.ID != id1 {
		t.Fatalf("CrawlBefore = %+v, %v", c, err)
	}
	issues, err := st.CrawlIssues(ctx, id1)
	ok(t, err)
	if len(issues) != 2 || issues[0].Code != "lost_pages" || !issues[0].New || len(issues[0].URLs) != 1 || issues[1].URLs == nil {
		t.Fatalf("CrawlIssues = %+v", issues)
	}
	hs, err := st.SiteHealths(ctx)
	ok(t, err)
	if len(hs) != 1 || hs[0].Score != 67 || hs[0].CrawlID != id1 || hs[0].CheckedAt != 160 {
		t.Fatalf("SiteHealths = %+v", hs)
	}
	ok(t, st.PruneCrawls(ctx, site.ID, 1))
	if list, _ := st.Crawls(ctx, site.ID, 10); len(list) != 1 || list[0].ID != id3 {
		t.Fatalf("after prune: %+v", list)
	}
	if issues, _ := st.CrawlIssues(ctx, id1); len(issues) != 0 {
		t.Fatalf("issues outlived their crawl: %+v", issues)
	}
}

func TestPagesMemory(t *testing.T) {
	st := open(t)
	site, err := st.AddSite(ctx, "example.com", 1)
	ok(t, err)
	ok(t, st.SavePages(ctx, site.ID, []Page{
		{URL: "https://example.com/", FinalURL: "https://example.com/", Status: 200, OK: true, Title: "Home", LastSeen: 100},
		{URL: "https://example.com/a", FinalURL: "https://example.com/a", Status: 200, OK: true, LastSeen: 100},
	}))
	ok(t, st.SetInspection(ctx, site.ID, "https://example.com/a", "NEUTRAL", "Crawled - currently not indexed", 150))
	ok(t, st.SetInspection(ctx, site.ID, "https://example.com/task-page", "PASS", "Submitted and indexed", 150))
	ok(t, st.SavePages(ctx, site.ID, []Page{
		{URL: "https://example.com/a", FinalURL: "https://example.com/a", Status: 404, LastSeen: 200},
	}))
	pages, err := st.SitePages(ctx, site.ID)
	ok(t, err)
	a := pages["https://example.com/a"]
	if a.Status != 404 || a.OK || a.FirstSeen != 100 || a.LastOK != 100 || a.LastSeen != 200 || a.IndexVerdict != "NEUTRAL" {
		t.Fatalf("a = %+v", a)
	}
	if p, err := st.Page(ctx, site.ID, "https://example.com/task-page"); err != nil || p.IndexVerdict != "PASS" || p.LastSeen != 0 {
		t.Fatalf("Page = %+v, %v", p, err)
	}
	if _, err := st.Page(ctx, site.ID, "https://example.com/none"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing Page: %v", err)
	}
	if n, _ := st.InspectionsSince(ctx, site.ID, 150); n != 2 {
		t.Fatalf("InspectionsSince = %d", n)
	}
	errs, total, err := st.Pages(ctx, PageQuery{SiteID: site.ID, Filter: "error"})
	ok(t, err)
	if total != 1 || errs[0].URL != "https://example.com/a" {
		t.Fatalf("error pages = %+v (%d)", errs, total)
	}
	all, total, err := st.Pages(ctx, PageQuery{SiteID: site.ID})
	ok(t, err)
	if total != 2 || all[0].URL != "https://example.com/a" { // problems first; the inspection-only row is not a crawled page
		t.Fatalf("all pages = %+v (%d)", all, total)
	}
	ni, total, err := st.Pages(ctx, PageQuery{SiteID: site.ID, Filter: "not_indexed", Contains: "/A"})
	ok(t, err)
	if total != 1 || ni[0].IndexCoverage != "Crawled - currently not indexed" {
		t.Fatalf("not indexed = %+v", ni)
	}
	if _, _, err := st.Pages(ctx, PageQuery{SiteID: site.ID, Filter: "bogus"}); !errors.Is(err, ErrBadQuery) {
		t.Fatalf("bogus filter: %v", err)
	}
	ok(t, st.PrunePages(ctx, site.ID, 160))
	if pages, _ := st.SitePages(ctx, site.ID); len(pages) != 1 {
		t.Fatalf("after prune: %+v", pages)
	}
}

func TestImpressionPages(t *testing.T) {
	st := open(t)
	site, err := st.AddSite(ctx, "example.com", 1)
	ok(t, err)
	ok(t, st.ReplaceGSC(ctx, site.ID, "page", "2026-11-01", "2026-11-02", []GSCRow{
		{Day: "2026-11-01", Page: "https://example.com/a", Impressions: 3},
		{Day: "2026-11-02", Page: "https://example.com/b", Impressions: 0},
		{Day: "2026-11-02", Page: "https://example.com/a", Impressions: 1},
	}, 1))
	got, err := st.ImpressionPages(ctx, site.ID, "2026-11-01", "2026-11-30")
	ok(t, err)
	if len(got) != 1 || !got["https://example.com/a"] {
		t.Fatalf("ImpressionPages = %v", got)
	}
}
