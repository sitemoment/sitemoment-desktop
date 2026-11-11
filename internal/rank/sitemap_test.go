// SPDX-License-Identifier: AGPL-3.0-or-later

package rank

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestReadSitemap(t *testing.T) {
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/index.xml", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<sitemapindex><sitemap><loc>` + srv.URL + `/one.xml</loc></sitemap>
<sitemap><loc>https://other.example/evil.xml</loc></sitemap>
<sitemap><loc>` + srv.URL + `/broken.xml</loc></sitemap>
<sitemap><loc>` + srv.URL + `/nested.xml</loc></sitemap>
<sitemap><loc>` + srv.URL + `/two.xml.gz</loc></sitemap></sitemapindex>`))
	})
	mux.HandleFunc("/one.xml", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
<url><loc> ` + srv.URL + `/a </loc><lastmod>2026-01-01</lastmod></url>
<url><loc>` + srv.URL + `/a</loc></url>
<url><loc>https://other.example/x</loc></url>
<url><loc>` + srv.URL + `/b</loc></url></urlset>`))
	})
	mux.HandleFunc("/broken.xml", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	mux.HandleFunc("/nested.xml", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<sitemapindex><sitemap><loc>` + srv.URL + `/deep.xml</loc></sitemap></sitemapindex>`))
	})
	mux.HandleFunc("/deep.xml", func(w http.ResponseWriter, r *http.Request) {
		t.Error("second index level was followed")
	})
	mux.HandleFunc("/two.xml.gz", func(w http.ResponseWriter, r *http.Request) {
		var b bytes.Buffer
		zw := gzip.NewWriter(&b)
		_, _ = zw.Write([]byte(`<urlset><url><loc>` + srv.URL + `/c</loc></url></urlset>`))
		_ = zw.Close()
		_, _ = w.Write(b.Bytes())
	})
	mux.HandleFunc("/page.html", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("<html></html>")) })
	srv = httptest.NewServer(mux)
	defer srv.Close()
	f := testFetcher(t, FetcherConfig{})
	ctx := context.Background()

	got, err := ReadSitemap(ctx, f, srv.URL+"/index.xml", 100)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{srv.URL + "/a", srv.URL + "/b", srv.URL + "/c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got, _ := ReadSitemap(ctx, f, srv.URL+"/index.xml", 1); len(got) != 1 {
		t.Fatalf("limit: %v", got)
	}
	if got, err := ReadSitemap(ctx, f, srv.URL+"/index.xml", 0); err != nil || len(got) != 0 {
		t.Fatalf("limit 0: %v %v", got, err)
	}
	if _, err := ReadSitemap(ctx, f, srv.URL+"/broken.xml", 10); reasonOf(t, err) != "status" {
		t.Fatalf("404: %v", err)
	}
	if _, err := ReadSitemap(ctx, f, srv.URL+"/page.html", 10); err == nil {
		t.Fatal("html accepted as a sitemap")
	}
	if _, err := ReadSitemap(ctx, guardedFetcher(), srv.URL+"/one.xml", 10); reasonOf(t, err) != "blocked_address" {
		t.Fatalf("guarded: %v", err)
	}
}

func TestParseRobots(t *testing.T) {
	cases := []struct {
		name, body string
		blocks     bool
		sitemaps   []string
	}{
		{"empty", "", false, nil},
		{"allow all", "User-agent: *\nDisallow:\n", false, nil},
		{"block all", "User-agent: *\nDisallow: /\n", true, nil},
		{"block all wildcard, comments", "# hi\nUser-Agent: * # all\nDisallow: /* \nSitemap: https://e.com/s.xml\n", true, []string{"https://e.com/s.xml"}},
		{"block other bot only", "User-agent: BadBot\nDisallow: /\n\nUser-agent: *\nDisallow: /tmp\n", false, nil},
		{"shared group", "User-agent: BadBot\nUser-agent: *\nDisallow: /\n", true, nil},
		{"allow root overrides", "User-agent: *\nDisallow: /\nAllow: /\n", false, nil},
		{"partial", "User-agent: *\nDisallow: /admin\n", false, nil},
		{"group ends", "User-agent: *\nDisallow: /x\nUser-agent: other\nDisallow: /\n", false, nil},
	}
	for _, c := range cases {
		got := parseRobots([]byte(c.body))
		if got.blocksRoot != c.blocks || !reflect.DeepEqual(got.sitemaps, c.sitemaps) {
			t.Errorf("%s: %+v", c.name, got)
		}
	}
}

func TestParseSitemapRejectsOtherXML(t *testing.T) {
	for _, s := range []string{"", "not xml", "<rss></rss>", "<html><body>x</body></html>"} {
		if _, err := parseSitemap([]byte(s)); err == nil {
			t.Errorf("%q parsed", s)
		}
	}
	sm, err := parseSitemap([]byte(strings.Repeat(" ", 3) + `<urlset><url><loc>https://e.com/</loc></url></urlset>`))
	if err != nil || len(sm.urls) != 1 {
		t.Fatalf("%+v %v", sm, err)
	}
}
