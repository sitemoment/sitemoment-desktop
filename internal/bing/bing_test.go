// SPDX-License-Identifier: AGPL-3.0-or-later
package bing

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

var ctx = context.Background()

func noSleep(context.Context, time.Duration) error { return nil }

// server answers each Bing method from bodies; it checks the API key.
func server(t *testing.T, status int, bodies map[string]string, seen *[]string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Query().Get("apikey") != "k1" {
			t.Errorf("request %s %s", r.Method, r.URL)
		}
		method := strings.TrimPrefix(r.URL.Path, "/webmaster/api.svc/json/")
		if seen != nil {
			*seen = append(*seen, method+"?"+r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		w.Write([]byte(bodies[method]))
	}))
	t.Cleanup(srv.Close)
	return &Client{HTTP: srv.Client(), Base: srv.URL + "/webmaster/api.svc/json", sleep: noSleep}
}

func TestParseDate(t *testing.T) {
	for in, want := range map[string]string{
		"/Date(1786665600000)/":      "2026-08-14",
		"/Date(1316156400000-0700)/": "2011-09-16",
		"/Date(1786737600000+0800)/": "2026-08-15",
	} {
		if got, err := ParseDate(in); err != nil || got != want {
			t.Errorf("ParseDate(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "2026-08-14", "/Date(abc)/"} {
		if _, err := ParseDate(in); err == nil {
			t.Errorf("ParseDate(%q) accepted", in)
		}
	}
}

func TestSitesTrafficAndStats(t *testing.T) {
	var seen []string
	c := server(t, 200, map[string]string{
		"GetUserSites": `{"d":[{"__type":"Site:#Microsoft.Bing.Webmaster.Api","AuthenticationCode":"x","DnsVerificationCode":"y","IsVerified":true,"Url":"https://www.example.com/"},
			{"__type":"Site:#Microsoft.Bing.Webmaster.Api","IsVerified":false,"Url":"https://blog.example.com/"}]}`,
		"GetRankAndTrafficStats": `{"d":[{"__type":"RankAndTrafficStats:#Microsoft.Bing.Webmaster.Api","Clicks":3,"Date":"/Date(1786665600000)/","Impressions":40},
			{"__type":"RankAndTrafficStats:#Microsoft.Bing.Webmaster.Api","Clicks":0,"Date":"/Date(1786752000000)/","Impressions":12}]}`,
		"GetQueryStats": `{"d":[{"__type":"QueryStats:#Microsoft.Bing.Webmaster.Api","AvgClickPosition":-1,"AvgImpressionPosition":7,"Clicks":2,"Date":"/Date(1786665600000)/","Impressions":30,"Query":"thumbnail maker"}]}`,
		"GetPageStats":  `{"d":[{"__type":"QueryStats:#Microsoft.Bing.Webmaster.Api","AvgClickPosition":-1,"AvgImpressionPosition":-1,"Clicks":1,"Date":"/Date(1786665600000)/","Impressions":9,"Query":"https://www.example.com/"}]}`,
	}, &seen)

	sites, err := c.Sites(ctx, "k1")
	if err != nil || !reflect.DeepEqual(sites, []Site{{URL: "https://blog.example.com/"}, {URL: "https://www.example.com/", Verified: true}}) {
		t.Fatalf("Sites = %+v, %v", sites, err)
	}
	traffic, err := c.Traffic(ctx, "k1", "https://www.example.com/")
	if err != nil || !reflect.DeepEqual(traffic, []Traffic{{Day: "2026-08-14", Clicks: 3, Impressions: 40}, {Day: "2026-08-15", Impressions: 12}}) {
		t.Fatalf("Traffic = %+v, %v", traffic, err)
	}
	queries, err := c.Queries(ctx, "k1", "https://www.example.com/")
	if err != nil || !reflect.DeepEqual(queries, []Stat{{Week: "2026-08-14", Key: "thumbnail maker", Clicks: 2, Impressions: 30, Position: 7}}) {
		t.Fatalf("Queries = %+v, %v", queries, err)
	}
	pages, err := c.Pages(ctx, "k1", "https://www.example.com/")
	if err != nil || len(pages) != 1 || pages[0].Key != "https://www.example.com/" || pages[0].Position != 0 {
		t.Fatalf("Pages = %+v, %v", pages, err)
	}
	if !strings.Contains(seen[1], "siteUrl=https%3A%2F%2Fwww.example.com%2F") {
		t.Fatalf("traffic request = %s", seen[1])
	}
}

func TestKeywordStats(t *testing.T) {
	var seen []string
	c := server(t, 200, map[string]string{
		"GetKeywordStats": `{"d":[
			{"__type":"KeywordStats:#Microsoft.Bing.Webmaster.Api","BroadImpressions":90,"Date":"/Date(1786665600000)/","Impressions":40,"Query":"dog beds"},
			{"__type":"KeywordStats:#Microsoft.Bing.Webmaster.Api","BroadImpressions":80,"Date":"/Date(1786060800000)/","Impressions":35,"Query":"dog beds"}]}`,
	}, &seen)
	weeks, err := c.KeywordStats(ctx, "k1", "dog beds", "us", "en-US")
	if err != nil || len(weeks) != 2 || weeks[0].Week != "2026-08-07" || weeks[1].Impressions != 40 || weeks[1].BroadImpressions != 90 {
		t.Fatalf("KeywordStats = %+v, %v (oldest first)", weeks, err)
	}
	if !strings.Contains(seen[0], "q=dog+beds") || !strings.Contains(seen[0], "country=us") || !strings.Contains(seen[0], "language=en-US") {
		t.Fatalf("request = %s", seen[0])
	}
}

func TestEmptyAnswers(t *testing.T) {
	for _, body := range []string{``, `{"d":null}`, `{"d":[]}`} {
		c := server(t, 200, map[string]string{"GetKeywordStats": body}, nil)
		if weeks, err := c.KeywordStats(ctx, "k1", "x", "us", "en-US"); err != nil || len(weeks) != 0 {
			t.Errorf("body %q: %+v, %v", body, weeks, err)
		}
	}
}

func TestErrors(t *testing.T) {
	c := server(t, 400, map[string]string{"GetUserSites": `{"ErrorCode":3,"Message":"InvalidApiKey"}`}, nil)
	if _, err := c.Sites(ctx, "k1"); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("invalid key: %v", err)
	}
	c = server(t, 400, map[string]string{"GetQueryStats": `{"ErrorCode":14,"Message":"ERROR!!! NotAuthorized"}`}, nil)
	if _, err := c.Queries(ctx, "k1", "https://x.example.com/"); !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("not authorized: %v", err)
	}
	var seen []string
	c = server(t, 400, map[string]string{"GetUserSites": `{"ErrorCode":4,"Message":"ThrottleUser"}`}, &seen)
	if _, err := c.Sites(ctx, "k1"); !errors.Is(err, ErrThrottled) || len(seen) != maxAttempts {
		t.Fatalf("throttled: %v after %d attempts", err, len(seen))
	}
}

type failTransport struct{}

func (failTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("connection reset")
}

func TestTransportErrorsDoNotLeakTheKey(t *testing.T) {
	c := &Client{HTTP: &http.Client{Transport: failTransport{}}, sleep: noSleep}
	_, err := c.Sites(ctx, "super-secret-key")
	if err == nil || strings.Contains(err.Error(), "super-secret-key") || !strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("err = %v", err)
	}
}
