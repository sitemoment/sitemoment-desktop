// SPDX-License-Identifier: AGPL-3.0-or-later
package health

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/google"
	"github.com/sitemoment/sitemoment-desktop/internal/rank"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

var ctx = context.Background()

func ok(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	ok(t, err)
	t.Cleanup(func() { st.Close() })
	ok(t, st.Migrate(ctx))
	return st
}

// site is a small website whose pages the test edits between checks.
type site struct {
	mu    sync.Mutex
	pages map[string]string // path → extra <head> markup; missing = 404
	links []string
	// extraDisallow are extra robots.txt Disallow lines (beyond /private), settable between checks.
	extraDisallow []string
	// hits counts requests per path, so tests can tell whether a URL was fetched again.
	hits map[string]int
	srv  *httptest.Server
}

func newSite(t *testing.T) *site {
	s := &site{pages: map[string]string{"/": "", "/a": "", "/b": "", "/c": "", "/d": ""}, links: []string{"/a", "/b", "/c", "/d"}, hits: map[string]int{}}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.hits[r.URL.Path]++
		switch r.URL.Path {
		case "/robots.txt":
			fmt.Fprint(w, "User-agent: *\nDisallow: /private\n")
			for _, d := range s.extraDisallow {
				fmt.Fprintf(w, "Disallow: %s\n", d)
			}
			fmt.Fprintf(w, "Sitemap: %s/sitemap.xml\n", s.srv.URL)
			return
		case "/sitemap.xml":
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprintf(w, `<urlset><url><loc>%s/</loc></url><url><loc>%s/a</loc></url><url><loc>%s/b</loc></url></urlset>`, s.srv.URL, s.srv.URL, s.srv.URL)
			return
		}
		head, found := s.pages[r.URL.Path]
		w.Header().Set("Content-Type", "text/html")
		if !found {
			w.WriteHeader(404)
			fmt.Fprint(w, "<html><head><title>Not found</title></head><body>404</body></html>")
			return
		}
		var body strings.Builder
		if r.URL.Path == "/" {
			for _, l := range s.links {
				fmt.Fprintf(&body, `<a href="%s">%s</a>`, l, l)
			}
			body.WriteString(`<a href="/private">p</a>`)
		}
		fmt.Fprintf(w, `<html><head><title>Page %s about something</title>%s</head><body><h1>Heading for %s</h1>%s</body></html>`, r.URL.Path, head, r.URL.Path, body.String())
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *site) edit(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn()
}

func (s *site) hitCount(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[path]
}

// fakeInspector answers NEUTRAL for every URL in notIndexed, PASS otherwise.
type fakeInspector struct {
	mu         sync.Mutex
	asked      []string
	notIndexed map[string]bool
	err        error
}

func (f *fakeInspector) Inspect(_ context.Context, _ store.Site, u string) (google.Inspection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, u)
	if f.err != nil {
		return google.Inspection{}, f.err
	}
	if f.notIndexed[u] {
		return google.Inspection{Verdict: "NEUTRAL", CoverageState: "Crawled - currently not indexed"}, nil
	}
	return google.Inspection{Verdict: "PASS", CoverageState: "Submitted and indexed"}, nil
}

func checker(t *testing.T, st *store.Store, s *site, now *time.Time) *Checker {
	return &Checker{Store: st, Fetcher: rank.NewFetcher(rank.FetcherConfig{AllowPrivate: true}),
		Now: func() time.Time { return *now }, Start: func(string) string { return s.srv.URL + "/" }}
}

func codes(issues []store.Issue) map[string]store.Issue {
	out := map[string]store.Issue{}
	for _, is := range issues {
		out[is.Code] = is
	}
	return out
}

func TestCheckFindsRegressions(t *testing.T) {
	st := openStore(t)
	s := newSite(t)
	now := time.Date(2026, 11, 10, 8, 0, 0, 0, time.Local)
	c := checker(t, st, s, &now)
	ws, err := st.AddSite(ctx, "example.com", 1)
	ok(t, err)

	first, err := c.Check(ctx, ws, "manual", nil)
	ok(t, err)
	if first.Status != "done" || first.Pages != 5 || first.OKPages != 5 || first.Score != 100-2 { // not_in_sitemap (P2): /c and /d
		t.Fatalf("first = %+v", first)
	}
	if !strings.Contains(first.Summary, `"disallowed":1`) {
		t.Fatalf("summary = %s (robots.txt disallows /private)", first.Summary)
	}

	s.edit(func() {
		delete(s.pages, "/a")                                    // used to work, now 404 (still linked)
		s.pages["/b"] = `<meta name="robots" content="noindex">` // newly noindex
		s.pages["/c"] = `<link rel="canonical" href="https://elsewhere.example/c">`
		delete(s.pages, "/d") // no longer linked and 404: found by the recheck
		s.links = []string{"/a", "/b", "/c"}
	})
	now = now.Add(24 * time.Hour)
	second, err := c.Check(ctx, ws, "schedule", nil)
	ok(t, err)
	issues, err := st.CrawlIssues(ctx, second.ID)
	ok(t, err)
	got := codes(issues)
	if lost := got[CodeLost]; lost.Count != 2 || lost.Severity != rank.SeverityP0 || !lost.New || !strings.Contains(strings.Join(lost.URLs, " "), "/d → 404") {
		t.Fatalf("lost = %+v", lost)
	}
	if n := got[CodeNewNoindex]; n.Count != 1 || !strings.HasSuffix(n.URLs[0], "/b") {
		t.Fatalf("new noindex = %+v", n)
	}
	if c := got[CodeCanonical]; c.Count != 1 || !strings.Contains(c.URLs[0], "https://elsewhere.example/c") {
		t.Fatalf("canonical = %+v", c)
	}
	if issues[0].Severity != rank.SeverityP0 || second.P0 < 2 || second.Score >= first.Score {
		t.Fatalf("grading: %+v / %+v", second, issues)
	}
	pages, err := st.SitePages(ctx, ws.ID)
	ok(t, err)
	if d := pages[s.srv.URL+"/d"]; d.Status != 404 || d.OK || d.LastOK != first.StartedAt {
		t.Fatalf("memory of /d = %+v", d)
	}
	// The same state a day later: nothing is new any more, but lost_pages and
	// new_noindex keep being reported while the pages stay broken/noindex and
	// are still within 30 days of having last been OK; canonical only fires
	// on an actual change, so it does not fire again.
	now = now.Add(24 * time.Hour)
	third, err := c.Check(ctx, ws, "schedule", nil)
	ok(t, err)
	issues, err = st.CrawlIssues(ctx, third.ID)
	ok(t, err)
	got = codes(issues)
	for _, is := range issues {
		if is.New {
			t.Errorf("%s is new again", is.Code)
		}
	}
	if lost := got[CodeLost]; lost.Count != 2 {
		t.Fatalf("lost pages must keep being reported within 30 days of last_ok: %+v", lost)
	}
	if n := got[CodeNewNoindex]; n.Count != 1 {
		t.Fatalf("noindex must keep being reported while the page stays noindex: %+v", n)
	}
	if _, found := got[CodeCanonical]; found {
		t.Fatal("canonical did not change again: must not be reported a second time")
	}
}

// TestLostPageStopsBeingReportedAfter30Days: a page that keeps failing is
// reported every check while last_ok is within 30 days, then drops out.
func TestLostPageStopsBeingReportedAfter30Days(t *testing.T) {
	st := openStore(t)
	s := newSite(t)
	now := time.Date(2026, 11, 10, 8, 0, 0, 0, time.Local)
	c := checker(t, st, s, &now)
	ws, err := st.AddSite(ctx, "example.com", 1)
	ok(t, err)
	_, err = c.Check(ctx, ws, "manual", nil)
	ok(t, err)

	s.edit(func() { delete(s.pages, "/a") }) // /a now 404, still linked
	now = now.Add(24 * time.Hour)
	second, err := c.Check(ctx, ws, "schedule", nil)
	ok(t, err)
	issues, err := st.CrawlIssues(ctx, second.ID)
	ok(t, err)
	if is := codes(issues)[CodeLost]; is.Count == 0 {
		t.Fatalf("expected /a reported lost right away: %+v", issues)
	}

	// A few days later, still failing and well within 30 days: still reported.
	now = now.Add(5 * 24 * time.Hour)
	third, err := c.Check(ctx, ws, "schedule", nil)
	ok(t, err)
	issues, err = st.CrawlIssues(ctx, third.ID)
	ok(t, err)
	if is := codes(issues)[CodeLost]; is.Count == 0 {
		t.Fatalf("a page failing for less than 30 days must keep being reported: %+v", issues)
	}

	// Past 30 days of continuous failure: stops being reported.
	now = now.Add(26 * 24 * time.Hour)
	fourth, err := c.Check(ctx, ws, "schedule", nil)
	ok(t, err)
	issues, err = st.CrawlIssues(ctx, fourth.ID)
	ok(t, err)
	if is := codes(issues)[CodeLost]; is.Count != 0 {
		t.Fatalf("a page failing for over 30 days must stop being reported: %+v", is)
	}
}

// TestRecheckSkipsRobotsDisallowedURLs: a page that was OK and is now
// disallowed by robots.txt must not be re-fetched by the recheck (lost
// pages/unseen-OK tracing), and must not be reported lost.
func TestRecheckSkipsRobotsDisallowedURLs(t *testing.T) {
	st := openStore(t)
	s := newSite(t)
	now := time.Date(2026, 11, 10, 8, 0, 0, 0, time.Local)
	c := checker(t, st, s, &now)
	ws, err := st.AddSite(ctx, "example.com", 1)
	ok(t, err)

	first, err := c.Check(ctx, ws, "manual", nil)
	ok(t, err)
	if first.Status != "done" || first.Pages != 5 {
		t.Fatalf("first = %+v", first)
	}

	s.edit(func() { s.extraDisallow = append(s.extraDisallow, "/a") })
	now = now.Add(24 * time.Hour)
	before := s.hitCount("/a")
	second, err := c.Check(ctx, ws, "schedule", nil)
	ok(t, err)
	if after := s.hitCount("/a"); after != before {
		t.Fatalf("recheck fetched a robots-disallowed URL: hits %d → %d", before, after)
	}
	issues, err := st.CrawlIssues(ctx, second.ID)
	ok(t, err)
	if is := codes(issues)[CodeLost]; is.Count != 0 {
		t.Fatalf("a robots-disallowed URL must not be reported lost: %+v", is)
	}
}

func TestCheckUsesSearchConsoleAndInspection(t *testing.T) {
	st := openStore(t)
	s := newSite(t)
	now := time.Date(2026, 11, 10, 8, 0, 0, 0, time.Local)
	c := checker(t, st, s, &now)
	insp := &fakeInspector{notIndexed: map[string]bool{s.srv.URL + "/b": true}}
	c.Inspector = insp
	ws, err := st.AddSite(ctx, "example.com", 1)
	ok(t, err)
	ok(t, st.BindGSC(ctx, ws.ID, "me@example.com", "sc-domain:example.com"))
	ws, _ = st.Site(ctx, ws.ID)
	// 28 days to 2026-11-07 against the 28 before: clicks 100 → 40, impressions 1000 → 900.
	ok(t, st.ReplaceGSC(ctx, ws.ID, "date", "2026-09-13", "2026-11-07", []store.GSCRow{
		{Day: "2026-09-20", Clicks: 100, Impressions: 1000}, {Day: "2026-10-20", Clicks: 40, Impressions: 900}}, 1))
	ok(t, st.ReplaceGSC(ctx, ws.ID, "page", "2026-10-20", "2026-10-20", []store.GSCRow{
		{Day: "2026-10-20", Page: s.srv.URL + "/", Impressions: 900}}, 1))

	cr, err := c.Check(ctx, ws, "manual", []string{"https://example.com/watched"})
	ok(t, err)
	issues, _ := st.CrawlIssues(ctx, cr.ID)
	got := codes(issues)
	if d := got[CodeClicksDrop]; d.Severity != rank.SeverityP1 || !strings.Contains(d.Detail, "从 100 降到 40") {
		t.Fatalf("clicks drop = %+v", d)
	}
	if _, found := got[CodeImprDrop]; found {
		t.Fatal("a 10% impressions dip is not a drop")
	}
	// The watched URL first; the home page has impressions so it is not asked about.
	if len(insp.asked) != 5 || insp.asked[0] != "https://example.com/watched" {
		t.Fatalf("asked = %v", insp.asked)
	}
	for _, u := range insp.asked {
		if u == s.srv.URL+"/" {
			t.Fatal("inspected a page with impressions")
		}
	}
	if ni := got[CodeNotIndexed]; ni.Count != 1 || ni.URLs[0] != s.srv.URL+"/b（Crawled - currently not indexed）" {
		t.Fatalf("not indexed = %+v", ni)
	}
	if !strings.Contains(cr.Summary, `"inspected":5`) || !strings.Contains(cr.Summary, `"clicks":40`) {
		t.Fatalf("summary = %s", cr.Summary)
	}
	// Inspected pages are not asked again for a week; the watched one after a day.
	insp.asked = nil
	now = now.Add(25 * time.Hour)
	_, err = c.Check(ctx, ws, "schedule", []string{"https://example.com/watched"})
	ok(t, err)
	if len(insp.asked) != 1 || insp.asked[0] != "https://example.com/watched" {
		t.Fatalf("asked again = %v", insp.asked)
	}
}

func TestInspectionBudgetAndErrors(t *testing.T) {
	st := openStore(t)
	s := newSite(t)
	now := time.Date(2026, 11, 10, 8, 0, 0, 0, time.Local)
	c := checker(t, st, s, &now)
	insp := &fakeInspector{err: google.ErrRateLimited}
	c.Inspector, c.InspectPerRun = insp, 2
	ws, _ := st.AddSite(ctx, "example.com", 1)
	ok(t, st.BindGSC(ctx, ws.ID, "me@example.com", "sc-domain:example.com"))
	ws, _ = st.Site(ctx, ws.ID)
	ok(t, st.ReplaceGSC(ctx, ws.ID, "date", "2026-11-01", "2026-11-07", nil, 1))
	cr, err := c.Check(ctx, ws, "manual", nil)
	ok(t, err)
	if len(insp.asked) != 1 || !strings.Contains(cr.Summary, "限流") {
		t.Fatalf("asked %v, summary %s", insp.asked, cr.Summary)
	}
	insp.err, insp.asked = nil, nil
	now = now.Add(time.Hour)
	_, err = c.Check(ctx, ws, "manual", nil)
	ok(t, err)
	if len(insp.asked) != 2 {
		t.Fatalf("per-run cap: asked %v", insp.asked)
	}
	c.InspectPerDay = 2
	insp.asked = nil
	now = now.Add(time.Hour)
	cr, err = c.Check(ctx, ws, "manual", nil)
	ok(t, err)
	if len(insp.asked) != 0 || !strings.Contains(cr.Summary, "已用完") {
		t.Fatalf("per-day cap: asked %v, summary %s", insp.asked, cr.Summary)
	}
}

func TestCheckFailsWhenSiteIsDown(t *testing.T) {
	st := openStore(t)
	s := newSite(t)
	now := time.Date(2026, 11, 10, 8, 0, 0, 0, time.Local)
	c := checker(t, st, s, &now)
	ws, _ := st.AddSite(ctx, "example.com", 1)
	s.srv.Close()
	cr, err := c.Check(ctx, ws, "schedule", nil)
	if !errors.Is(err, ErrUnreachable) || cr.Status != "failed" || !strings.Contains(cr.Error, "网站打不开") {
		t.Fatalf("crawl = %+v, err = %v", cr, err)
	}
	if _, err := st.LatestCrawl(ctx, ws.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("a failed check counts as the latest finished one")
	}
}

func TestCheckReportsARobotsBlock(t *testing.T) {
	st := openStore(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "User-agent: *\nDisallow: /\n")
	}))
	defer srv.Close()
	now := time.Date(2026, 11, 10, 8, 0, 0, 0, time.Local)
	c := &Checker{Store: st, Fetcher: rank.NewFetcher(rank.FetcherConfig{AllowPrivate: true}), Now: func() time.Time { return now },
		Start: func(string) string { return srv.URL + "/" }}
	ws, _ := st.AddSite(ctx, "example.com", 1)
	cr, err := c.Check(ctx, ws, "manual", nil)
	ok(t, err)
	issues, _ := st.CrawlIssues(ctx, cr.ID)
	if cr.P0 != 1 || codes(issues)[CodeRobotsBlocked].Title != "robots.txt 屏蔽了整站" {
		t.Fatalf("crawl %+v issues %+v", cr, issues)
	}
}

func TestUnseenOKIsOldestFirst(t *testing.T) {
	// Deliberately the reverse of alphabetical order, so a leftover
	// sort.Strings would make this test fail.
	prev := map[string]store.Page{
		"https://e.com/a": {URL: "https://e.com/a", OK: true, LastOK: 999, LastSeen: 300},
		"https://e.com/m": {URL: "https://e.com/m", OK: true, LastOK: 10, LastSeen: 200},
		"https://e.com/z": {URL: "https://e.com/z", OK: true, LastOK: 50, LastSeen: 100},
	}
	res := &rank.CrawlResult{Host: "e.com"}
	got := unseenOK(prev, res, 10, 100000)
	want := []string{"https://e.com/z", "https://e.com/m", "https://e.com/a"}
	if len(got) != len(want) {
		t.Fatalf("unseenOK = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unseenOK = %v, want %v (oldest LastSeen first)", got, want)
		}
	}
}

func TestGrade(t *testing.T) {
	p0, p1, p2, score := Grade([]store.Issue{{Severity: "p0"}, {Severity: "p1"}, {Severity: "p1"}, {Severity: "p2"}})
	if p0 != 1 || p1 != 2 || p2 != 1 || score != 100-25-16-2 {
		t.Fatalf("Grade = %d %d %d %d", p0, p1, p2, score)
	}
	if _, _, _, s := Grade([]store.Issue{{Severity: "p0"}, {Severity: "p0"}, {Severity: "p0"}, {Severity: "p0"}, {Severity: "p0"}}); s != 0 {
		t.Fatalf("score below zero: %d", s)
	}
}
