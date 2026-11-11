// SPDX-License-Identifier: AGPL-3.0-or-later
package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
)

// ErrDataUnavailable means SiteMoment cannot sell this data right now (the
// source is off, its budget is used up, or the upstream failed).
var ErrDataUnavailable = errors.New("cloud: SiteMoment data is not available right now")

var toolName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// Tool is one paid data tool in the catalog.
type Tool struct {
	Name       string   `json:"name"`
	Label      string   `json:"label"`
	MaxCredits float64  `json:"maxCredits"`
	TTLDays    int      `json:"ttlDays"`
	Required   []string `json:"required"`
	ListParam  string   `json:"listParam"`
	MaxItems   int      `json:"maxItems"`
	Enabled    bool     `json:"enabled"`
}

// Quote is what a data call would cost at most (0 when cached).
type Quote struct {
	Tool       string  `json:"tool"`
	Label      string  `json:"label"`
	MaxCredits float64 `json:"maxCredits"`
	Cached     bool    `json:"cached"`
}

// DataResult is a paid data answer; Credits is what it cost.
type DataResult struct {
	Data      json.RawMessage `json:"data"`
	Credits   float64         `json:"credits"`
	Cached    bool            `json:"cached"`
	Source    string          `json:"source"`
	FetchedAt int64           `json:"fetched_at"`
}

// CreditsError means this month's credits cannot cover the call's worst case.
type CreditsError struct {
	Need  float64 `json:"need"`
	Used  float64 `json:"used"`
	Limit float64 `json:"limit"`
	Month string  `json:"month"`
}

func (e *CreditsError) Error() string {
	return fmt.Sprintf("cloud: not enough credits: need up to %g, %g of %g used in %s", e.Need, e.Used, e.Limit, e.Month)
}

// Catalog lists the paid data tools and their worst-case prices.
func (c *Client) Catalog(ctx context.Context) ([]Tool, error) {
	var out struct {
		Tools []Tool `json:"tools"`
	}
	err := c.authed(ctx, http.MethodGet, "/v1/desktop/data/catalog", nil, &out)
	return out.Tools, err
}

// Quote prices a data call without spending anything.
func (c *Client) Quote(ctx context.Context, tool string, params map[string]any) (Quote, error) {
	var q Quote
	err := c.dataCall(ctx, tool, "?quote=1", params, &q)
	return q, err
}

// Data buys a data call from the month's credits (cache hits are free).
func (c *Client) Data(ctx context.Context, tool string, params map[string]any) (DataResult, error) {
	var r DataResult
	err := c.dataCall(ctx, tool, "", params, &r)
	return r, err
}

func (c *Client) dataCall(ctx context.Context, tool, query string, params map[string]any, out any) error {
	if !toolName.MatchString(tool) {
		return fmt.Errorf("cloud: bad data tool name %q", tool)
	}
	if params == nil {
		params = map[string]any{}
	}
	err := c.authed(ctx, http.MethodPost, "/v1/desktop/data/"+tool+query, params, out)
	var ae *apiError
	if !errors.As(err, &ae) {
		return err
	}
	switch ae.Code {
	case "credits_exhausted":
		ce := &CreditsError{}
		json.Unmarshal(ae.raw, ce)
		return ce
	case "data_unavailable", "data_budget", "upstream_error", "upstream_rate_limited":
		return fmt.Errorf("%w (%s)", ErrDataUnavailable, ae.Code)
	}
	return err
}
