// SPDX-License-Identifier: AGPL-3.0-or-later

package rank

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
)

// fakeProxy is an HTTP forward proxy on 127.0.0.1 (a random port) that
// answers every request itself and records what it was asked for.
type fakeProxy struct {
	srv  *httptest.Server
	hits atomic.Int32
	mu   sync.Mutex
	seen []string
}

func newFakeProxy(t *testing.T, h http.HandlerFunc) *fakeProxy {
	t.Helper()
	p := &fakeProxy{}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.hits.Add(1)
		p.mu.Lock()
		p.seen = append(p.seen, r.URL.String())
		p.mu.Unlock()
		h(w, r)
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *fakeProxy) url() *url.URL {
	u, _ := url.Parse(p.srv.URL)
	return u
}

// proxiedFetcher is the production configuration (address and port guard
// on) with every request sent through p.
func proxiedFetcher(p *fakeProxy) *Fetcher {
	cfg := DefaultFetcherConfig()
	cfg.Proxy = func(*http.Request) (*url.URL, error) { return p.url(), nil }
	f := NewFetcher(cfg)
	f.lookup = fakeLookup(map[string][]string{
		"public.test":    {"93.184.216.34"},
		"other.test":     {"93.184.216.35"},
		"localhost.test": {"127.0.0.1"},
		"metadata.test":  {"169.254.169.254"},
		"mixed.test":     {"93.184.216.34", "10.0.0.5"},
	})
	return f
}

func TestProxyReachesPublicHost(t *testing.T) {
	p := newFakeProxy(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<title>ok</title>"))
	})
	// The proxy listens on 127.0.0.1 and a port other than 80/443: dialing
	// it must not trip the guard that would refuse it as a destination.
	res, err := proxiedFetcher(p).Get(context.Background(), "http://public.test/page")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 || string(res.Body) != "<title>ok</title>" {
		t.Fatalf("response %d %q", res.StatusCode, res.Body)
	}
	if len(p.seen) != 1 || p.seen[0] != "http://public.test/page" {
		t.Fatalf("proxy saw %v", p.seen)
	}
}

func TestProxyStillRefusesPrivateDestinations(t *testing.T) {
	p := newFakeProxy(t, func(w http.ResponseWriter, r *http.Request) {})
	f := proxiedFetcher(p)
	cases := []struct{ url, reason string }{
		{"http://localhost.test/", "blocked_address"},
		{"http://metadata.test/latest/meta-data/", "blocked_address"},
		{"http://mixed.test/", "blocked_address"},
		{"http://10.0.0.5/", "blocked_address"},
		{"http://127.0.0.1/", "blocked_address"},
		{"http://[::1]/", "blocked_address"},
		{"http://nowhere.test/", "dns"},
		{"http://public.test:8080/", "bad_url"},
	}
	for _, c := range cases {
		_, err := f.Get(context.Background(), c.url)
		if err == nil {
			t.Errorf("%s: no error", c.url)
			continue
		}
		if r := reasonOf(t, err); r != c.reason {
			t.Errorf("%s: reason %q, want %q", c.url, r, c.reason)
		}
	}
	if n := p.hits.Load(); n != 0 {
		t.Fatalf("proxy was asked %d times: %v", n, p.seen)
	}
}

func TestProxyRedirectIsRechecked(t *testing.T) {
	p := newFakeProxy(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Host {
		case "public.test":
			http.Redirect(w, r, "http://metadata.test/latest/meta-data/", http.StatusFound)
		case "other.test":
			http.Redirect(w, r, "http://public.test/", http.StatusMovedPermanently)
		default:
			w.Write([]byte("should not be reached"))
		}
	})
	f := proxiedFetcher(p)
	_, err := f.Get(context.Background(), "http://public.test/")
	if r := reasonOf(t, err); r != "blocked_address" {
		t.Fatalf("Get reason %q", r)
	}
	tr, err := f.Trace(context.Background(), "http://public.test/")
	if err != nil || tr.Error != "blocked_address" || len(tr.Hops) != 1 {
		t.Fatalf("Trace = %+v, %v", tr, err)
	}
	if n := p.hits.Load(); n != 2 {
		t.Fatalf("proxy hits %d (%v), want 2 (one per first hop)", n, p.seen)
	}
	// A redirect to another public host is followed through the proxy.
	tr, err = f.Trace(context.Background(), "http://other.test/")
	if err != nil || len(tr.Hops) != 2 || tr.Error != "blocked_address" {
		t.Fatalf("Trace other = %+v, %v", tr, err)
	}
}

func TestProxyNilKeepsDirectGuard(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()
	cfg := DefaultFetcherConfig()
	cfg.Proxy = func(*http.Request) (*url.URL, error) { return nil, nil } // e.g. netproxy: loopback is direct
	f := NewFetcher(cfg)
	f.anyPort = true
	_, err := f.Get(context.Background(), srv.URL)
	if r := reasonOf(t, err); r != "blocked_address" {
		t.Fatalf("reason %q", r)
	}
	if hits.Load() != 0 {
		t.Fatal("server reached")
	}
}

// TestProxyTransportDialsOnlyProxies: the transport used for proxied
// requests refuses to dial anything that is not a proxy the fetcher was
// told to use.
func TestProxyTransportDialsOnlyProxies(t *testing.T) {
	p := newFakeProxy(t, func(w http.ResponseWriter, r *http.Request) {})
	f := proxiedFetcher(p)
	if _, err := f.proxied.DialContext(context.Background(), "tcp", p.url().Host); err == nil || reasonOf(t, err) != "blocked_address" {
		t.Fatalf("dial to a proxy never used: %v", err)
	}
	if _, err := f.Get(context.Background(), "http://public.test/"); err != nil {
		t.Fatal(err)
	}
	c, err := f.proxied.DialContext(context.Background(), "tcp", p.url().Host)
	if err != nil {
		t.Fatalf("dial to the proxy in use: %v", err)
	}
	c.Close()
}
