// SPDX-License-Identifier: AGPL-3.0-or-later

package webdata

import (
	"context"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultPSIURL is Google's PageSpeed Insights API (free; a key raises the quota).
const DefaultPSIURL = "https://www.googleapis.com/pagespeedonline/v5/runPagespeed"

// PSI runs Lighthouse through PageSpeed Insights and reads Chrome UX Report
// field data from the same response.
type PSI struct {
	http    *http.Client
	baseURL string
	key     string
}

// NewPSI builds the client; key may be empty.
func NewPSI(baseURL, key string) *PSI {
	return &PSI{http: newClient(90 * time.Second), baseURL: baseURL, key: key}
}

// FieldMetric is one CrUX metric at the 75th percentile.
type FieldMetric struct {
	P75 float64 `json:"p75"`
	// Category is FAST, AVERAGE or SLOW.
	Category string `json:"category"`
}

// PSIResult is the part of a PageSpeed report the agent uses.
type PSIResult struct {
	URL      string `json:"url"`
	Strategy string `json:"strategy"`
	// Performance is the Lighthouse score 0–100 (nil when Lighthouse failed).
	Performance *int    `json:"performance"`
	LCPms       float64 `json:"lcpMs"`
	CLS         float64 `json:"cls"`
	TBTms       float64 `json:"tbtMs"`
	FCPms       float64 `json:"fcpMs"`
	// Field is real-user data (LCP, INP, CLS, FCP, TTFB); empty when the page has too little traffic.
	Field        map[string]FieldMetric `json:"field"`
	FieldOverall string                 `json:"fieldOverall,omitempty"`
}

var cruxNames = map[string]string{
	"LARGEST_CONTENTFUL_PAINT_MS":     "LCP",
	"INTERACTION_TO_NEXT_PAINT":       "INP",
	"CUMULATIVE_LAYOUT_SHIFT_SCORE":   "CLS",
	"FIRST_CONTENTFUL_PAINT_MS":       "FCP",
	"EXPERIMENTAL_TIME_TO_FIRST_BYTE": "TTFB",
}

// Run tests pageURL with strategy "mobile" (default) or "desktop".
func (p *PSI) Run(ctx context.Context, pageURL, strategy string) (*PSIResult, error) {
	if strategy != "desktop" {
		strategy = "mobile"
	}
	q := url.Values{}
	q.Set("url", pageURL)
	q.Set("strategy", strategy)
	q.Set("category", "performance")
	if p.key != "" {
		q.Set("key", p.key)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(p.baseURL, "?")+"?"+q.Encode(), nil)
	if err != nil {
		return nil, HTTPError(err.Error())
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, HTTPError(strings.ReplaceAll(err.Error(), p.key, "***"))
	}
	defer resp.Body.Close()
	body := readBody(resp.Body)
	switch {
	case resp.StatusCode == 429:
		return nil, RateLimited(retryAfter(resp.Header, 60, retryAfterCapSecs))
	case !success(resp.StatusCode):
		return nil, HTTPError(statusText(resp.StatusCode) + ": " + truncate(string(body), errorBodyMaxChars))
	}
	v, err := decodeJSON(body)
	if err != nil {
		return nil, DecodeError(err.Error())
	}
	out := &PSIResult{URL: pageURL, Strategy: strategy, Field: map[string]FieldMetric{}}
	lh := get(v, "lighthouseResult")
	if s, ok := asF64(path(lh, "categories", "performance", "score")); ok {
		n := int(math.Round(s * 100))
		out.Performance = &n
	}
	audit := func(id string) float64 {
		f, _ := asF64(path(lh, "audits", id, "numericValue"))
		return math.Round(f*1000) / 1000
	}
	out.LCPms, out.CLS, out.TBTms, out.FCPms = audit("largest-contentful-paint"), audit("cumulative-layout-shift"),
		audit("total-blocking-time"), audit("first-contentful-paint")
	le := get(v, "loadingExperience")
	if m, ok := get(le, "metrics").(map[string]any); ok {
		for k, short := range cruxNames {
			node := m[k]
			pct, ok := asF64(get(node, "percentile"))
			if !ok {
				continue
			}
			if short == "CLS" {
				pct /= 100 // CrUX reports CLS × 100
			}
			cat, _ := asStr(get(node, "category"))
			out.Field[short] = FieldMetric{P75: pct, Category: cat}
		}
	}
	out.FieldOverall, _ = asStr(get(le, "overall_category"))
	return out, nil
}
