// SPDX-License-Identifier: AGPL-3.0-or-later
package google

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"
)

const gscBase = "https://searchconsole.googleapis.com/webmasters/v3"

// maxRows caps one query; GSC itself stops at 50,000 rows per day.
const maxRows = 100000

// maxAttempts is how many times call tries a request that fails with a
// retryable error (429, 5xx, or a network error) before giving up.
const maxAttempts = 4

// baseBackoff and maxBackoff bound the exponential backoff between retries
// when the server does not send a Retry-After header.
const (
	baseBackoff = 2 * time.Second
	maxBackoff  = 30 * time.Second
)

var (
	// ErrRateLimited means Search Console asked us to slow down.
	ErrRateLimited = errors.New("google: Search Console rate limit, try again later")
	// ErrTruncated means a query returned more than maxRows rows.
	ErrTruncated = errors.New("google: too many Search Console rows, result truncated")
	// ErrServerError means Search Console returned a 5xx after every retry.
	ErrServerError = errors.New("google: Search Console server error")
)

// GSC is a Search Console API client.
type GSC struct {
	HTTP     *http.Client
	Base     string // empty: Google's
	RowLimit int    // rows per page; 0: 25,000

	// sleep waits out a retry backoff; nil uses a real, ctx-aware sleep.
	// Tests override it to skip real delays or observe requested durations.
	sleep func(context.Context, time.Duration) error
}

// realSleep waits for d or until ctx is done, whichever comes first.
func realSleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (g *GSC) sleeper() func(context.Context, time.Duration) error {
	if g.sleep != nil {
		return g.sleep
	}
	return realSleep
}

// Property is a Search Console property the account can read.
type Property struct {
	SiteURL    string `json:"siteUrl"`
	Permission string `json:"permissionLevel"`
}

// Query is one Search Analytics request (web search only).
type Query struct {
	Start, End string // YYYY-MM-DD, inclusive
	Dimensions []string
}

// Row is one Search Analytics row; Keys follow the query's Dimensions.
type Row struct {
	Keys        []string `json:"keys"`
	Clicks      float64  `json:"clicks"`
	Impressions float64  `json:"impressions"`
	CTR         float64  `json:"ctr"`
	Position    float64  `json:"position"`
}

func (g *GSC) base() string {
	if g.Base != "" {
		return g.Base
	}
	return gscBase
}

// Properties lists the verified properties, sorted by URL.
func (g *GSC) Properties(ctx context.Context, token string) ([]Property, error) {
	var out struct {
		SiteEntry []Property `json:"siteEntry"`
	}
	if err := g.call(ctx, http.MethodGet, g.base()+"/sites", token, nil, &out); err != nil {
		return nil, err
	}
	props := []Property{}
	for _, p := range out.SiteEntry {
		if p.Permission != "siteUnverifiedUser" {
			props = append(props, p)
		}
	}
	sort.Slice(props, func(i, j int) bool { return props[i].SiteURL < props[j].SiteURL })
	return props, nil
}

// Query runs a Search Analytics query, following pages until a short page.
func (g *GSC) Query(ctx context.Context, token, property string, q Query) ([]Row, error) {
	limit := g.RowLimit
	if limit <= 0 {
		limit = 25000
	}
	// QueryEscape, not PathEscape: GSC rejects an unescaped ":" in "sc-domain:".
	endpoint := g.base() + "/sites/" + url.QueryEscape(property) + "/searchAnalytics/query"
	// byPage when the "page" dimension is requested, else byProperty, so
	// totals match what the Search Console UI shows for the same query.
	aggregationType := "byProperty"
	for _, d := range q.Dimensions {
		if d == "page" {
			aggregationType = "byPage"
			break
		}
	}
	var rows []Row
	for start := 0; ; {
		body := map[string]any{
			"startDate": q.Start, "endDate": q.End, "dimensions": q.Dimensions, "type": "web",
			"rowLimit": limit, "startRow": start, "dataState": "all", "aggregationType": aggregationType,
		}
		var page struct {
			Rows []Row `json:"rows"`
		}
		if err := g.call(ctx, http.MethodPost, endpoint, token, body, &page); err != nil {
			return nil, err
		}
		rows = append(rows, page.Rows...)
		if len(page.Rows) < limit {
			return rows, nil
		}
		if len(rows) >= maxRows {
			return rows, ErrTruncated
		}
		start += len(page.Rows)
	}
}

// backoff picks how long to wait before the next attempt (1-based: the
// delay before the 2nd attempt is backoff(1, 0)). retryAfter, when > 0,
// takes precedence over the exponential schedule, but is still clamped to
// maxBackoff: a server-supplied (or bogus) Retry-After must not be able to
// park a sync slot for an arbitrarily long time.
func backoff(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		if retryAfter > maxBackoff {
			return maxBackoff
		}
		return retryAfter
	}
	d := baseBackoff
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= maxBackoff {
			return maxBackoff
		}
	}
	return d
}

// retryAfterHeader parses a Retry-After response header given in seconds;
// anything else (missing, "0", non-numeric, HTTP-date) is ignored, falling
// back to the exponential schedule.
func retryAfterHeader(h http.Header) time.Duration {
	s := h.Get("Retry-After")
	if s == "" {
		return 0
	}
	secs, err := strconv.Atoi(s)
	if err != nil || secs <= 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}

func (g *GSC) call(ctx context.Context, method, endpoint, token string, body, out any) error {
	var raw []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		raw = b
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		var rd io.Reader
		if raw != nil {
			rd = bytes.NewReader(raw) // fresh reader per attempt: body must be re-sent on retry
		}
		req, err := http.NewRequestWithContext(ctx, method, endpoint, rd)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		if raw != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		res, err := g.HTTP.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			lastErr = err
			if attempt == maxAttempts {
				return err
			}
			if serr := g.sleeper()(ctx, backoff(attempt, 0)); serr != nil {
				return serr
			}
			continue
		}

		data, err := io.ReadAll(io.LimitReader(res.Body, 64<<20))
		res.Body.Close()
		if err != nil {
			return err
		}

		switch {
		case res.StatusCode == http.StatusUnauthorized:
			return ErrReauth
		case res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500:
			var e struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			json.Unmarshal(data, &e)
			if res.StatusCode == http.StatusTooManyRequests {
				lastErr = ErrRateLimited
			} else {
				lastErr = fmt.Errorf("google: Search Console HTTP %d: %s: %w", res.StatusCode, e.Error.Message, ErrServerError)
			}
			if attempt == maxAttempts {
				return lastErr
			}
			if serr := g.sleeper()(ctx, backoff(attempt, retryAfterHeader(res.Header))); serr != nil {
				return serr
			}
			continue
		case res.StatusCode == http.StatusForbidden:
			return errors.New("google: this account cannot read that Search Console property")
		case res.StatusCode >= 400:
			var e struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			json.Unmarshal(data, &e)
			return fmt.Errorf("google: Search Console HTTP %d: %s", res.StatusCode, e.Error.Message)
		}
		return json.Unmarshal(data, out)
	}
	return lastErr
}
