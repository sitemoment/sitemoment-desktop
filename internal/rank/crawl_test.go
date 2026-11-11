// SPDX-License-Identifier: AGPL-3.0-or-later

package rank

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeSite mimics heartie.net after its redesign (2026-11-06 share): old URLs
// 404 without redirects, the home page and a feature page both lead with
// "AI character chat", and /role/ pages are linked but not in the sitemap.
func fakeSite(t *testing.T) (*httptest.Server, *atomic.Int64) {
	hits := &atomic.Int64{}
	var srv *httptest.Server
	page := func(title, h1, extraHead, body string) string {
		return fmt.Sprintf(`<!doctype html><html lang="en"><head><title>%s</title>%s</head><body><h1>%s</h1>%s</body></html>`, title, extraHead, h1, body)
	}
	pages := map[string]string{
		"/": page("Heartie: AI Character Chat With Characters Who Remember You", "AI character chat with someone who remembers you", "",
			`<a href="/features/ai-character-chat">Chat</a><a href="/pricing#plans">Pricing</a><a href="/role/luna">Luna</a>
			 <a href="/role/kai">Kai</a><a href="/broken">Old</a><a href="/logo.png">logo</a><a href="https://other.example/">x</a>`),
		"/features/ai-character-chat":           page("AI Character Chat That Plays the Scene With You — Heartie", "AI character chat that plays the scene with you", "", `<a href="/">Home</a>`),
		"/pricing":                              page("Pricing — Heartie", "Pricing", "", `<a href="/">Home</a>`),
		"/privacy":                              page("Privacy — Heartie", "Privacy", "", ""),
		"/role/luna":                            page("Luna — Heartie", "Meet your character", "", ""),
		"/role/kai":                             page("Kai — Heartie", "Meet your character", "", ""),
		"/blog/ai-companion-apps-compared-2026": page("AI Companion Apps Compared (2026) — Heartie", "AI companion apps compared", "", ""),
		"/secret":                               page("Secret — Heartie", "Secret", `<meta name="robots" content="noindex">`, ""),
	}
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/robots.txt":
			fmt.Fprintf(w, "User-agent: *\nAllow: /\nSitemap: %s/sitemap.xml\n", srv.URL)
			return
		case "/sitemap.xml":
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprint(w, `<?xml version="1.0"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`)
			for _, p := range []string{"/", "/features/ai-character-chat", "/pricing", "/blog/ai-companion-apps-compared-2026", "/old-in-sitemap", "/moved", "/secret"} {
				fmt.Fprintf(w, "<url><loc>%s%s</loc></url>", srv.URL, p)
			}
			fmt.Fprint(w, `</urlset>`)
			return
		case "/moved":
			http.Redirect(w, r, "/pricing", http.StatusMovedPermanently)
			return
		case "/privacy/":
			http.Redirect(w, r, "/privacy", http.StatusMovedPermanently)
			return
		case "/download/":
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		body, ok := pages[r.URL.Path]
		if !ok {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(404)
			fmt.Fprint(w, `<html><head><title>Not found</title><meta name="robots" content="noindex"></head><body>404</body></html>`)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, hits
}

func crawlFake(t *testing.T, opt CrawlOptions) (*httptest.Server, *CrawlResult) {
	t.Helper()
	srv, _ := fakeSite(t)
	opt.Delay = -1
	res, err := Crawl(context.Background(), testFetcher(t, FetcherConfig{}), srv.URL+"/", opt)
	if err != nil {
		t.Fatal(err)
	}
	return srv, res
}

func pageOf(res *CrawlResult, path string) *CrawledPage {
	for i := range res.Pages {
		if strings.HasSuffix(res.Pages[i].URL, path) {
			return &res.Pages[i]
		}
	}
	return nil
}

func TestCrawlFollowsLinksAndSitemap(t *testing.T) {
	srv, res := crawlFake(t, CrawlOptions{})
	if !res.RobotsTxt || len(res.Sitemaps) != 1 || len(res.SitemapURLs) != 7 || res.Truncated {
		t.Fatalf("result = %+v", res)
	}
	for _, p := range []string{"/", "/features/ai-character-chat", "/pricing", "/role/luna", "/role/kai", "/broken", "/old-in-sitemap", "/moved", "/secret"} {
		if pageOf(res, p) == nil {
			t.Errorf("%s not crawled", p)
		}
	}
	if pageOf(res, "/logo.png") != nil {
		t.Error("asset crawled")
	}
	for _, p := range res.Pages {
		if !strings.HasPrefix(p.URL, srv.URL) || strings.Contains(p.URL, "#") {
			t.Errorf("off-site or fragment URL crawled: %s", p.URL)
		}
	}
	luna := pageOf(res, "/role/luna")
	if luna.InSitemap || luna.LinkedFrom != srv.URL+"/" || luna.Depth != 1 || !luna.Indexable() {
		t.Fatalf("luna = %+v", luna)
	}
	if s := pageOf(res, "/secret"); !s.Noindex || !s.InSitemap {
		t.Fatalf("secret = %+v", s)
	}
	if m := pageOf(res, "/moved"); m.Redirects != 1 || !strings.HasSuffix(m.FinalURL, "/pricing") {
		t.Fatalf("moved = %+v", m)
	}
	// The sitemap queued the feature page before the home page's link reached it; the linker is still recorded.
	if f := pageOf(res, "/features/ai-character-chat"); f.LinkedFrom != srv.URL+"/" {
		t.Fatalf("feature linkedFrom = %q", f.LinkedFrom)
	}
}

func TestCrawlStopsAtMaxPages(t *testing.T) {
	_, res := crawlFake(t, CrawlOptions{MaxPages: 3})
	if len(res.Pages) != 3 || !res.Truncated {
		t.Fatalf("pages = %d, truncated = %v", len(res.Pages), res.Truncated)
	}
}

func issueByID(issues []Issue, id string) *Issue {
	for i := range issues {
		if issues[i].ID == id {
			return &issues[i]
		}
	}
	return nil
}

func TestSiteIssuesFindTheRedesignProblems(t *testing.T) {
	_, res := crawlFake(t, CrawlOptions{})
	issues := SiteIssues(res)
	if issues[0].Severity != SeverityP0 {
		t.Fatalf("first issue = %+v", issues[0])
	}
	want := map[string]int{
		"sitemap_broken":        1, // /old-in-sitemap
		"broken_internal_links": 1, // /broken
		"sitemap_redirect":      1, // /moved
		"sitemap_noindex":       1, // /secret
		"duplicate_h1":          1, // the two role pages
		"cannibalization":       1, // home vs feature page
		"not_in_sitemap":        2, // the role pages
	}
	for id, n := range want {
		is := issueByID(issues, id)
		if is == nil || is.Count != n {
			t.Errorf("%s = %+v, want count %d", id, is, n)
		}
	}
	c := issueByID(issues, "cannibalization")
	if c == nil || c.Groups[0].Value != "ai character chat" || len(c.Groups[0].URLs) != 2 {
		t.Fatalf("cannibalization = %+v", c)
	}
	n := issueByID(issues, "not_in_sitemap")
	if n == nil || !strings.Contains(n.Detail, "/role/ (2)") {
		t.Fatalf("not_in_sitemap = %+v", n)
	}
	for _, id := range []string{"no_sitemap", "robots_blocks_site", "duplicate_title", "missing_title"} {
		if issueByID(issues, id) != nil {
			t.Errorf("unexpected %s", id)
		}
	}
}

func TestSeriesAndArticleOverlapAreNotKeyPageClashes(t *testing.T) {
	page := func(u, title string) CrawledPage {
		return CrawledPage{URL: u, FinalURL: u, Status: 200, HTML: true, Title: title, H1: []string{}, Depth: 1}
	}
	res := &CrawlResult{Host: "heartie.test", Sitemaps: []string{"s"}, Pages: []CrawledPage{
		page("https://heartie.test/blog/use-memory", "How to Use Memory in AI Chat"),
		page("https://heartie.test/blog/use-voice", "How to Use Voice Messages in AI Chat"),
		page("https://heartie.test/blog/voice-apps", "Best AI Chat Apps With Voice"),
		page("https://heartie.test/blog/anime", "Best AI Chat Apps for Anime Fans"),
		page("https://heartie.test/", "Heartie: AI Character Chat With Characters Who Remember You"),
		page("https://heartie.test/features/chat", "AI Character Chat That Plays the Scene — Heartie"),
		page("https://heartie.test/characters/amy", "Amy — Heartie"),
		page("https://heartie.test/es/characters/amy", "Amy — Heartie"),
		page("https://heartie.test/zh-hant/characters/amy", "Amy — Heartie"),
		page("https://heartie.test/blog/type/compare", "Comparisons — Heartie Blog"),
		page("https://heartie.test/blog/type/compare/2", "Comparisons — Heartie Blog"),
		page("https://heartie.test/chat/1", "Heartie"),
		page("https://heartie.test/role/1", "Heartie"),
	}}
	issues := SiteIssues(res)
	c := issueByID(issues, "cannibalization")
	if c == nil || len(c.Groups) != 1 || c.Groups[0].Value != "ai character chat" {
		t.Fatalf("cannibalization = %+v", c)
	}
	d := issueByID(issues, "duplicate_title")
	if d == nil || len(d.Groups) != 1 || d.Groups[0].Value != "Heartie" {
		t.Fatalf("duplicate_title = %+v (translations and pagination must not count)", d)
	}
	if n := issueByID(issues, "not_in_sitemap"); n == nil || containsString(n.URLs, "https://heartie.test/blog/type/compare/2") ||
		!containsString(n.URLs, "https://heartie.test/chat/1") {
		t.Fatalf("not_in_sitemap = %+v (page 2 of a list must be skipped, /chat/1 kept)", n)
	}
	o := issueByID(issues, "overlapping_posts")
	if o == nil || len(o.Groups) != 1 || o.Groups[0].Value != "best ai chat apps" || o.Severity != SeverityP2 {
		t.Fatalf("overlapping_posts = %+v", o)
	}
}

func TestVariantKey(t *testing.T) {
	cases := map[string]string{
		"https://x.test/es/characters/amy":      "characters/amy",
		"https://x.test/zh-hant/characters/amy": "characters/amy",
		"https://x.test/blog/page/3":            "blog",
		"https://x.test/blog/2":                 "blog",
		"https://x.test/chat/1":                 "chat",
		"https://x.test/api/v1":                 "api/v1",
		"https://x.test/features/x":             "features/x",
	}
	for in, want := range cases {
		if got := variantKey(in); got != want {
			t.Errorf("%s = %q, want %q", in, got, want)
		}
	}
}

func TestSharedLeadDropsConnectors(t *testing.T) {
	if got := sharedLead(words("Character AI vs Talkie"), words("Character AI vs Chai")); len(got) != 2 {
		t.Fatalf("got %v", got)
	}
	if got := sharedLead(words("Best AI companion apps for teens"), words("Best AI companion apps for seniors")); strings.Join(got, " ") != "best ai companion apps" {
		t.Fatalf("got %v", got)
	}
}

func TestCheckURLsFindsLostPagesAndSuggestsTargets(t *testing.T) {
	srv, res := crawlFake(t, CrawlOptions{})
	f := testFetcher(t, FetcherConfig{})
	old := []string{srv.URL + "/faq/", srv.URL + "/blog/best-ai-companion-apps-2026/", srv.URL + "/download/", srv.URL + "/privacy/", srv.URL + "/pricing"}
	got := CheckURLs(context.Background(), f, old, res.Pages, 2)
	byPath := map[string]URLCheck{}
	for _, c := range got {
		byPath[strings.TrimPrefix(c.URL, srv.URL)] = c
	}
	if c := byPath["/faq/"]; !c.Lost || c.FinalStatus != 404 || c.Redirected {
		t.Errorf("faq = %+v", c)
	}
	if c := byPath["/blog/best-ai-companion-apps-2026/"]; !c.Lost || !strings.HasSuffix(c.Suggest, "/blog/ai-companion-apps-compared-2026") {
		t.Errorf("blog = %+v", c)
	}
	if c := byPath["/download/"]; !c.ToHome || c.Permanent || c.Lost {
		t.Errorf("download = %+v", c)
	}
	if c := byPath["/privacy/"]; c.Lost || !c.Permanent || !strings.HasSuffix(c.FinalURL, "/privacy") || c.FinalStatus != 200 {
		t.Errorf("privacy = %+v", c)
	}
	if c := byPath["/pricing"]; c.Lost || c.Redirected || c.Suggest != "" {
		t.Errorf("pricing = %+v", c)
	}
}

func TestTraceStopsAfterMaxRedirects(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, r.URL.Path+"x", http.StatusFound)
	}))
	defer srv.Close()
	tr, err := testFetcher(t, FetcherConfig{MaxRedirects: 2}).Trace(context.Background(), srv.URL+"/a")
	if err != nil || tr.Error != "redirects" || len(tr.Hops) != 3 {
		t.Fatalf("trace = %+v, %v", tr, err)
	}
}

func TestTraceRefusesPrivateRedirectTarget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data", http.StatusFound)
	}))
	defer srv.Close()
	f := guardedFetcher()
	f.blocked = func(a netip.Addr) bool { return a.String() == "169.254.169.254" }
	tr, err := f.Trace(context.Background(), srv.URL+"/")
	if err != nil || tr.Error == "" || len(tr.Hops) != 1 {
		t.Fatalf("trace = %+v, %v", tr, err)
	}
}

func TestPageTextKeepsHeadingsAndDropsScripts(t *testing.T) {
	got := pageText([]byte(`<html><head><title>T</title><script>var x=1</script></head><body>
		<nav><a href="/">Home</a></nav><h1>AI character chat</h1><p>Characters who <b>remember</b> you.</p>
		<script>track()</script><svg><text>icon</text></svg><ul><li>One</li><li>Two</li></ul></body></html>`))
	want := "Home\n# AI character chat\nCharacters who remember you.\nOne\nTwo"
	if got != want {
		t.Fatalf("got %q", got)
	}
}

func TestFetchPageReturnsReportAndText(t *testing.T) {
	srv, _ := fakeSite(t)
	r, text, err := FetchPage(context.Background(), testFetcher(t, FetcherConfig{}), srv.URL+"/features/ai-character-chat")
	if err != nil || r.Page.Title == "" || !strings.HasPrefix(text, "# AI character chat that plays the scene with you") {
		t.Fatalf("report = %+v, text = %q, err = %v", r, text, err)
	}
	if _, _, err := FetchPage(context.Background(), testFetcher(t, FetcherConfig{}), srv.URL+"/faq/"); reasonOf(t, err) != "status" {
		t.Fatalf("404 err = %v", err)
	}
}
