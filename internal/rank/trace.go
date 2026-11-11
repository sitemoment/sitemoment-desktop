// SPDX-License-Identifier: AGPL-3.0-or-later

package rank

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Hop is one request in a redirect chain.
type Hop struct {
	URL    string `json:"url"`
	Status int    `json:"status"`
	// Location is the resolved redirect target ("" when the hop is not a redirect).
	Location string `json:"location,omitempty"`
}

// Trace is where a URL ends up, hop by hop.
type Trace struct {
	URL  string `json:"url"`
	Hops []Hop  `json:"hops"`
	// FinalURL and FinalStatus are the last hop's.
	FinalURL    string `json:"finalUrl"`
	FinalStatus int    `json:"finalStatus"`
	// Error is a fetch failure reason (dns, timeout, redirects, …) when the chain broke.
	Error string `json:"error,omitempty"`
}

// Redirected reports whether the first hop was a redirect.
func (t *Trace) Redirected() bool { return len(t.Hops) > 0 && isRedirect(t.Hops[0].Status) }

// Permanent reports whether every redirect in the chain is 301 or 308.
func (t *Trace) Permanent() bool {
	for _, h := range t.Hops {
		if isRedirect(h.Status) && h.Status != http.StatusMovedPermanently && h.Status != http.StatusPermanentRedirect {
			return false
		}
	}
	return t.Redirected()
}

func isRedirect(code int) bool {
	switch code {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	}
	return false
}

// traceBodyBytes is how much of each hop's body is drained so the connection can be reused.
const traceBodyBytes = 64 << 10

// Trace GETs rawURL without following redirects automatically, then follows
// them itself (each hop re-checked by the guard), at most MaxRedirects. A
// transport failure is recorded in Trace.Error, not returned; only a bad
// starting URL is an error.
func (f *Fetcher) Trace(ctx context.Context, rawURL string) (*Trace, error) {
	u, err := normalizeURL(rawURL, f.anyPort)
	if err != nil {
		return nil, &FetchError{Reason: "bad_url", Err: err}
	}
	ctx, cancel := context.WithTimeout(ctx, f.cfg.Timeout)
	defer cancel()
	client := &http.Client{
		Transport:     f.transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	t := &Trace{URL: u, Hops: []Hop{}}
	next := u
	for i := 0; ; i++ {
		if i > f.cfg.MaxRedirects {
			t.Error = "redirects"
			return t, nil
		}
		hop, err := f.hop(ctx, client, next)
		if err != nil {
			t.Error = err.Reason
			return t, nil
		}
		t.Hops = append(t.Hops, hop)
		t.FinalURL, t.FinalStatus = hop.URL, hop.Status
		if hop.Location == "" {
			return t, nil
		}
		next = hop.Location
	}
}

func (f *Fetcher) hop(ctx context.Context, client *http.Client, rawURL string) (Hop, *FetchError) {
	pu, err := url.Parse(rawURL)
	if err != nil {
		return Hop{}, &FetchError{Reason: "bad_url", Err: err}
	}
	if err := checkURL(pu, f.anyPort); err != nil {
		return Hop{}, &FetchError{Reason: "bad_url", Err: err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return Hop{}, &FetchError{Reason: "bad_url", Err: err}
	}
	req.Header.Set("User-Agent", f.cfg.UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	resp, err := client.Do(req)
	if err != nil {
		return Hop{}, classify(ctx, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, traceBodyBytes))
	h := Hop{URL: rawURL, Status: resp.StatusCode}
	if isRedirect(resp.StatusCode) {
		loc := strings.TrimSpace(resp.Header.Get("Location"))
		if loc == "" {
			return h, nil
		}
		target, err := pu.Parse(loc)
		if err != nil {
			return Hop{}, &FetchError{Reason: "bad_url", Err: fmt.Errorf("bad Location %q: %w", loc, err)}
		}
		h.Location = target.String()
	}
	return h, nil
}
