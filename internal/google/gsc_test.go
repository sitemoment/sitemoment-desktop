// SPDX-License-Identifier: AGPL-3.0-or-later
package google

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

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

func TestQueryErrors(t *testing.T) {
	for code, want := range map[int]error{401: ErrReauth, 429: ErrRateLimited} {
		g := &GSC{HTTP: client(func(*http.Request) (*http.Response, error) { return reply(code, `{"error":{"message":"x"}}`) })}
		if _, err := g.Query(ctx, "tok", "https://a.example.com/", Query{Start: "2026-11-01", End: "2026-11-01", Dimensions: []string{"date"}}); !errors.Is(err, want) {
			t.Errorf("HTTP %d: %v, want %v", code, err, want)
		}
	}
}
