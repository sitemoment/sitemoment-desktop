// SPDX-License-Identifier: AGPL-3.0-or-later
package google

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"time"
)

const gscBase = "https://searchconsole.googleapis.com/webmasters/v3"

// maxRows caps one query or report; GSC itself stops at 50,000 rows per day.
const maxRows = 100000

// ErrTruncated means a query returned more than maxRows rows.
var ErrTruncated = errors.New("google: too many rows, result truncated")

// GSC is a Search Console API client.
type GSC struct {
	HTTP     *http.Client
	Base     string // empty: Google's
	RowLimit int    // rows per page; 0: 25,000

	// sleep waits out a retry backoff; nil uses a real, ctx-aware sleep.
	// Tests override it to skip real delays or observe requested durations.
	sleep func(context.Context, time.Duration) error
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

func gscForbidden(string) error {
	return errors.New("google: this account cannot read that Search Console property")
}

func (g *GSC) call(ctx context.Context, method, endpoint, token string, body, out any) error {
	return callJSON(ctx, caller{http: g.HTTP, sleep: g.sleeper(), product: "Search Console", forbidden: gscForbidden}, method, endpoint, token, body, out)
}
