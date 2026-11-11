// SPDX-License-Identifier: AGPL-3.0-or-later

package webdata

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultSuggestURL is Google's autocomplete endpoint. It is unofficial and
// undocumented: use it gently (the expander spaces requests) and expect it to
// change or rate-limit without notice.
const DefaultSuggestURL = "https://suggestqueries.google.com/complete/search"

// Suggest reads Google autocomplete: the free way to find long-tail queries.
type Suggest struct {
	http    *http.Client
	baseURL string
	// Gap spaces requests during Expand.
	Gap time.Duration
}

// NewSuggest builds the client.
func NewSuggest(baseURL string, opts ...Option) *Suggest {
	return &Suggest{http: newClient(15*time.Second, opts...), baseURL: baseURL, Gap: 400 * time.Millisecond}
}

// Complete returns the suggestions for q in language hl and country gl.
func (s *Suggest) Complete(ctx context.Context, q, hl, gl string) ([]string, error) {
	v := url.Values{}
	v.Set("client", "firefox")
	v.Set("q", q)
	if hl != "" {
		v.Set("hl", hl)
	}
	if gl != "" {
		v.Set("gl", gl)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+"?"+v.Encode(), nil)
	if err != nil {
		return nil, HTTPError(err.Error())
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; SiteMomentBot/1.0; +https://sitemoment.com/rank)")
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, HTTPError(err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode == 429 || resp.StatusCode == 403 {
		return nil, RateLimited(retryAfter(resp.Header, 300, retryAfterCapSecs))
	}
	if !success(resp.StatusCode) {
		return nil, HTTPError(statusText(resp.StatusCode))
	}
	parsed, err := decodeJSON(readBody(resp.Body))
	if err != nil {
		return nil, DecodeError(err.Error())
	}
	list, ok := asArr(at(parsed, 1))
	if !ok {
		return nil, DecodeError("autocomplete response has no suggestion list")
	}
	out := []string{}
	for _, it := range list {
		if str, ok := asStr(it); ok && strings.TrimSpace(str) != "" {
			out = append(out, strings.TrimSpace(str))
		}
	}
	return out, nil
}

// Expand asks for seed and, with alphabet, "seed a" … "seed z", returning the
// distinct suggestions in first-seen order. It stops at the first rate limit
// and returns what it has with that error.
func (s *Suggest) Expand(ctx context.Context, seed, hl, gl string, alphabet bool) ([]string, error) {
	queries := []string{seed}
	if alphabet {
		for c := 'a'; c <= 'z'; c++ {
			queries = append(queries, seed+" "+string(c))
		}
	}
	seen := map[string]bool{}
	out := []string{}
	for i, q := range queries {
		if i > 0 && s.Gap > 0 {
			select {
			case <-ctx.Done():
				return out, ctx.Err()
			case <-time.After(s.Gap):
			}
		}
		got, err := s.Complete(ctx, q, hl, gl)
		if err != nil {
			if len(out) > 0 {
				return out, err
			}
			return nil, err
		}
		for _, g := range got {
			k := strings.ToLower(g)
			if !seen[k] {
				seen[k] = true
				out = append(out, g)
			}
		}
	}
	return out, nil
}
