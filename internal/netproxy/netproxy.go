// SPDX-License-Identifier: AGPL-3.0-or-later

// Package netproxy makes the app's outbound HTTP follow the operating
// system's proxy settings. Go's net/http only reads HTTP(S)_PROXY from the
// environment, which an app started from Finder or the Start menu does not
// have, while users (in China especially) run Clash/V2Ray as a system proxy.
//
// Resolution, per request (owner decision 2026-11-11: just follow the
// machine's system proxy, nothing to configure in the app):
//
//  1. loopback, private and link-local addresses, localhost and *.local are
//     always direct (the OAuth callback on 127.0.0.1 must never be proxied);
//  2. the OS system proxy (macOS `scutil --proxy`, the Windows registry),
//     honouring its exception list;
//  3. if the OS has no proxy configured (or only a PAC script, which is not
//     evaluated), HTTPS_PROXY/HTTP_PROXY/ALL_PROXY/NO_PROXY from the
//     environment;
//  4. direct.
package netproxy

import (
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http/httpproxy"
)

// DefaultTTL is how long a system proxy lookup is reused, so that turning
// Clash on or off is picked up within half a minute without a restart.
const DefaultTTL = 30 * time.Second

// System is the OS proxy configuration. Proxy addresses are host:port; ""
// means that kind of proxy is off.
type System struct {
	HTTP, HTTPS, SOCKS string
	// Exceptions are the hosts the OS sends direct: names, *-globs,
	// CIDRs (macOS also writes "169.254/16") and "<local>" (no dot).
	Exceptions []string
	// ExcludeSimple sends dotless host names direct (macOS).
	ExcludeSimple bool
	// PAC is the auto-config script URL when one is enabled. It is not
	// evaluated.
	PAC string
}

func (s System) configured() bool { return s.HTTP != "" || s.HTTPS != "" || s.SOCKS != "" }

// Source says where the proxy in effect comes from.
type Source string

const (
	SourceSystem Source = "system"
	SourceEnv    Source = "env"
	// SourcePACUnsupported: the OS uses a PAC script, which is not
	// evaluated, and the environment has no proxy either, so requests go
	// direct.
	SourcePACUnsupported Source = "pac-unsupported"
	SourceDirect         Source = "direct"
)

// Status is what the Settings page shows.
type Status struct {
	Source Source `json:"source"`
	// HTTPProxy, HTTPSProxy and SOCKS are host:port for the system proxy,
	// the variables' values for the environment.
	HTTPProxy  string   `json:"httpProxy"`
	HTTPSProxy string   `json:"httpsProxy"`
	SOCKS      string   `json:"socks"`
	Exceptions []string `json:"exceptions"`
	// PAC is the OS's auto-config URL, if any (shown as unsupported).
	PAC string `json:"pac"`
	// Error is a failure to read the OS settings (requests then use the
	// environment or go direct).
	Error string `json:"error"`
}

// Resolver picks the proxy for each request. The zero value with ReadSystem
// and Getenv set is usable; New fills in the real OS reader.
type Resolver struct {
	// Getenv reads the proxy variables (os.Getenv in production).
	Getenv func(string) string
	// ReadSystem reads the OS proxy settings.
	ReadSystem func() (System, error)
	// TTL is how long a ReadSystem result is reused; 0 means DefaultTTL.
	TTL time.Duration

	now func() time.Time // tests

	mu    sync.Mutex
	sys   System
	err   error
	sysAt time.Time
	read  bool

	transportOnce sync.Once
	transport     *http.Transport
}

// New returns a Resolver that reads this machine's system proxy and the
// process environment.
func New() *Resolver {
	return &Resolver{Getenv: os.Getenv, ReadSystem: readSystem}
}

// system returns the OS settings, re-read at most once per TTL.
func (r *Resolver) system() (System, error) {
	if r.ReadSystem == nil {
		return System{}, nil
	}
	now := time.Now
	if r.now != nil {
		now = r.now
	}
	ttl := r.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if t := now(); !r.read || t.Sub(r.sysAt) >= ttl || t.Before(r.sysAt) {
		r.sys, r.err = r.ReadSystem()
		r.sysAt, r.read = t, true
	}
	return r.sys, r.err
}

// envConfig is the environment's proxy configuration, and whether it sets
// any proxy at all (NO_PROXY alone does not count).
func (r *Resolver) envConfig() (*httpproxy.Config, bool) {
	get := r.Getenv
	if get == nil {
		get = func(string) string { return "" }
	}
	pick := func(names ...string) string {
		for _, n := range names {
			if v := strings.TrimSpace(get(n)); v != "" {
				return v
			}
		}
		return ""
	}
	all := pick("ALL_PROXY", "all_proxy")
	c := &httpproxy.Config{
		HTTPProxy:  pick("HTTP_PROXY", "http_proxy"),
		HTTPSProxy: pick("HTTPS_PROXY", "https_proxy"),
		NoProxy:    pick("NO_PROXY", "no_proxy"),
	}
	if c.HTTPProxy == "" {
		c.HTTPProxy = all
	}
	if c.HTTPSProxy == "" {
		c.HTTPSProxy = all
	}
	return c, c.HTTPProxy != "" || c.HTTPSProxy != ""
}

// Proxy is an http.Transport Proxy function.
func (r *Resolver) Proxy(req *http.Request) (*url.URL, error) {
	return r.ProxyFor(req.URL)
}

// ProxyFor returns the proxy for a request to u, or nil for direct.
func (r *Resolver) ProxyFor(u *url.URL) (*url.URL, error) {
	host := normHost(u.Hostname())
	if alwaysDirect(host) {
		return nil, nil
	}
	if s, err := r.system(); err == nil && s.configured() {
		return s.proxyFor(strings.ToLower(u.Scheme), host), nil
	}
	if c, ok := r.envConfig(); ok {
		return c.ProxyFunc()(u)
	}
	return nil, nil
}

// Status describes the proxy in effect for the Settings page.
func (r *Resolver) Status() Status {
	s, err := r.system()
	st := Status{Source: SourceDirect, PAC: s.PAC, Exceptions: []string{}}
	if err != nil {
		st.Error = err.Error()
	}
	if err == nil && s.configured() {
		st.Source, st.HTTPProxy, st.HTTPSProxy, st.SOCKS = SourceSystem, s.HTTP, s.HTTPS, s.SOCKS
		st.Exceptions = append(st.Exceptions, s.Exceptions...)
		if s.ExcludeSimple {
			st.Exceptions = append(st.Exceptions, "<simple host names>")
		}
		return st
	}
	if c, ok := r.envConfig(); ok {
		st.Source, st.HTTPProxy, st.HTTPSProxy = SourceEnv, c.HTTPProxy, c.HTTPSProxy
		if c.HTTPProxy == c.HTTPSProxy && isSocks(c.HTTPProxy) {
			st.SOCKS, st.HTTPProxy, st.HTTPSProxy = c.HTTPProxy, "", ""
		}
		for _, e := range strings.Split(c.NoProxy, ",") {
			if e = strings.TrimSpace(e); e != "" {
				st.Exceptions = append(st.Exceptions, e)
			}
		}
		return st
	}
	if s.PAC != "" {
		st.Source = SourcePACUnsupported
	}
	return st
}

func isSocks(v string) bool {
	v = strings.ToLower(v)
	return strings.HasPrefix(v, "socks5://") || strings.HasPrefix(v, "socks5h://")
}

// Transport is the app's shared transport: http.DefaultTransport's settings
// with the resolver as its Proxy. Every outbound client should use it (or
// Proxy, for a transport with its own settings).
func (r *Resolver) Transport() *http.Transport {
	r.transportOnce.Do(func() {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.Proxy = r.Proxy
		r.transport = t
	})
	return r.transport
}

// Client returns a client on the shared transport; timeout 0 means none
// (the caller's context bounds the call).
func (r *Resolver) Client(timeout time.Duration) *http.Client {
	return &http.Client{Transport: r.Transport(), Timeout: timeout}
}

// proxyFor picks the OS proxy for scheme: HTTPS requests use the secure web
// proxy, else the web proxy, else SOCKS; HTTP requests the web proxy, else
// SOCKS.
func (s System) proxyFor(scheme, host string) *url.URL {
	if s.bypass(host) {
		return nil
	}
	if scheme == "https" && s.HTTPS != "" {
		return &url.URL{Scheme: "http", Host: s.HTTPS}
	}
	if s.HTTP != "" {
		return &url.URL{Scheme: "http", Host: s.HTTP}
	}
	if s.SOCKS != "" {
		return &url.URL{Scheme: "socks5", Host: s.SOCKS}
	}
	return nil
}

func (s System) bypass(host string) bool {
	if s.ExcludeSimple && isSimple(host) {
		return true
	}
	for _, p := range s.Exceptions {
		if matchException(host, p) {
			return true
		}
	}
	return false
}

// normHost lower-cases a host name and drops a trailing dot.
func normHost(h string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
}

// isSimple reports whether host is a dotless name (not an IP address).
func isSimple(host string) bool {
	if _, err := netip.ParseAddr(host); err == nil {
		return false
	}
	return host != "" && !strings.Contains(host, ".")
}

// alwaysDirect: the local machine and the local network never go through a
// proxy.
func alwaysDirect(host string) bool {
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return true
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	a = a.Unmap()
	return a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() || a.IsUnspecified()
}

// matchException reports whether host (already normHost'ed) matches one OS
// exception pattern.
func matchException(host, pat string) bool {
	pat = normHost(pat)
	switch {
	case pat == "":
		return false
	case pat == "<local>":
		return isSimple(host)
	case strings.Contains(pat, "/"):
		p, ok := parsePrefix(pat)
		if !ok {
			return false
		}
		a, err := netip.ParseAddr(host)
		return err == nil && p.Contains(a.Unmap())
	case strings.Contains(pat, "*"):
		return globMatch(pat, host)
	case strings.HasPrefix(pat, "."):
		return strings.HasSuffix(host, pat)
	default:
		return host == strings.Trim(pat, "[]")
	}
}

// parsePrefix parses a CIDR, including macOS's short IPv4 form "169.254/16".
func parsePrefix(s string) (netip.Prefix, bool) {
	if p, err := netip.ParsePrefix(s); err == nil {
		return p.Masked(), true
	}
	addr, bits, ok := strings.Cut(s, "/")
	if !ok || strings.Contains(addr, ":") {
		return netip.Prefix{}, false
	}
	parts := strings.Split(addr, ".")
	if len(parts) == 0 || len(parts) > 4 {
		return netip.Prefix{}, false
	}
	for len(parts) < 4 {
		parts = append(parts, "0")
	}
	p, err := netip.ParsePrefix(strings.Join(parts, ".") + "/" + bits)
	if err != nil {
		return netip.Prefix{}, false
	}
	return p.Masked(), true
}

// globMatch matches a pattern containing at least one *, which stands for
// any run of characters.
func globMatch(pat, s string) bool {
	parts := strings.Split(pat, "*")
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	last := parts[len(parts)-1]
	for _, p := range parts[1 : len(parts)-1] {
		i := strings.Index(s, p)
		if i < 0 {
			return false
		}
		s = s[i+len(p):]
	}
	return strings.HasSuffix(s, last)
}

// hostPort joins a proxy host and port, defaulting the port.
func hostPort(host, port, def string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if port = strings.TrimSpace(port); port == "" || port == "0" {
		port = def
	}
	return net.JoinHostPort(strings.Trim(host, "[]"), port)
}
