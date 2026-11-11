// SPDX-License-Identifier: AGPL-3.0-or-later

package rank

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// siteServer serves the good fixture with robots.txt (naming a sitemap
// index), the index and a child sitemap.
func siteServer(t *testing.T, page []byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/sourdough/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(page)
	})
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("User-agent: *\nDisallow: /admin\n\nSitemap: " + srv.URL + "/sitemap_index.xml\n"))
	})
	mux.HandleFunc("/sitemap_index.xml", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<?xml version="1.0"?><sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
<sitemap><loc>` + srv.URL + `/posts.xml</loc></sitemap></sitemapindex>`))
	})
	mux.HandleFunc("/posts.xml", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
<url><loc>` + srv.URL + `/a</loc></url><url><loc>` + srv.URL + `/b</loc></url><url><loc>https://other.example/c</loc></url></urlset>`))
	})
	mux.HandleFunc("/feed.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	mux.HandleFunc("/gone", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "gone", http.StatusGone) })
	srv = httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestAuditEndToEnd(t *testing.T) {
	tlsSrv := siteServer(t, fixture(t, "good.html"))
	// A plain-HTTP front door that upgrades to the HTTPS site, as http://site/ → https://site/ would.
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, tlsSrv.URL+r.URL.Path, http.StatusMovedPermanently)
	}))
	defer plain.Close()
	f := testFetcher(t, FetcherConfig{}, tlsSrv)

	r, err := Audit(context.Background(), f, plain.URL+"/sourdough/")
	if err != nil {
		t.Fatal(err)
	}
	if r.URL != plain.URL+"/sourdough/" || r.FinalURL != tlsSrv.URL+"/sourdough/" || !r.HTTPS || r.StatusCode != 200 {
		t.Fatalf("report urls: %+v", r)
	}
	if !r.Site.RobotsTxt || !r.Site.Sitemap || r.Site.SitemapURLs == nil || *r.Site.SitemapURLs != 2 {
		t.Fatalf("site %+v", r.Site)
	}
	st := statuses(r)
	for _, id := range []string{"https", "http_redirect", "robots_txt", "sitemap", "redirects", "title", "json_ld"} {
		if st[id] != StatusPass {
			t.Errorf("%s = %s", id, st[id])
		}
	}
	// The fixture's canonical names example.com, not the test host.
	if st["canonical"] != StatusFail {
		t.Errorf("canonical = %s", st["canonical"])
	}
	if r.Score < 85 {
		t.Errorf("score %d", r.Score)
	}

	// Directly on HTTPS with a non-default port the http:// variant can't be derived: unknown.
	r, err = Audit(context.Background(), f, tlsSrv.URL+"/sourdough/")
	if err != nil {
		t.Fatal(err)
	}
	if st := statuses(r); st["http_redirect"] != StatusWarn {
		t.Errorf("http_redirect = %s", st["http_redirect"])
	}
}

func TestAuditRobotsBlockAndMissingSitemap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			_, _ = w.Write([]byte("User-agent: Googlebot\nAllow: /\n\nUser-agent: *\nDisallow: /\n"))
		case "/":
			w.Header().Set("Content-Type", "application/xhtml+xml")
			_, _ = w.Write([]byte(`<html><head><title>x</title></head><body><p>hi</p></body></html>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	r, err := Audit(context.Background(), testFetcher(t, FetcherConfig{}), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	st := statuses(r)
	if st["robots_txt"] != StatusFail || st["sitemap"] != StatusWarn || st["https"] != StatusFail || st["http_redirect"] != StatusFail {
		t.Fatalf("%v", st)
	}
	if r.Site.SitemapURLs != nil {
		t.Fatalf("sitemapUrls %v", *r.Site.SitemapURLs)
	}
}

func TestAuditRefusals(t *testing.T) {
	srv := siteServer(t, fixture(t, "good.html"))
	f := testFetcher(t, FetcherConfig{}, srv)
	cases := []struct{ url, reason string }{
		{srv.URL + "/feed.json", "not_html"},
		{srv.URL + "/gone", "status"},
		{srv.URL + "/missing", "status"},
		{"ftp://example.com/", "bad_url"},
	}
	for _, c := range cases {
		_, err := Audit(context.Background(), f, c.url)
		if err == nil {
			t.Errorf("%s: no error", c.url)
			continue
		}
		if r := reasonOf(t, err); r != c.reason {
			t.Errorf("%s: %q, want %q", c.url, r, c.reason)
		}
	}
	// The production guard refuses the audit before fetching.
	if _, err := Audit(context.Background(), guardedFetcher(), srv.URL+"/sourdough/"); reasonOf(t, err) != "blocked_address" {
		t.Errorf("guarded audit: %v", err)
	}
}

func TestIsHTML(t *testing.T) {
	cases := []struct {
		ct, body string
		want     bool
	}{
		{"text/html", "", true},
		{"TEXT/HTML; charset=utf-8", "", true},
		{"application/xhtml+xml", "", true},
		{"text/plain", "<html>", false},
		{"application/json", "{}", false},
		{"", "<!doctype html><html><body>x", true},
		{"", "plain words", false},
		{"text/html;;;", "", false},
	}
	for _, c := range cases {
		h := http.Header{}
		if c.ct != "" {
			h.Set("Content-Type", c.ct)
		}
		if got := isHTML(&Response{Header: h, Body: []byte(c.body)}); got != c.want {
			t.Errorf("isHTML(%q, %q) = %v", c.ct, c.body, got)
		}
	}
}

func TestAuditIsConcurrencySafe(t *testing.T) {
	srv := siteServer(t, fixture(t, "good.html"))
	f := testFetcher(t, FetcherConfig{}, srv)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			_, err := Audit(context.Background(), f, srv.URL+"/sourdough/")
			errs <- err
		}()
	}
	for i := 0; i < 8; i++ {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
}
