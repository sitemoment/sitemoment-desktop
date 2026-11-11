// SPDX-License-Identifier: AGPL-3.0-or-later

package rank

import (
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func htmlResp(finalURL string, body []byte) *Response {
	h := http.Header{}
	h.Set("Content-Type", "text/html; charset=utf-8")
	return &Response{URL: finalURL, FinalURL: finalURL, StatusCode: 200, Header: h, Body: body, TimeMs: 120}
}

func goodSite() SiteInfo {
	yes, n := true, 12
	return SiteInfo{RobotsTxt: true, Sitemap: true, SitemapURLs: &n, HTTPRedirectsToHTTPS: &yes}
}

func statuses(r *Report) map[string]string {
	m := map[string]string{}
	for _, c := range r.Checks {
		m[c.ID] = c.Status
	}
	return m
}

func TestAnalyzeGolden(t *testing.T) {
	allPass := map[string]string{}
	for id := range checkWeights {
		allPass[id] = StatusPass
	}
	with := func(over map[string]string) map[string]string {
		m := map[string]string{}
		for k, v := range allPass {
			m[k] = v
		}
		for k, v := range over {
			m[k] = v
		}
		return m
	}
	cases := []struct {
		name, fixture, url string
		site               SiteInfo
		want               map[string]string
		minScore, maxScore int
	}{
		{"good", "good.html", "https://example.com/sourdough/", goodSite(), allPass, 100, 100},
		{"noindex", "noindex.html", "https://example.com/sourdough/", goodSite(),
			with(map[string]string{"indexable": StatusFail}), 85, 92},
		{"jsonld broken", "jsonld_broken.html", "https://example.com/sourdough/", goodSite(),
			with(map[string]string{"json_ld": StatusFail}), 92, 97},
		{"bad", "bad.html", "http://example.com/", SiteInfo{}, map[string]string{
			"title": StatusWarn, "description": StatusFail, "canonical": StatusFail,
			"h1": StatusWarn, "heading_order": StatusWarn, "word_count": StatusWarn, "image_alt": StatusFail,
			"internal_links": StatusFail, "redirects": StatusPass,
			"indexable": StatusPass, "lang": StatusWarn, "viewport": StatusFail, "https": StatusFail,
			"http_redirect": StatusFail, "robots_txt": StatusWarn, "sitemap": StatusWarn, "json_ld": StatusWarn,
			"og_tags": StatusFail, "twitter_card": StatusWarn, "response_time": StatusPass, "page_size": StatusPass,
		}, 25, 40},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := Analyze(c.url, htmlResp(c.url, fixture(t, c.fixture)), c.site)
			if got := statuses(r); !reflect.DeepEqual(got, c.want) {
				for id, w := range c.want {
					if got[id] != w {
						t.Errorf("%s: %s, want %s", id, got[id], w)
					}
				}
				if len(got) != len(c.want) {
					t.Errorf("got %d checks, want %d", len(got), len(c.want))
				}
			}
			if r.Score < c.minScore || r.Score > c.maxScore {
				t.Errorf("score %d not in [%d, %d]", r.Score, c.minScore, c.maxScore)
			}
			for _, ch := range r.Checks {
				if ch.Detail == "" || ch.Title == "" || strings.Count(strings.TrimSuffix(ch.Detail, "."), ". ") > 0 {
					t.Errorf("%s: detail must be one sentence: %q", ch.ID, ch.Detail)
				}
				if _, ok := checkWeights[ch.ID]; !ok {
					t.Errorf("%s has no weight", ch.ID)
				}
			}
		})
	}
}

func TestAnalyzeGoodPageFacts(t *testing.T) {
	r := Analyze("https://example.com/sourdough/", htmlResp("https://example.com/sourdough/", fixture(t, "good.html")), goodSite())
	p := r.Page
	if p.Title != "Sourdough Bread at Home: A Beginner's Complete Guide" || p.Lang != "en" || !p.Viewport {
		t.Errorf("meta: %+v", p)
	}
	if !reflect.DeepEqual(p.H1, []string{"Sourdough bread at home"}) || p.Headings != (Headings{H2: 2, H3: 1}) {
		t.Errorf("headings: %v %+v", p.H1, p.Headings)
	}
	if p.Canonical != "https://example.com/sourdough/" {
		t.Errorf("canonical %q", p.Canonical)
	}
	if p.Images != 2 || p.ImagesMissingAlt != 0 {
		t.Errorf("images %d/%d (alt=\"\" counts as present)", p.Images, p.ImagesMissingAlt)
	}
	// "/", "/recipes/", www.example.com/about are internal; twitter is external+nofollow; #top and mailto are skipped.
	if p.Links != (LinkCounts{Internal: 3, External: 1, Nofollow: 1}) {
		t.Errorf("links %+v", p.Links)
	}
	if !reflect.DeepEqual(p.JSONLDTypes, []string{"WebSite", "Article", "HowTo"}) {
		t.Errorf("json-ld types %v", p.JSONLDTypes)
	}
	if p.OG.Image == "" || p.Twitter.Card != "summary_large_image" {
		t.Errorf("social %+v %+v", p.OG, p.Twitter)
	}
	// Visible words only: script, style, noscript, template and svg text are excluded.
	if p.Words < 300 || p.Words > 400 {
		t.Errorf("words %d", p.Words)
	}
	if !r.HTTPS || r.Bytes == 0 || r.StatusCode != 200 {
		t.Errorf("report %+v", r)
	}
}

func TestAnalyzeEdgeCases(t *testing.T) {
	page := "https://example.com/a"
	cases := []struct {
		name, html string
		hdr        map[string]string
		check, st  string
	}{
		{"x-robots-tag", `<title>t</title>`, map[string]string{"X-Robots-Tag": "googlebot: noindex"}, "indexable", StatusFail},
		{"robots none", `<meta name="robots" content="none">`, nil, "indexable", StatusFail},
		{"robots nofollow only", `<meta name="ROBOTS" content="nofollow">`, nil, "indexable", StatusPass},
		{"canonical relative self", `<link rel="canonical" href="/a">`, nil, "canonical", StatusPass},
		{"canonical other page same host", `<link rel="canonical" href="https://www.example.com/b">`, nil, "canonical", StatusPass},
		{"canonical missing", `<p>x</p>`, nil, "canonical", StatusWarn},
		{"canonical other host", `<link rel="canonical" href="https://evil.example/a">`, nil, "canonical", StatusFail},
		{"heading order ok", `<h1>a</h1><h2>b</h2><h3>c</h3><h2>d</h2><h1>e</h1>`, nil, "heading_order", StatusPass},
		{"heading skip", `<h1>a</h1><h3>c</h3>`, nil, "heading_order", StatusWarn},
		{"no headings", `<p>a</p>`, nil, "heading_order", StatusWarn},
		{"one of three alt missing", `<img alt="a"><img alt=""><img>`, nil, "image_alt", StatusWarn},
		{"no images", `<p>a</p>`, nil, "image_alt", StatusPass},
		{"two internal", `<a href="/x">x</a><a href="y">y</a>`, nil, "internal_links", StatusWarn},
		{"og partial", `<meta property="og:title" content="t">`, nil, "og_tags", StatusWarn},
		{"ld no blocks", `<p>x</p>`, nil, "json_ld", StatusWarn},
		{"ld array", `<script type="application/ld+json">[{"@type":"Product"}]</script>`, nil, "json_ld", StatusPass},
		{"title too long", `<title>` + strings.Repeat("x", 61) + `</title>`, nil, "title", StatusWarn},
		{"svg title ignored", `<svg><title>an icon</title></svg>`, nil, "title", StatusFail},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := htmlResp(page, []byte(c.html))
			for k, v := range c.hdr {
				resp.Header.Set(k, v)
			}
			if got := statuses(Analyze(page, resp, SiteInfo{}))[c.check]; got != c.st {
				t.Errorf("%s = %s, want %s", c.check, got, c.st)
			}
		})
	}
}

func TestAnalyzeSiteAndPerformanceChecks(t *testing.T) {
	page := "https://example.com/"
	yes, no := true, false
	cases := []struct {
		name      string
		site      SiteInfo
		timeMs    int64
		size      int
		redirects int
		want      map[string]string
	}{
		{"robots blocks", SiteInfo{RobotsTxt: true, RobotsBlocksRoot: true}, 100, 10, 0,
			map[string]string{"robots_txt": StatusFail, "sitemap": StatusWarn, "http_redirect": StatusWarn}},
		{"no http redirect", SiteInfo{HTTPRedirectsToHTTPS: &no}, 1600, 10, 1,
			map[string]string{"http_redirect": StatusFail, "response_time": StatusWarn, "redirects": StatusPass}},
		{"slow and big", SiteInfo{HTTPRedirectsToHTTPS: &yes}, 3000, 2 << 20, 2,
			map[string]string{"http_redirect": StatusPass, "response_time": StatusFail, "page_size": StatusFail, "redirects": StatusWarn}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := htmlResp(page, []byte(strings.Repeat(" ", c.size)))
			resp.TimeMs, resp.Redirects = c.timeMs, c.redirects
			got := statuses(Analyze(page, resp, c.site))
			for id, w := range c.want {
				if got[id] != w {
					t.Errorf("%s = %s, want %s", id, got[id], w)
				}
			}
		})
	}
}

func TestReportJSONShape(t *testing.T) {
	r := Analyze("https://example.com/", htmlResp("https://example.com/", []byte("<p>x</p>")), SiteInfo{})
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	for _, k := range []string{"id", "url", "finalUrl", "fetchedAt", "statusCode", "timeMs", "bytes", "https", "score", "checks", "page", "site"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing %q", k)
		}
	}
	page := m["page"].(map[string]any)
	for _, k := range []string{"title", "description", "h1", "headings", "words", "canonical", "lang", "robots", "viewport",
		"images", "imagesMissingAlt", "links", "jsonLdTypes", "og", "twitter"} {
		if _, ok := page[k]; !ok {
			t.Errorf("missing page.%q", k)
		}
	}
	if page["h1"] == nil || page["jsonLdTypes"] == nil {
		t.Error("h1 and jsonLdTypes must be arrays, not null")
	}
	site := m["site"].(map[string]any)
	if v, ok := site["sitemapUrls"]; !ok || v != nil {
		t.Errorf("site.sitemapUrls = %v, want null", v)
	}
}

func TestScoreWeights(t *testing.T) {
	checks := []Check{{ID: "indexable", Status: StatusPass}, {ID: "https", Status: StatusWarn}, {ID: "title", Status: StatusFail}}
	// (5 + 4/2 + 0) / 12 = 58.3
	if got := score(checks); got != 58 {
		t.Fatalf("score %d", got)
	}
	if score(nil) != 0 {
		t.Fatal("empty score")
	}
}

func TestCountWords(t *testing.T) {
	cases := map[string]int{"": 0, "Hello, world!": 2, "don't stop": 2, "a-b c_d": 4, "日本語": 3, "SEO 101 tips": 3}
	for in, want := range cases {
		if got := countWords(in); got != want {
			t.Errorf("countWords(%q) = %d, want %d", in, got, want)
		}
	}
}
