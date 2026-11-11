// SPDX-License-Identifier: AGPL-3.0-or-later
package google

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"testing"
)

func TestGAPropertiesPaginatesAndSorts(t *testing.T) {
	var tokens []string
	g := &GA{HTTP: client(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "analyticsadmin.googleapis.com" || r.URL.Path != "/v1beta/accountSummaries" || r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("request %s %s", r.URL, r.Header.Get("Authorization"))
		}
		if r.URL.Query().Get("pageSize") != "200" {
			t.Errorf("pageSize = %q", r.URL.Query().Get("pageSize"))
		}
		tokens = append(tokens, r.URL.Query().Get("pageToken"))
		if r.URL.Query().Get("pageToken") == "" {
			return reply(200, `{"accountSummaries":[{"account":"accounts/2","displayName":"Zeta","propertySummaries":[
				{"property":"properties/22","displayName":"zeta.com"}]}],"nextPageToken":"p2"}`)
		}
		return reply(200, `{"accountSummaries":[{"account":"accounts/1","displayName":"Alpha","propertySummaries":[
			{"property":"properties/12","displayName":"b.example.com"},{"property":"properties/11","displayName":"a.example.com"}]},
			{"account":"accounts/3","displayName":"Empty"}]}`)
	})}
	got, err := g.Properties(ctx, "tok")
	if err != nil {
		t.Fatal(err)
	}
	want := []GAProperty{
		{Property: "properties/11", DisplayName: "a.example.com", Account: "accounts/1", AccountName: "Alpha"},
		{Property: "properties/12", DisplayName: "b.example.com", Account: "accounts/1", AccountName: "Alpha"},
		{Property: "properties/22", DisplayName: "zeta.com", Account: "accounts/2", AccountName: "Zeta"},
	}
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(tokens, []string{"", "p2"}) {
		t.Fatalf("Properties = %+v (page tokens %v)", got, tokens)
	}
}

func TestGAReportPaginates(t *testing.T) {
	var offsets []float64
	g := &GA{RowLimit: 2, HTTP: client(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "analyticsdata.googleapis.com" || r.URL.Path != "/v1beta/properties/123:runReport" || r.Method != http.MethodPost {
			t.Errorf("request %s %s", r.Method, r.URL)
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		dr := body["dateRanges"].([]any)[0].(map[string]any)
		dims := body["dimensions"].([]any)
		if dr["startDate"] != "2026-11-01" || dr["endDate"] != "2026-11-02" || len(dims) != 2 ||
			dims[1].(map[string]any)["name"] != "landingPage" || body["limit"] != float64(2) {
			t.Errorf("body %v", body)
		}
		offsets = append(offsets, body["offset"].(float64))
		if body["offset"] == float64(0) {
			return reply(200, `{"rows":[
				{"dimensionValues":[{"value":"20261101"},{"value":"/"}],"metricValues":[{"value":"10"},{"value":"8"},{"value":"6"},{"value":"1"}]},
				{"dimensionValues":[{"value":"20261101"},{"value":"/a"}],"metricValues":[{"value":"3"},{"value":"3"},{"value":"2"},{"value":"0"}]}],
				"rowCount":3}`)
		}
		return reply(200, `{"rows":[{"dimensionValues":[{"value":"20261102"},{"value":"/"}],"metricValues":[{"value":"7.5"},{"value":"7"},{"value":"5"},{"value":"0"}]}],"rowCount":3}`)
	})}
	rows, err := g.Report(ctx, "tok", "properties/123", GAReport{Start: "2026-11-01", End: "2026-11-02",
		Dimensions: []string{"date", "landingPage"}, Metrics: []string{"sessions", "totalUsers", "engagedSessions", "keyEvents"}})
	if err != nil || len(rows) != 3 || rows[2].Dims[0] != "20261102" || rows[2].Metrics[0] != 7.5 || rows[0].Metrics[3] != 1 {
		t.Fatalf("Report = %+v, %v", rows, err)
	}
	if !reflect.DeepEqual(offsets, []float64{0, 2}) {
		t.Fatalf("offsets = %v", offsets)
	}
}

func TestGAReportEmptyAndErrors(t *testing.T) {
	g := &GA{HTTP: client(func(*http.Request) (*http.Response, error) { return reply(200, `{"kind":"analyticsData#runReport"}`) })}
	if rows, err := g.Report(ctx, "tok", "properties/1", GAReport{Start: "2026-11-01", End: "2026-11-01", Dimensions: []string{"date"}, Metrics: []string{"sessions"}}); err != nil || len(rows) != 0 {
		t.Fatalf("empty report = %+v, %v", rows, err)
	}
	if _, err := g.Report(ctx, "tok", "properties/1/x", GAReport{}); err == nil {
		t.Fatal("bad property accepted")
	}
	for code, body := range map[int]string{
		403: `{"error":{"code":403,"message":"Request had insufficient authentication scopes.","status":"PERMISSION_DENIED"}}`,
		401: `{"error":{"message":"x"}}`,
		429: `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED"}}`,
	} {
		want := map[int]error{403: ErrGAScope, 401: ErrReauth, 429: ErrRateLimited}[code]
		g := &GA{sleep: noSleep, HTTP: client(func(*http.Request) (*http.Response, error) { return reply(code, body) })}
		if _, err := g.Properties(ctx, "tok"); !errors.Is(err, want) {
			t.Errorf("HTTP %d: %v, want %v", code, err, want)
		}
	}
	g = &GA{HTTP: client(func(*http.Request) (*http.Response, error) {
		return reply(200, `{"rows":[{"dimensionValues":[{"value":"20261101"}],"metricValues":[{"value":"NaN?"}]}],"rowCount":1}`)
	})}
	if _, err := g.Report(ctx, "tok", "properties/1", GAReport{Dimensions: []string{"date"}, Metrics: []string{"sessions"}}); err == nil {
		t.Fatal("bad metric value accepted")
	}
}
