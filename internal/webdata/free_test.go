// SPDX-License-Identifier: AGPL-3.0-or-later

package webdata

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func query(k, v string) func(*http.Request, []byte) bool {
	return func(r *http.Request, _ []byte) bool { return r.URL.Query().Get(k) == v }
}

func TestWaybackListsDistinctHTMLPages(t *testing.T) {
	m := newMock(t)
	m.on("GET", "/cdx/search/cdx", 200, []any{
		[]any{"original", "timestamp"},
		[]any{"http://heartie.net:80/faq/", "20250410120000"},
		[]any{"https://www.heartie.net/faq/", "20250501000000"},
		[]any{"https://heartie.net/blog/best-ai-companion-apps-2026/", "20260102000000"},
		[]any{"https://heartie.net/logo.png", "20250410120000"},
		[]any{"https://heartie.net/robots.txt", "20250410120000"},
		[]any{"http://heartie.net/?ts=abc", "20220115060423"},
		[]any{"https://heartie.net/cdn-cgi/scripts/x/email-decode.min", "20260612000849"},
	}, nil, all(query("url", "heartie.net/*"), query("collapse", "urlkey"), header("User-Agent", "SiteMomentBot/1.0 (+https://sitemoment.com/rank)")))
	got, err := NewWayback(m.URL).URLs(bg, "heartie.net", 100)
	if err != nil {
		t.Fatal(err)
	}
	want := []ArchivedURL{
		{URL: "http://heartie.net/faq/", FirstSeen: "2025-04-10"},
		{URL: "https://heartie.net/blog/best-ai-companion-apps-2026/", FirstSeen: "2026-01-02"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestWaybackEmptyResponse(t *testing.T) {
	m := newMock(t)
	m.on("GET", "/cdx/search/cdx", 200, []any{}, nil, nil)
	got, err := NewWayback(m.URL).URLs(bg, "new.example", 10)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestPSIReadsLabAndFieldData(t *testing.T) {
	m := newMock(t)
	m.on("GET", "/run", 200, map[string]any{
		"lighthouseResult": map[string]any{
			"categories": map[string]any{"performance": map[string]any{"score": 0.87}},
			"audits": map[string]any{
				"largest-contentful-paint": map[string]any{"numericValue": 2412.3456},
				"cumulative-layout-shift":  map[string]any{"numericValue": 0.0213},
				"total-blocking-time":      map[string]any{"numericValue": 120},
			},
		},
		"loadingExperience": map[string]any{
			"overall_category": "AVERAGE",
			"metrics": map[string]any{
				"LARGEST_CONTENTFUL_PAINT_MS":   map[string]any{"percentile": 2600, "category": "AVERAGE"},
				"CUMULATIVE_LAYOUT_SHIFT_SCORE": map[string]any{"percentile": 5, "category": "FAST"},
			},
		},
	}, nil, all(query("strategy", "mobile"), query("key", "k1"), query("url", "https://heartie.net/")))
	r, err := NewPSI(m.URL+"/run", "k1").Run(bg, "https://heartie.net/", "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Performance == nil || *r.Performance != 87 || r.LCPms != 2412.346 || r.TBTms != 120 || r.FieldOverall != "AVERAGE" ||
		r.Field["LCP"] != (FieldMetric{2600, "AVERAGE"}) || r.Field["CLS"] != (FieldMetric{0.05, "FAST"}) {
		t.Fatalf("result = %+v", r)
	}
}

func TestPSIErrorHidesKey(t *testing.T) {
	m := newMock(t)
	m.on("GET", "/run", 400, `{"error":{"message":"bad url"}}`, nil, nil)
	_, err := NewPSI(m.URL+"/run", "secret-key").Run(bg, "nope", "desktop")
	if err == nil || strings.Contains(err.Error(), "secret-key") {
		t.Fatalf("err = %v", err)
	}
}

func TestSuggestExpandDedupsAcrossLetters(t *testing.T) {
	m := newMock(t)
	m.on("GET", "/s", 200, []any{"ai companion", []any{"ai companion app", "AI Companion Free"}}, nil, query("q", "ai companion"))
	m.on("GET", "/s", 200, []any{"ai companion a", []any{"ai companion app", "ai companion apk"}}, nil, query("q", "ai companion a"))
	m.on("GET", "/s", 200, []any{"x", []any{}}, nil, nil)
	s := NewSuggest(m.URL + "/s")
	s.Gap = 0
	got, err := s.Expand(bg, "ai companion", "en", "us", true)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"ai companion app", "AI Companion Free", "ai companion apk"}) {
		t.Fatalf("got %v", got)
	}
}

func TestSuggestRateLimitKeepsPartialResults(t *testing.T) {
	m := newMock(t)
	m.on("GET", "/s", 200, []any{"seo", []any{"seo tools"}}, nil, query("q", "seo"))
	m.on("GET", "/s", 429, nil, nil, nil)
	s := NewSuggest(m.URL + "/s")
	s.Gap = 0
	got, err := s.Expand(bg, "seo", "", "", true)
	if k, ok := kindOf(err); !ok || k != KindRateLimited || !reflect.DeepEqual(got, []string{"seo tools"}) {
		t.Fatalf("got %v, %v", got, err)
	}
}
