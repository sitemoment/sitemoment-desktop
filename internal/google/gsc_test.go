// SPDX-License-Identifier: AGPL-3.0-or-later
package google

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// noSleep skips the real backoff delay in tests that intentionally exhaust
// retries or just don't care about timing.
func noSleep(context.Context, time.Duration) error { return nil }

func TestProperties(t *testing.T) {
	g := &GSC{HTTP: client(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer tok" || r.URL.Path != "/webmasters/v3/sites" {
			t.Errorf("request %s %s", r.Header.Get("Authorization"), r.URL.Path)
		}
		return reply(200, `{"siteEntry":[
			{"siteUrl":"sc-domain:b.example.com","permissionLevel":"siteOwner"},
			{"siteUrl":"https://a.example.com/","permissionLevel":"siteFullUser"},
			{"siteUrl":"https://c.example.com/","permissionLevel":"siteUnverifiedUser"}]}`)
	})}
	got, err := g.Properties(ctx, "tok")
	if err != nil || len(got) != 2 || got[0].SiteURL != "https://a.example.com/" || got[1].SiteURL != "sc-domain:b.example.com" {
		t.Fatalf("Properties = %+v, %v", got, err)
	}
}

func TestQueryPaginates(t *testing.T) {
	var starts []int
	g := &GSC{RowLimit: 2, HTTP: client(func(r *http.Request) (*http.Response, error) {
		if !strings.Contains(r.URL.EscapedPath(), "/sites/sc-domain%3Aexample.com/searchAnalytics/query") {
			t.Errorf("path %s", r.URL.EscapedPath())
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["startDate"] != "2026-11-01" || body["type"] != "web" || body["rowLimit"] != float64(2) {
			t.Errorf("body %v", body)
		}
		if body["aggregationType"] != "byProperty" {
			t.Errorf("aggregationType = %v, want byProperty", body["aggregationType"])
		}
		start := int(body["startRow"].(float64))
		starts = append(starts, start)
		if start == 0 {
			return reply(200, `{"rows":[{"keys":["2026-11-01","a"],"clicks":3,"impressions":10,"ctr":0.3,"position":4.5},{"keys":["2026-11-01","b"],"clicks":1,"impressions":5,"ctr":0.2,"position":8}]}`)
		}
		return reply(200, `{"rows":[{"keys":["2026-11-01","c"],"clicks":0,"impressions":2,"ctr":0,"position":20}]}`)
	})}
	rows, err := g.Query(ctx, "tok", "sc-domain:example.com", Query{Start: "2026-11-01", End: "2026-11-01", Dimensions: []string{"date", "query"}})
	if err != nil || len(rows) != 3 || rows[2].Keys[1] != "c" || rows[0].Position != 4.5 {
		t.Fatalf("Query = %+v, %v", rows, err)
	}
	if len(starts) != 2 || starts[1] != 2 {
		t.Fatalf("startRows = %v", starts)
	}
}

func TestQueryAggregationTypeByPage(t *testing.T) {
	g := &GSC{HTTP: client(func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["aggregationType"] != "byPage" {
			t.Errorf("aggregationType = %v, want byPage", body["aggregationType"])
		}
		return reply(200, `{"rows":[]}`)
	})}
	if _, err := g.Query(ctx, "tok", "https://a.example.com/", Query{Start: "2026-11-01", End: "2026-11-01", Dimensions: []string{"date", "page"}}); err != nil {
		t.Fatalf("Query = %v", err)
	}
}

func TestQueryErrors(t *testing.T) {
	for code, want := range map[int]error{401: ErrReauth, 429: ErrRateLimited} {
		// sleep: noSleep keeps the 429 case (which now retries) fast.
		g := &GSC{sleep: noSleep, HTTP: client(func(*http.Request) (*http.Response, error) { return reply(code, `{"error":{"message":"x"}}`) })}
		if _, err := g.Query(ctx, "tok", "https://a.example.com/", Query{Start: "2026-11-01", End: "2026-11-01", Dimensions: []string{"date"}}); !errors.Is(err, want) {
			t.Errorf("HTTP %d: %v, want %v", code, err, want)
		}
	}
}

func TestQueryRetries429ThenSucceeds(t *testing.T) {
	var calls int
	g := &GSC{sleep: noSleep, HTTP: client(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls <= 2 {
			return reply(429, `{"error":{"message":"slow down"}}`)
		}
		return reply(200, `{"rows":[{"keys":["2026-11-01"],"clicks":1,"impressions":2,"ctr":0.5,"position":1}]}`)
	})}
	rows, err := g.Query(ctx, "tok", "https://a.example.com/", Query{Start: "2026-11-01", End: "2026-11-01", Dimensions: []string{"date"}})
	if err != nil || len(rows) != 1 || calls != 3 {
		t.Fatalf("Query = %+v, %v, calls=%d, want 1 row, nil err, 3 calls", rows, err, calls)
	}
}

func TestQueryRetries5xxExhausted(t *testing.T) {
	var calls int
	g := &GSC{sleep: noSleep, HTTP: client(func(*http.Request) (*http.Response, error) {
		calls++
		return reply(503, `{"error":{"message":"down"}}`)
	})}
	_, err := g.Query(ctx, "tok", "https://a.example.com/", Query{Start: "2026-11-01", End: "2026-11-01", Dimensions: []string{"date"}})
	if calls != 4 {
		t.Fatalf("calls = %d, want 4", calls)
	}
	if !errors.Is(err, ErrRateLimited) && !errors.Is(err, ErrServerError) {
		t.Fatalf("err = %v, want ErrRateLimited or ErrServerError", err)
	}
}

func TestQueryHonorsRetryAfter(t *testing.T) {
	var slept []time.Duration
	var calls int
	g := &GSC{
		sleep: func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil },
		HTTP: client(func(*http.Request) (*http.Response, error) {
			calls++
			if calls == 1 {
				return &http.Response{StatusCode: 429, Body: io.NopCloser(strings.NewReader(`{}`)),
					Header: http.Header{"Retry-After": {"5"}}}, nil
			}
			return reply(200, `{"rows":[]}`)
		}),
	}
	if _, err := g.Query(ctx, "tok", "https://a.example.com/", Query{Start: "2026-11-01", End: "2026-11-01", Dimensions: []string{"date"}}); err != nil {
		t.Fatalf("Query = %v", err)
	}
	if len(slept) != 1 || slept[0] != 5*time.Second {
		t.Fatalf("slept = %v, want [5s]", slept)
	}
}

func TestQueryCtxCancelDuringBackoff(t *testing.T) {
	g := &GSC{
		sleep: func(c context.Context, _ time.Duration) error { return c.Err() },
		HTTP:  client(func(*http.Request) (*http.Response, error) { return reply(429, `{}`) }),
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := g.Query(cctx, "tok", "https://a.example.com/", Query{Start: "2026-11-01", End: "2026-11-01", Dimensions: []string{"date"}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
