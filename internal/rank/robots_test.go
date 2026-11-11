// SPDX-License-Identifier: AGPL-3.0-or-later

package rank

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestRobotsStripsBOM(t *testing.T) {
	body := append([]byte("\xEF\xBB\xBF"), []byte("User-agent: *\nDisallow: /private\n")...)
	ri := parseRobots(body)
	if ri.allowed("x", "https://e.com/private") {
		t.Fatal("a leading UTF-8 BOM must not disable the first robots.txt group")
	}
	if !ri.allowed("x", "https://e.com/ok") {
		t.Fatal("an unrelated path should still be allowed")
	}
}

func TestRobotsNonASCIIAndPercentCase(t *testing.T) {
	ri := parseRobots([]byte("User-agent: *\nDisallow: /café\nDisallow: /brace%7bb\n"))
	if ri.allowed("x", "https://e.com/café") {
		t.Fatal("a non-ASCII Disallow rule must match the percent-encoded path (RFC 9309 §2.2.2)")
	}
	if ri.allowed("x", "https://e.com/brace{b") {
		t.Fatal("a lower-case percent-escape in robots.txt must still match an upper-case-escaped path")
	}
	if !ri.allowed("x", "https://e.com/unrelated") {
		t.Fatal("unrelated paths must stay allowed")
	}
}

func TestRobotsAllowed(t *testing.T) {
	ri := parseRobots([]byte(`User-agent: *
Disallow: /private
Disallow: /*.pdf$
Allow: /private/ok

User-agent: OtherBot
Disallow: /

User-agent: SiteMomentBot
User-agent: Googlebot
Disallow: /drafts/
Disallow: /search?
`))
	cases := []struct {
		agent, url string
		want       bool
	}{
		{"sitemomentbot", "https://e.com/", true},
		{"sitemomentbot", "https://e.com/drafts/a", false},
		{"sitemomentbot", "https://e.com/search?q=x", false},
		{"sitemomentbot", "https://e.com/search", true},
		// Our own group replaces "*": /private is not blocked for us.
		{"sitemomentbot", "https://e.com/private", true},
		{"anybot", "https://e.com/private/x", false},
		{"anybot", "https://e.com/private/ok/x", true}, // longer Allow wins
		{"anybot", "https://e.com/files/a.pdf", false},
		{"anybot", "https://e.com/files/a.pdf?x=1", true}, // "$" anchors the end
		{"otherbot", "https://e.com/anything", false},
		{"otherbot", "https://e.com/robots.txt", true},
	}
	for _, c := range cases {
		if got := ri.allowed(c.agent, c.url); got != c.want {
			t.Errorf("%s %s = %v, want %v", c.agent, c.url, got, c.want)
		}
	}
	tie := parseRobots([]byte("User-agent: *\nDisallow: /a\nAllow: /a\n"))
	if !tie.allowed("x", "https://e.com/a") {
		t.Error("Allow must win a tie")
	}
	if none := parseRobots(nil); !none.allowed("x", "https://e.com/a") {
		t.Error("an empty robots.txt allows everything")
	}
}

func TestCrawlSkipsDisallowedURLs(t *testing.T) {
	var mu sync.Mutex
	hits := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
		mu.Unlock()
		switch r.URL.Path {
		case "/robots.txt":
			fmt.Fprint(w, "User-agent: *\nDisallow: /admin\n\nUser-agent: SiteMomentBot\nDisallow: /cart\n")
		case "/":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><head><title>Home</title></head><body><a href="/cart">Cart</a><a href="/admin">Admin</a><a href="/about">About</a></body></html>`)
		default:
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><head><title>Page</title></head><body>x</body></html>`)
		}
	}))
	defer srv.Close()
	res, err := Crawl(context.Background(), testFetcher(t, FetcherConfig{}), srv.URL+"/", CrawlOptions{Delay: -1})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	cartHits := hits["/cart"]
	mu.Unlock()
	if cartHits != 0 || pageOf(res, "/cart") != nil {
		t.Fatalf("crawled /cart, which robots.txt disallows for SiteMomentBot")
	}
	// The SiteMomentBot group replaces "*", so /admin is allowed for us.
	if pageOf(res, "/admin") == nil || pageOf(res, "/about") == nil {
		t.Fatalf("pages = %+v", res.Pages)
	}
	if res.Disallowed != 1 {
		t.Fatalf("Disallowed = %d, want 1", res.Disallowed)
	}
	if res.Allowed(srv.URL + "/cart") {
		t.Fatal("Allowed should say /cart is disallowed for us")
	}
	if !res.Allowed(srv.URL + "/admin") {
		t.Fatal("Allowed should say /admin is allowed for us")
	}
}

func TestCrawlStopsWhenRobotsTxtFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head><title>Home</title></head><body>x</body></html>`)
	}))
	defer srv.Close()
	res, err := Crawl(context.Background(), testFetcher(t, FetcherConfig{}), srv.URL+"/", CrawlOptions{Delay: -1})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Pages) != 0 || res.RobotsStatus != 503 || res.Disallowed != 1 {
		t.Fatalf("result = %+v", res)
	}
	if is := issueByID(SiteIssues(res), "robots_server_error"); is == nil || is.Severity != SeverityP0 {
		t.Fatalf("issues = %+v", SiteIssues(res))
	}
	if res.Allowed(srv.URL + "/") {
		t.Fatal("Allowed should say nothing is allowed when robots.txt is a 5xx")
	}
	srv.Close()
	res, err = Crawl(context.Background(), testFetcher(t, FetcherConfig{}), srv.URL+"/", CrawlOptions{Delay: -1})
	if err != nil || res.RobotsError == "" || len(res.Pages) != 0 {
		t.Fatalf("down site = %+v, %v", res, err)
	}
}
