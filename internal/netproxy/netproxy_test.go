// SPDX-License-Identifier: AGPL-3.0-or-later
package netproxy

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func sys(s System, err error) func() (System, error) {
	return func() (System, error) { return s, err }
}

var clash = System{HTTP: "127.0.0.1:7890", HTTPS: "127.0.0.1:7890", SOCKS: "127.0.0.1:7891",
	Exceptions: []string{"*.local", "169.254/16", "<local>", "*.qq.com", "baidu.com", "10.*", "192.168.0.0/16"}}

func proxyOf(t *testing.T, r *Resolver, raw string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	u, err := r.Proxy(req)
	if err != nil {
		t.Fatalf("Proxy(%s): %v", raw, err)
	}
	if u == nil {
		return "direct"
	}
	return u.String()
}

func TestSystemProxy(t *testing.T) {
	r := &Resolver{Getenv: env(nil), ReadSystem: sys(clash, nil)}
	cases := map[string]string{
		"https://www.google.com/":         "http://127.0.0.1:7890",
		"http://example.com/":             "http://127.0.0.1:7890",
		"https://sitemoment.com/api/x":    "http://127.0.0.1:7890",
		"http://printer.local/":           "direct", // *.local
		"http://intranet/":                "direct", // <local>: no dot
		"https://im.qq.com/":              "direct", // *.qq.com
		"https://baidu.com/":              "direct", // exact
		"https://www.baidu.com/":          "http://127.0.0.1:7890",
		"http://10.1.2.3/":                "direct", // private literal (and 10.*)
		"http://169.254.169.254/":         "direct",
		"http://192.168.1.1:8080/":        "direct",
		"http://127.0.0.1:1234/callback":  "direct",
		"http://localhost:8080/":          "direct",
		"http://[::1]:8080/":              "direct",
		"http://app.localhost/":           "direct",
		"https://93.184.216.34/":          "http://127.0.0.1:7890",
		"https://www.google.com:443/path": "http://127.0.0.1:7890",
	}
	for in, want := range cases {
		if got := proxyOf(t, r, in); got != want {
			t.Errorf("%s: got %s, want %s", in, got, want)
		}
	}
}

func TestSystemProxySchemePreference(t *testing.T) {
	cases := []struct {
		sys         System
		http, https string
	}{
		{System{HTTP: "h:1", HTTPS: "s:2", SOCKS: "k:3"}, "http://h:1", "http://s:2"},
		{System{HTTP: "h:1", SOCKS: "k:3"}, "http://h:1", "http://h:1"},
		{System{HTTPS: "s:2", SOCKS: "k:3"}, "socks5://k:3", "http://s:2"},
		{System{SOCKS: "k:3"}, "socks5://k:3", "socks5://k:3"},
		{System{}, "direct", "direct"},
		{System{PAC: "http://127.0.0.1/pac"}, "direct", "direct"},
	}
	for _, c := range cases {
		r := &Resolver{Getenv: env(nil), ReadSystem: sys(c.sys, nil)}
		if got := proxyOf(t, r, "http://example.com/"); got != c.http {
			t.Errorf("%+v http: got %s want %s", c.sys, got, c.http)
		}
		if got := proxyOf(t, r, "https://example.com/"); got != c.https {
			t.Errorf("%+v https: got %s want %s", c.sys, got, c.https)
		}
	}
}

func TestExcludeSimpleHostnames(t *testing.T) {
	r := &Resolver{Getenv: env(nil), ReadSystem: sys(System{HTTP: "h:1", ExcludeSimple: true}, nil)}
	if got := proxyOf(t, r, "http://wiki/"); got != "direct" {
		t.Errorf("simple host: %s", got)
	}
	if got := proxyOf(t, r, "http://wiki.example/"); got != "http://h:1" {
		t.Errorf("dotted host: %s", got)
	}
}

func TestSystemBeatsEnv(t *testing.T) {
	r := &Resolver{
		Getenv:     env(map[string]string{"HTTPS_PROXY": "http://127.0.0.1:1081", "http_proxy": "http://127.0.0.1:1082"}),
		ReadSystem: sys(clash, nil),
	}
	if got := proxyOf(t, r, "https://www.google.com/"); got != "http://127.0.0.1:7890" {
		t.Errorf("https: %s", got)
	}
	// A host in the system's exceptions is direct, not handed to env.
	if got := proxyOf(t, r, "https://baidu.com/"); got != "direct" {
		t.Errorf("exception: %s", got)
	}
	if st := r.Status(); st.Source != SourceSystem {
		t.Errorf("status %+v", st)
	}
}

func TestEnvWhenNoSystemProxy(t *testing.T) {
	for _, s := range []struct {
		name string
		sys  System
		err  error
	}{
		{"off", System{Exceptions: []string{"*.local"}}, nil},
		{"pac", System{PAC: "http://127.0.0.1:33331/pac"}, nil},
		{"read error", System{}, errors.New("scutil: boom")},
	} {
		r := &Resolver{
			Getenv:     env(map[string]string{"HTTPS_PROXY": "http://127.0.0.1:1081", "http_proxy": "http://127.0.0.1:1082", "NO_PROXY": "internal.example"}),
			ReadSystem: sys(s.sys, s.err),
		}
		if got := proxyOf(t, r, "https://www.google.com/"); got != "http://127.0.0.1:1081" {
			t.Errorf("%s https: %s", s.name, got)
		}
		if got := proxyOf(t, r, "http://example.com/"); got != "http://127.0.0.1:1082" {
			t.Errorf("%s http: %s", s.name, got)
		}
		if got := proxyOf(t, r, "https://internal.example/"); got != "direct" {
			t.Errorf("%s no_proxy: %s", s.name, got)
		}
		if got := proxyOf(t, r, "http://127.0.0.1:5555/callback"); got != "direct" {
			t.Errorf("%s loopback: %s", s.name, got)
		}
		st := r.Status()
		if st.Source != SourceEnv || st.HTTPSProxy != "http://127.0.0.1:1081" || st.HTTPProxy != "http://127.0.0.1:1082" || st.PAC != s.sys.PAC {
			t.Errorf("%s status %+v", s.name, st)
		}
	}
}

func TestEnvAllProxy(t *testing.T) {
	r := &Resolver{Getenv: env(map[string]string{"ALL_PROXY": "socks5://127.0.0.1:7891"}), ReadSystem: sys(System{}, nil)}
	for _, u := range []string{"http://example.com/", "https://example.com/"} {
		if got := proxyOf(t, r, u); got != "socks5://127.0.0.1:7891" {
			t.Errorf("%s: %s", u, got)
		}
	}
	if st := r.Status(); st.Source != SourceEnv || st.SOCKS != "socks5://127.0.0.1:7891" {
		t.Errorf("status %+v", st)
	}
}

func TestStatus(t *testing.T) {
	r := &Resolver{Getenv: env(nil), ReadSystem: sys(clash, nil)}
	st := r.Status()
	if st.Source != SourceSystem || st.HTTPProxy != "127.0.0.1:7890" || st.HTTPSProxy != "127.0.0.1:7890" || st.SOCKS != "127.0.0.1:7891" || len(st.Exceptions) != len(clash.Exceptions) {
		t.Errorf("system %+v", st)
	}
	r = &Resolver{Getenv: env(nil), ReadSystem: sys(System{PAC: "http://127.0.0.1:33331/pac"}, nil)}
	if st := r.Status(); st.Source != SourcePACUnsupported || st.PAC != "http://127.0.0.1:33331/pac" {
		t.Errorf("pac %+v", st)
	}
	r = &Resolver{Getenv: env(nil), ReadSystem: sys(System{}, errors.New("scutil: boom"))}
	if st := r.Status(); st.Source != SourceDirect || st.Error == "" {
		t.Errorf("error %+v", st)
	}
	r = &Resolver{Getenv: env(nil), ReadSystem: sys(System{Exceptions: []string{"*.local"}}, nil)}
	if st := r.Status(); st.Source != SourceDirect || st.Error != "" {
		t.Errorf("off %+v", st)
	}
}

func TestSystemCache(t *testing.T) {
	var calls atomic.Int32
	now := time.Unix(1000, 0)
	cur := System{HTTP: "h:1"}
	r := &Resolver{Getenv: env(nil), TTL: 30 * time.Second, now: func() time.Time { return now },
		ReadSystem: func() (System, error) { calls.Add(1); return cur, nil }}
	proxyOf(t, r, "http://a.example/")
	proxyOf(t, r, "http://b.example/")
	r.Status()
	if n := calls.Load(); n != 1 {
		t.Fatalf("reads within TTL = %d", n)
	}
	// Turning the proxy off is picked up once the cache expires.
	cur = System{}
	now = now.Add(31 * time.Second)
	if got := proxyOf(t, r, "http://a.example/"); got != "direct" || calls.Load() != 2 {
		t.Fatalf("after expiry: %s calls=%d", got, calls.Load())
	}
}

func TestMatchException(t *testing.T) {
	cases := []struct {
		host, pat string
		want      bool
	}{
		{"printer.local", "*.local", true},
		{"local", "*.local", false},
		{"169.254.1.2", "169.254/16", true},
		{"169.255.1.2", "169.254/16", false},
		{"10.2.3.4", "10.*", true},
		{"10.example.com", "10.*", true}, // a glob is a glob
		{"172.20.0.1", "172.16.0.0/12", true},
		{"172.32.0.1", "172.16.0.0/12", false},
		{"intranet", "<local>", true},
		{"intranet.corp", "<local>", false},
		{"::1", "::1", true},
		{"Example.COM", "example.com", true},
		{"a.example.com", ".example.com", true},
		{"a.example.com", "example.com", false},
		{"kadmin.heartie.net", "kadmin.heartie.net", true},
		{"example.com", "", false},
		{"example.com", "not/a/cidr", false},
	}
	for _, c := range cases {
		if got := matchException(normHost(c.host), c.pat); got != c.want {
			t.Errorf("%s ~ %s: %v", c.host, c.pat, got)
		}
	}
}

// TestTransportUsesProxy checks that Client() really sends requests to the
// proxy the resolver picks.
func TestTransportUsesProxy(t *testing.T) {
	var seen atomic.Value
	px := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Store(r.URL.String())
		w.WriteHeader(http.StatusNoContent)
	}))
	defer px.Close()
	pu, _ := url.Parse(px.URL)
	r := &Resolver{Getenv: env(nil), ReadSystem: sys(System{HTTP: pu.Host}, nil)}
	res, err := r.Client(5 * time.Second).Get("http://public.example/generate_204")
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent || seen.Load() != "http://public.example/generate_204" {
		t.Fatalf("status %d, proxy saw %v", res.StatusCode, seen.Load())
	}
	if r.Transport() != r.Transport() {
		t.Fatal("Transport is not shared")
	}
}
