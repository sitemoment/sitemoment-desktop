// SPDX-License-Identifier: AGPL-3.0-or-later

package rank

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// testFetcher talks to httptest servers: no address or port guard, and it
// trusts the certificates of the given TLS servers.
func testFetcher(t *testing.T, cfg FetcherConfig, tlsServers ...*httptest.Server) *Fetcher {
	t.Helper()
	cfg.AllowPrivate = true
	f := NewFetcher(cfg)
	for _, s := range tlsServers {
		f.transport.TLSClientConfig.RootCAs = s.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	}
	return f
}

// guardedFetcher is a production fetcher whose port rule is lifted, so that
// the address guard is what refuses a 127.0.0.1 httptest server.
func guardedFetcher() *Fetcher {
	f := NewFetcher(DefaultFetcherConfig())
	f.anyPort = true
	return f
}

func reasonOf(t *testing.T, err error) string {
	t.Helper()
	var fe *FetchError
	if !errors.As(err, &fe) {
		t.Fatalf("want *FetchError, got %T %v", err, err)
	}
	return fe.Reason
}

func fakeLookup(m map[string][]string) func(context.Context, string) ([]netip.Addr, error) {
	return func(_ context.Context, host string) ([]netip.Addr, error) {
		ips, ok := m[host]
		if !ok {
			return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
		}
		var out []netip.Addr
		for _, s := range ips {
			out = append(out, netip.MustParseAddr(s))
		}
		return out, nil
	}
}

func TestGuardBlocksLoopbackServer(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()

	// The real production config: refused before any connection (port rule).
	_, err := NewFetcher(DefaultFetcherConfig()).Get(context.Background(), srv.URL)
	if r := reasonOf(t, err); r != "bad_url" {
		t.Fatalf("default config: reason %q", r)
	}
	// With the port rule lifted, the address guard refuses 127.0.0.1.
	_, err = guardedFetcher().Get(context.Background(), srv.URL)
	if r := reasonOf(t, err); r != "blocked_address" {
		t.Fatalf("guarded: reason %q", r)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("server was reached %d times", n)
	}
	// And AllowPrivate does reach it.
	if _, err := testFetcher(t, FetcherConfig{}).Get(context.Background(), srv.URL); err != nil || hits.Load() != 1 {
		t.Fatalf("AllowPrivate: %v hits=%d", err, hits.Load())
	}
}

func TestGuardRefusals(t *testing.T) {
	lookup := fakeLookup(map[string][]string{
		"localhost.test": {"127.0.0.1"},
		"metadata.test":  {"169.254.169.254"},
		"mixed.test":     {"93.184.216.34", "10.0.0.5"},
		"mapped.test":    {"::ffff:192.168.0.1"},
		"v6local.test":   {"fd00::1"},
		"cgnat.test":     {"100.64.1.1"},
		"empty.test":     {},
	})
	cases := []struct{ url, reason string }{
		{"http://localhost.test/", "blocked_address"},
		{"http://metadata.test/latest/meta-data/", "blocked_address"},
		{"http://mixed.test/", "blocked_address"},
		{"http://mapped.test/", "blocked_address"},
		{"http://v6local.test/", "blocked_address"},
		{"http://cgnat.test/", "blocked_address"},
		{"http://127.0.0.1/", "blocked_address"},
		{"http://169.254.169.254/", "blocked_address"},
		{"http://[::1]/", "blocked_address"},
		{"http://[::ffff:127.0.0.1]/", "blocked_address"},
		{"http://0.0.0.0/", "blocked_address"},
		{"http://nowhere.test/", "dns"},
		{"http://empty.test/", "dns"},
		{"http://localhost.test:8080/", "bad_url"},
		{"ftp://localhost.test/", "bad_url"},
		{"http://u:p@localhost.test/", "bad_url"},
	}
	f := NewFetcher(DefaultFetcherConfig())
	f.lookup = lookup
	for _, c := range cases {
		_, err := f.Get(context.Background(), c.url)
		if err == nil {
			t.Errorf("%s: no error", c.url)
			continue
		}
		if r := reasonOf(t, err); r != c.reason {
			t.Errorf("%s: reason %q, want %q (%v)", c.url, r, c.reason, err)
		}
	}
}

func TestControlHookRefusesBlockedAddress(t *testing.T) {
	f := NewFetcher(DefaultFetcherConfig())
	for _, a := range []string{"127.0.0.1:80", "[::1]:443", "10.1.2.3:80", "not-an-addr"} {
		if err := f.control("tcp", a, nil); err == nil {
			t.Errorf("control(%s) allowed", a)
		}
	}
	if err := f.control("tcp", "93.184.216.34:443", nil); err != nil {
		t.Errorf("control(public) = %v", err)
	}
	if err := testFetcher(t, FetcherConfig{}).control("tcp", "127.0.0.1:80", nil); err != nil {
		t.Errorf("AllowPrivate control = %v", err)
	}
}

func TestDialRefusesPortAndBlockedAddress(t *testing.T) {
	f := NewFetcher(DefaultFetcherConfig())
	d := &net.Dialer{}
	if _, err := f.dial(context.Background(), d, "tcp", "93.184.216.34:22"); reasonOf(t, err) != "bad_url" {
		t.Errorf("port 22: %v", err)
	}
	if _, err := f.dial(context.Background(), d, "tcp", "192.168.1.1:443"); reasonOf(t, err) != "blocked_address" {
		t.Errorf("private: %v", err)
	}
}

func TestRedirectHopsAreRechecked(t *testing.T) {
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/to-private":
			http.Redirect(w, r, "http://10.0.0.1"+strings.TrimPrefix(srvURL, "http://127.0.0.1")+"/", http.StatusFound)
		case "/to-metadata":
			http.Redirect(w, r, "http://169.254.169.254/latest/", http.StatusFound)
		case "/to-ftp":
			http.Redirect(w, r, "ftp://example.com/", http.StatusFound)
		case "/to-userinfo":
			http.Redirect(w, r, "http://a:b@127.0.0.1/", http.StatusFound)
		}
	}))
	defer srv.Close()
	srvURL = srv.URL

	// Loopback is allowed (to reach the test server); everything else follows the real rules.
	f := guardedFetcher()
	f.blocked = func(a netip.Addr) bool { return !a.IsLoopback() && isBlockedAddr(a) }
	for path, want := range map[string]string{
		"/to-private":  "blocked_address",
		"/to-metadata": "blocked_address",
		"/to-ftp":      "bad_url",
		"/to-userinfo": "bad_url",
	} {
		_, err := f.Get(context.Background(), srv.URL+path)
		if r := reasonOf(t, err); r != want {
			t.Errorf("%s: reason %q, want %q", path, r, want)
		}
	}
	// A redirect to a non-80/443 port is refused by the hop check.
	if err := checkURL(mustParse(t, "http://example.com:8080/"), false); err == nil {
		t.Error("redirect to port 8080 allowed")
	}
}

func TestRedirectLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := len(r.URL.Path) // "/", "/x", "/xx", …
		if n <= 6 {
			http.Redirect(w, r, r.URL.Path+"x", http.StatusMovedPermanently)
			return
		}
		_, _ = w.Write([]byte("<html>done</html>"))
	}))
	defer srv.Close()
	f := testFetcher(t, FetcherConfig{})
	// "/" → … → "/xxxxxx" is 6 redirects: one too many.
	if _, err := f.Get(context.Background(), srv.URL+"/"); reasonOf(t, err) != "redirects" {
		t.Fatalf("6 redirects: %v", err)
	}
	// From "/x" it is 5 redirects: allowed.
	resp, err := f.Get(context.Background(), srv.URL+"/x")
	if err != nil || resp.Redirects != 5 || !strings.HasSuffix(resp.FinalURL, "/xxxxxx") {
		t.Fatalf("5 redirects: %+v %v", resp, err)
	}
}

func TestFetchFailures(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()
	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/chunked" {
			for i := 0; i < 20; i++ {
				_, _ = w.Write([]byte(strings.Repeat("a", 100)))
				w.(http.Flusher).Flush()
			}
			return
		}
		_, _ = w.Write([]byte(strings.Repeat("a", 2000))) // sets Content-Length
	}))
	defer big.Close()
	untrusted := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	untrusted.Config.ErrorLog = log.New(io.Discard, "", 0)
	untrusted.StartTLS()
	defer untrusted.Close()
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()

	f := testFetcher(t, FetcherConfig{Timeout: 300 * time.Millisecond, MaxBytes: 1000})
	cases := []struct{ url, reason string }{
		{slow.URL, "timeout"},
		{big.URL + "/", "too_large"},
		{big.URL + "/chunked", "too_large"},
		{untrusted.URL, "tls"},
		{closedURL, "refused"},
	}
	for _, c := range cases {
		_, err := f.Get(context.Background(), c.url)
		if err == nil {
			t.Errorf("%s: no error", c.url)
			continue
		}
		if r := reasonOf(t, err); r != c.reason {
			t.Errorf("%s: reason %q, want %q (%v)", c.url, r, c.reason, err)
		}
	}
}

func TestGetSendsUserAgentAndReturnsStatus(t *testing.T) {
	var ua string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua = r.UserAgent()
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	resp, err := testFetcher(t, FetcherConfig{}).Get(context.Background(), srv.URL)
	if err != nil || resp.StatusCode != 404 {
		t.Fatalf("%+v %v", resp, err)
	}
	if ua != DefaultFetcherConfig().UserAgent {
		t.Fatalf("user agent %q", ua)
	}
}

func TestDefaultFetcherConfig(t *testing.T) {
	c := DefaultFetcherConfig()
	if c.Timeout != 10*time.Second || c.MaxBytes != 5<<20 || c.MaxRedirects != 5 ||
		c.UserAgent != "SiteMomentBot/1.0 (+https://sitemoment.com/rank)" || c.AllowPrivate {
		t.Fatalf("%+v", c)
	}
}
