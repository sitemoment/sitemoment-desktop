// SPDX-License-Identifier: AGPL-3.0-or-later

package rank

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// FetcherConfig bounds every server-side fetch.
type FetcherConfig struct {
	// Timeout is the total budget of one Get, redirects and body included.
	Timeout time.Duration
	// MaxBytes caps the (decompressed) body; larger bodies fail with too_large.
	MaxBytes int64
	// MaxRedirects is the number of redirect hops followed; negative means
	// none (zero takes the default, like the other fields).
	MaxRedirects int
	UserAgent    string
	// AllowPrivate turns the address and port guard off so tests can reach
	// httptest servers on 127.0.0.1 and random ports. Never set it in
	// production.
	AllowPrivate bool
}

// DefaultFetcherConfig is the production configuration from the spec.
func DefaultFetcherConfig() FetcherConfig {
	return FetcherConfig{
		Timeout:      10 * time.Second,
		MaxBytes:     5 << 20,
		MaxRedirects: 5,
		UserAgent:    "SiteMomentBot/1.0 (+https://sitemoment.com/rank)",
	}
}

// Response is a fetched resource. Body is fully read (and capped).
type Response struct {
	URL        string // the normalized requested URL
	FinalURL   string // the URL after redirects
	StatusCode int
	Header     http.Header
	Body       []byte
	TimeMs     int64 // total time, redirects and body included
	Redirects  int
	// HTTPSUpgrade is true when some redirect hop went from http to https.
	HTTPSUpgrade bool
}

// FetchError is a refused or failed fetch. Reason is one of dns, refused,
// timeout, tls, too_large, status, not_html, blocked_address, bad_url or
// redirects. Get itself never returns status or not_html; Audit and
// ReadSitemap do.
type FetchError struct {
	Reason string
	Err    error
}

func (e *FetchError) Error() string {
	if e.Err == nil {
		return "fetch failed: " + e.Reason
	}
	return "fetch failed: " + e.Reason + ": " + e.Err.Error()
}

func (e *FetchError) Unwrap() error { return e.Err }

// Fetcher performs SSRF-guarded GET requests. It resolves host names itself,
// refuses non-public addresses, dials exactly the checked address, allows
// only ports 80 and 443 and re-checks every redirect hop. A Fetcher is safe
// for concurrent use and reuses connections.
type Fetcher struct {
	cfg       FetcherConfig
	transport *http.Transport
	// lookup resolves a host name; replaced in tests.
	lookup func(ctx context.Context, host string) ([]netip.Addr, error)
	// blocked decides whether an address is refused; nil allows everything.
	blocked func(netip.Addr) bool
	// anyPort lifts the 80/443 rule.
	anyPort bool
}

// NewFetcher builds a Fetcher; zero fields of cfg take their defaults.
func NewFetcher(cfg FetcherConfig) *Fetcher {
	def := DefaultFetcherConfig()
	if cfg.Timeout <= 0 {
		cfg.Timeout = def.Timeout
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = def.MaxBytes
	}
	switch {
	case cfg.MaxRedirects == 0:
		cfg.MaxRedirects = def.MaxRedirects
	case cfg.MaxRedirects < 0:
		cfg.MaxRedirects = 0
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = def.UserAgent
	}
	f := &Fetcher{
		cfg: cfg,
		lookup: func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		},
		anyPort: cfg.AllowPrivate,
	}
	if !cfg.AllowPrivate {
		f.blocked = isBlockedAddr
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second, Control: f.control}
	f.transport = &http.Transport{
		Proxy: nil, // never let an environment proxy bypass the guard
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return f.dial(ctx, dialer, network, addr)
		},
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:    5 * time.Second,
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           64,
		MaxIdleConnsPerHost:    4,
		IdleConnTimeout:        30 * time.Second,
		MaxResponseHeaderBytes: 64 << 10,
	}
	return f
}

// agentToken is the product token of the User-Agent ("SiteMomentBot"), the
// name robots.txt groups address.
func (f *Fetcher) agentToken() string {
	return strings.TrimSpace(strings.SplitN(f.cfg.UserAgent, "/", 2)[0])
}

// control is the last line of defence: it runs on the socket right before
// connect and refuses blocked addresses even if a caller bypassed dial.
func (f *Fetcher) control(_, address string, _ syscall.RawConn) error {
	if f.blocked == nil {
		return nil
	}
	ap, err := netip.ParseAddrPort(address)
	if err != nil || f.blocked(ap.Addr()) {
		return &FetchError{Reason: "blocked_address", Err: fmt.Errorf("address %s is not public", address)}
	}
	return nil
}

// dial resolves addr, checks every address and dials the checked ones.
func (f *Fetcher) dial(ctx context.Context, d *net.Dialer, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, &FetchError{Reason: "bad_url", Err: err}
	}
	if !f.anyPort && port != "80" && port != "443" {
		return nil, &FetchError{Reason: "bad_url", Err: fmt.Errorf("port %s is not allowed", port)}
	}
	var addrs []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		addrs = []netip.Addr{ip}
	} else {
		addrs, err = f.lookup(ctx, host)
		if err != nil {
			return nil, &FetchError{Reason: "dns", Err: err}
		}
		if len(addrs) == 0 {
			return nil, &FetchError{Reason: "dns", Err: fmt.Errorf("no addresses for %s", host)}
		}
	}
	if f.blocked != nil {
		for _, a := range addrs {
			if f.blocked(a) {
				return nil, &FetchError{Reason: "blocked_address", Err: fmt.Errorf("%s resolves to a non-public address", host)}
			}
		}
	}
	var last error
	for _, a := range addrs {
		c, err := d.DialContext(ctx, network, net.JoinHostPort(a.Unmap().String(), port))
		if err == nil {
			return c, nil
		}
		last = err
		if ctx.Err() != nil {
			break
		}
	}
	return nil, last
}

// Get fetches rawURL with the guard, following at most MaxRedirects
// redirects. Any HTTP status is returned as a Response; failures and
// refusals are *FetchError.
func (f *Fetcher) Get(ctx context.Context, rawURL string) (*Response, error) {
	u, err := normalizeURL(rawURL, f.anyPort)
	if err != nil {
		return nil, &FetchError{Reason: "bad_url", Err: err}
	}
	ctx, cancel := context.WithTimeout(ctx, f.cfg.Timeout)
	defer cancel()

	out := &Response{URL: u}
	client := &http.Client{
		Transport: f.transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > f.cfg.MaxRedirects {
				return &FetchError{Reason: "redirects", Err: fmt.Errorf("more than %d redirects", f.cfg.MaxRedirects)}
			}
			if err := checkURL(req.URL, f.anyPort); err != nil {
				return &FetchError{Reason: "bad_url", Err: fmt.Errorf("redirect to %q: %w", req.URL.Redacted(), err)}
			}
			prev := via[len(via)-1].URL
			if strings.EqualFold(prev.Scheme, "http") && strings.EqualFold(req.URL.Scheme, "https") {
				out.HTTPSUpgrade = true
			}
			out.Redirects = len(via)
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, &FetchError{Reason: "bad_url", Err: err}
	}
	req.Header.Set("User-Agent", f.cfg.UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return nil, classify(ctx, err)
	}
	defer resp.Body.Close()
	if resp.ContentLength > f.cfg.MaxBytes {
		return nil, &FetchError{Reason: "too_large", Err: fmt.Errorf("content length %d over %d bytes", resp.ContentLength, f.cfg.MaxBytes)}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, f.cfg.MaxBytes+1))
	if err != nil {
		return nil, classify(ctx, err)
	}
	if int64(len(body)) > f.cfg.MaxBytes {
		return nil, &FetchError{Reason: "too_large", Err: fmt.Errorf("body over %d bytes", f.cfg.MaxBytes)}
	}
	out.FinalURL = resp.Request.URL.String()
	out.StatusCode = resp.StatusCode
	out.Header = resp.Header
	out.Body = body
	out.TimeMs = time.Since(start).Milliseconds()
	return out, nil
}

// classify maps a transport error to a FetchError reason.
func classify(ctx context.Context, err error) *FetchError {
	var fe *FetchError
	if errors.As(err, &fe) {
		return fe
	}
	var (
		dnsErr   *net.DNSError
		netErr   net.Error
		certErr  *tls.CertificateVerificationError
		recErr   tls.RecordHeaderError
		alertErr tls.AlertError
		uaErr    x509.UnknownAuthorityError
		hostErr  x509.HostnameError
		invErr   x509.CertificateInvalidError
	)
	switch {
	case errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil:
		return &FetchError{Reason: "timeout", Err: err}
	case errors.As(err, &dnsErr):
		return &FetchError{Reason: "dns", Err: err}
	case errors.As(err, &certErr), errors.As(err, &recErr), errors.As(err, &alertErr),
		errors.As(err, &uaErr), errors.As(err, &hostErr), errors.As(err, &invErr),
		strings.Contains(err.Error(), "tls: "):
		return &FetchError{Reason: "tls", Err: err}
	case errors.As(err, &netErr) && netErr.Timeout():
		return &FetchError{Reason: "timeout", Err: err}
	default:
		return &FetchError{Reason: "refused", Err: err}
	}
}

// originOf returns scheme://host of an absolute URL.
func originOf(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, errBadURL
	}
	return &url.URL{Scheme: u.Scheme, Host: u.Host}, nil
}
