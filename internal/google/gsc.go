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
)

const gscBase = "https://searchconsole.googleapis.com/webmasters/v3"

// maxRows caps one query; GSC itself stops at 50,000 rows per day.
const maxRows = 100000

var (
	// ErrRateLimited means Search Console asked us to slow down.
	ErrRateLimited = errors.New("google: Search Console rate limit, try again later")
	// ErrTruncated means a query returned more than maxRows rows.
	ErrTruncated = errors.New("google: too many Search Console rows, result truncated")
)

// GSC is a Search Console API client.
type GSC struct {
	HTTP     *http.Client
	Base     string // empty: Google's
	RowLimit int    // rows per page; 0: 25,000
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
	var rows []Row
	for start := 0; ; {
		body := map[string]any{
			"startDate": q.Start, "endDate": q.End, "dimensions": q.Dimensions, "type": "web",
			"rowLimit": limit, "startRow": start, "dataState": "all",
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

func (g *GSC) call(ctx context.Context, method, endpoint, token string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := g.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 64<<20))
	if err != nil {
		return err
	}
	switch {
	case res.StatusCode == http.StatusUnauthorized:
		return ErrReauth
	case res.StatusCode == http.StatusTooManyRequests:
		return ErrRateLimited
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
