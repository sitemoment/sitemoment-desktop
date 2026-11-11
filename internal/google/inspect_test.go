// SPDX-License-Identifier: AGPL-3.0-or-later
package google

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

// inspectReply is a URL Inspection answer as documented
// (developers.google.com/webmaster-tools/v1/urlInspection.index/UrlInspectionResult).
const inspectReply = `{"inspectionResult":{
  "inspectionResultLink":"https://search.google.com/search-console/inspect?resource_id=sc-domain:example.com&id=x",
  "indexStatusResult":{
    "sitemap":["https://example.com/sitemap.xml"],
    "verdict":"NEUTRAL",
    "coverageState":"Crawled - currently not indexed",
    "robotsTxtState":"ALLOWED",
    "indexingState":"INDEXING_ALLOWED",
    "lastCrawlTime":"2026-11-02T10:11:12Z",
    "pageFetchState":"SUCCESSFUL",
    "googleCanonical":"https://example.com/a",
    "userCanonical":"https://example.com/a",
    "crawledAs":"MOBILE"}}}`

func TestInspect(t *testing.T) {
	g := &GSC{HTTP: client(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.String() != "https://searchconsole.googleapis.com/v1/urlInspection/index:inspect" {
			t.Errorf("request %s %s", r.Method, r.URL)
		}
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["inspectionUrl"] != "https://example.com/a" || body["siteUrl"] != "sc-domain:example.com" {
			t.Errorf("body %v", body)
		}
		return reply(200, inspectReply)
	})}
	got, err := g.Inspect(ctx, "tok", "sc-domain:example.com", "https://example.com/a")
	if err != nil {
		t.Fatal(err)
	}
	if got.Verdict != "NEUTRAL" || got.CoverageState != "Crawled - currently not indexed" || got.Indexed() ||
		got.LastCrawlTime != "2026-11-02T10:11:12Z" || got.Link == "" {
		t.Fatalf("Inspect = %+v", got)
	}
}

func TestInspectQuotaIsRateLimited(t *testing.T) {
	g := &GSC{sleep: noSleep, HTTP: client(func(*http.Request) (*http.Response, error) {
		return reply(429, `{"error":{"code":429,"message":"Quota exceeded","status":"RESOURCE_EXHAUSTED"}}`)
	})}
	if _, err := g.Inspect(ctx, "tok", "sc-domain:example.com", "https://example.com/a"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
}
