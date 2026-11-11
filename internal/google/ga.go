// SPDX-License-Identifier: AGPL-3.0-or-later
package google

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	gaAdminBase = "https://analyticsadmin.googleapis.com/v1beta"
	gaDataBase  = "https://analyticsdata.googleapis.com/v1beta"
)

// ErrGAScope means the account was connected without Google Analytics access.
var ErrGAScope = errors.New("google: Analytics access was not granted, connect the account again and allow it")

// GA is a Google Analytics 4 client: the Admin API for the property list and
// the Data API for reports.
type GA struct {
	HTTP      *http.Client
	AdminBase string // empty: Google's
	DataBase  string // empty: Google's
	RowLimit  int    // rows per page; 0: 100,000 (the API allows 250,000)

	// sleep waits out a retry backoff; nil uses a real, ctx-aware sleep.
	sleep func(context.Context, time.Duration) error
}

// GAProperty is a GA4 property the account can read.
type GAProperty struct {
	Property    string `json:"property"` // "properties/123"
	DisplayName string `json:"displayName"`
	Account     string `json:"account"` // "accounts/9"
	AccountName string `json:"accountName"`
}

// GAReport is one runReport request over [Start, End] (YYYY-MM-DD).
type GAReport struct {
	Start, End string
	Dimensions []string
	Metrics    []string
	// OnPage, when set, is called before every page's HTTP request (most
	// reports are one page; a few are not) and must return a release func
	// to call once that page's request is done. This is where a caller
	// plugs in a ratelimit.Limiter: Acquiring once for the whole Report
	// would under-count a paginated report's real request rate.
	OnPage func(ctx context.Context) (release func(), err error)
}

// GARow is one report row: Dims follow the report's Dimensions (dates as
// YYYYMMDD), Metrics its Metrics.
type GARow struct {
	Dims    []string
	Metrics []float64
}

func def(s, d string) string {
	if s != "" {
		return s
	}
	return d
}

func gaForbidden(msg string) error {
	if strings.Contains(msg, "insufficient authentication scopes") {
		return ErrGAScope
	}
	return errors.New("google: this account cannot read that GA4 property")
}

func (g *GA) caller() caller {
	return caller{http: g.HTTP, sleep: g.sleep, product: "Analytics", forbidden: gaForbidden}
}

// Properties lists every GA4 property of every account the token can read,
// by account name then property name.
func (g *GA) Properties(ctx context.Context, token string) ([]GAProperty, error) {
	out := []GAProperty{}
	for pageToken := ""; ; {
		q := url.Values{"pageSize": {"200"}}
		if pageToken != "" {
			q.Set("pageToken", pageToken)
		}
		var page struct {
			AccountSummaries []struct {
				Account           string `json:"account"`
				DisplayName       string `json:"displayName"`
				PropertySummaries []struct {
					Property    string `json:"property"`
					DisplayName string `json:"displayName"`
				} `json:"propertySummaries"`
			} `json:"accountSummaries"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := callJSON(ctx, g.caller(), http.MethodGet, def(g.AdminBase, gaAdminBase)+"/accountSummaries?"+q.Encode(), token, nil, &page); err != nil {
			return nil, err
		}
		for _, a := range page.AccountSummaries {
			for _, p := range a.PropertySummaries {
				out = append(out, GAProperty{Property: p.Property, DisplayName: p.DisplayName, Account: a.Account, AccountName: a.DisplayName})
			}
		}
		if page.NextPageToken == "" {
			break
		}
		pageToken = page.NextPageToken
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].AccountName != out[j].AccountName {
			return out[i].AccountName < out[j].AccountName
		}
		return out[i].DisplayName < out[j].DisplayName
	})
	return out, nil
}

type gaName struct {
	Name string `json:"name"`
}

func gaNames(in []string) []gaName {
	out := make([]gaName, len(in))
	for i, n := range in {
		out[i] = gaName{Name: n}
	}
	return out
}

// Report runs a report, following offsets until rowCount rows are read.
func (g *GA) Report(ctx context.Context, token, property string, r GAReport) ([]GARow, error) {
	id := strings.TrimPrefix(property, "properties/")
	if id == "" || strings.ContainsAny(id, "/?#") {
		return nil, fmt.Errorf("google: bad GA4 property %q", property)
	}
	limit := g.RowLimit
	if limit <= 0 {
		limit = 100000
	}
	endpoint := def(g.DataBase, gaDataBase) + "/properties/" + id + ":runReport"
	var rows []GARow
	for offset := 0; ; {
		body := map[string]any{
			"dateRanges": []map[string]string{{"startDate": r.Start, "endDate": r.End}},
			"dimensions": gaNames(r.Dimensions), "metrics": gaNames(r.Metrics),
			"limit": limit, "offset": offset, "keepEmptyRows": false,
		}
		var page struct {
			Rows []struct {
				DimensionValues []struct {
					Value string `json:"value"`
				} `json:"dimensionValues"`
				MetricValues []struct {
					Value string `json:"value"`
				} `json:"metricValues"`
			} `json:"rows"`
			RowCount int `json:"rowCount"`
		}
		var release func()
		if r.OnPage != nil {
			rl, err := r.OnPage(ctx)
			if err != nil {
				return nil, err
			}
			release = rl
		}
		err := callJSON(ctx, g.caller(), http.MethodPost, endpoint, token, body, &page)
		if release != nil {
			release()
		}
		if err != nil {
			return nil, err
		}
		for _, pr := range page.Rows {
			row := GARow{Dims: make([]string, len(pr.DimensionValues)), Metrics: make([]float64, len(pr.MetricValues))}
			for i, d := range pr.DimensionValues {
				row.Dims[i] = d.Value
			}
			for i, m := range pr.MetricValues {
				v, err := strconv.ParseFloat(m.Value, 64)
				if err != nil {
					return nil, fmt.Errorf("google: Analytics metric value %q: %w", m.Value, err)
				}
				row.Metrics[i] = v
			}
			rows = append(rows, row)
		}
		offset += len(page.Rows)
		if len(page.Rows) == 0 || offset >= page.RowCount {
			return rows, nil
		}
		if len(rows) >= maxRows {
			return rows, ErrTruncated
		}
	}
}
