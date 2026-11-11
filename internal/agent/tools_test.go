// SPDX-License-Identifier: AGPL-3.0-or-later
package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/cloud"
	"github.com/sitemoment/sitemoment-desktop/internal/rank"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
	"github.com/sitemoment/sitemoment-desktop/internal/webdata"
)

func siteEnv(t *testing.T, d *Deps) *Env {
	t.Helper()
	if d.Store == nil {
		d.Store = openStore(t)
	}
	site, err := d.Store.AddSite(ctx, "example.com", 1)
	ok(t, err)
	d.Now = func() time.Time { return time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC) }
	return &Env{Deps: d, Site: site}
}

func TestGSCQueryTool(t *testing.T) {
	env := siteEnv(t, &Deps{})
	if _, err := runGSCQuery(ctx, env, map[string]any{}); err == nil || !strings.Contains(err.Error(), "还没有 Search Console 数据") {
		t.Fatalf("no data: %v", err)
	}
	ok(t, env.Store.ReplaceGSC(ctx, env.Site.ID, "date", "2026-10-01", "2026-11-08", nil, 1))
	ok(t, env.Store.ReplaceGSC(ctx, env.Site.ID, "query", "2026-10-10", "2026-10-10", []store.GSCRow{{Day: "2026-10-10", Query: "thumbnail maker", Clicks: 2, Impressions: 40, Position: 14}}, 1))
	ok(t, env.Store.ReplaceGSC(ctx, env.Site.ID, "query", "2026-11-01", "2026-11-01", []store.GSCRow{{Day: "2026-11-01", Query: "thumbnail maker", Clicks: 8, Impressions: 100, Position: 11.84}}, 1))
	res, err := runGSCQuery(ctx, env, map[string]any{"days": 28.0, "compare": true})
	ok(t, err)
	var out struct {
		From, To, PrevFrom, PrevTo string
		Rows                       []struct {
			Key          string
			Position     float64
			PrevPosition *float64
		}
	}
	ok(t, json.Unmarshal([]byte(res.Text), &out))
	if out.From != "2026-10-12" || out.To != "2026-11-08" || out.PrevFrom != "2026-09-14" || out.PrevTo != "2026-10-11" {
		t.Fatalf("window %+v", out)
	}
	if len(out.Rows) != 1 || out.Rows[0].Position != 11.8 || out.Rows[0].PrevPosition == nil || *out.Rows[0].PrevPosition != 14 {
		t.Fatalf("rows %+v", out.Rows)
	}
	if res.Card == nil || len(res.Card.Rows) != 1 || !strings.Contains(res.Card.Note, "2026-10-12 至 2026-11-08") {
		t.Fatalf("card %+v", res.Card)
	}
	if _, err := runGSCQuery(ctx, env, map[string]any{"dimension": "country"}); !errors.Is(err, store.ErrBadQuery) {
		t.Fatalf("bad dimension: %v", err)
	}
	if _, err := runGSCQuery(ctx, &Env{Deps: env.Deps}, map[string]any{}); err == nil {
		t.Fatal("ran without a site")
	}
}

func TestWindowRejectsFromOnlyFutureDatesAndTooLongRanges(t *testing.T) {
	env := siteEnv(t, &Deps{})
	ok(t, env.Store.ReplaceGSC(ctx, env.Site.ID, "date", "2026-10-01", "2026-11-08", nil, 1))
	// env.Now (set by siteEnv) is 2026-11-10 09:00 UTC.
	if _, err := runGSCQuery(ctx, env, map[string]any{"from": "2026-10-01"}); err == nil {
		t.Fatal("accepted from without to")
	}
	if _, err := runGSCQuery(ctx, env, map[string]any{"to": "2026-11-08"}); err == nil {
		t.Fatal("accepted to without from")
	}
	if _, err := runGSCQuery(ctx, env, map[string]any{"from": "2026-10-01", "to": "2026-12-31"}); err == nil {
		t.Fatal("accepted a to date in the future")
	}
	if _, err := runGSCQuery(ctx, env, map[string]any{"from": "2024-01-01", "to": "2026-11-08"}); err == nil {
		t.Fatal("accepted a range over 480 days")
	}
	if _, err := runGSCQuery(ctx, env, map[string]any{"from": "2026-11-01", "to": "2026-11-08"}); err != nil {
		t.Fatalf("rejected a normal explicit range: %v", err)
	}
}

func TestKeywordsTool(t *testing.T) {
	env := siteEnv(t, &Deps{})
	kw, err := env.Store.AddKeyword(ctx, env.Site.ID, "AI Thumbnail", store.KindTarget, 1)
	ok(t, err)
	_, err = env.Store.SetKeywordMetric(ctx, kw.ID, "volume", 1900, "sitemoment", time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC).Unix())
	ok(t, err)
	_, err = env.Store.AddKeyword(ctx, env.Site.ID, "logo maker", store.KindScene, 1)
	ok(t, err)
	res, err := runKeywords(ctx, env, map[string]any{"contains": "thumb"})
	ok(t, err)
	if !strings.Contains(res.Text, `"keyword":"ai thumbnail"`) || !strings.Contains(res.Text, `"date":"2026-11-09"`) || strings.Contains(res.Text, "logo") {
		t.Fatalf("text %s", res.Text)
	}
}

func TestSuggestToolCaches(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprintf(w, `[%q,["thumbnail maker free","thumbnail maker ai"]]`, r.URL.Query().Get("q"))
	}))
	defer srv.Close()
	env := siteEnv(t, &Deps{Suggest: webdata.NewSuggest(srv.URL)})
	for i, wantCached := range []bool{false, true} {
		res, err := runSuggest(ctx, env, map[string]any{"seed": "Thumbnail  Maker"})
		ok(t, err)
		if res.Cached != wantCached || !strings.Contains(res.Text, "thumbnail maker ai") || len(res.Card.Items) != 2 {
			t.Fatalf("call %d: %+v", i, res)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("%d upstream calls", hits.Load())
	}
}

func TestFetchPageAndPlaybookTools(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head><title>Thumbnail Maker</title></head><body><h1>Make thumbnails</h1><p>Free tool.</p></body></html>`)
	}))
	defer srv.Close()
	d := &Deps{Fetcher: rank.NewFetcher(rank.FetcherConfig{AllowPrivate: true})}
	env := siteEnv(t, d)
	res, err := runFetchPage(ctx, env, map[string]any{"url": srv.URL})
	ok(t, err)
	if !strings.Contains(res.Text, `"title":"Thumbnail Maker"`) || res.Card == nil {
		t.Fatalf("fetch_page %s", res.Text)
	}
	names := map[string]bool{}
	for _, tool := range webTools(d) {
		names[tool.Name] = true
	}
	if !names["site_crawl"] || !names["fetch_page"] || names["page_speed"] || names["lost_urls"] || !names["load_playbook"] {
		t.Fatalf("web tools %v", names)
	}
	res, err = runPlaybook(ctx, env, map[string]any{"topic": "keywords"})
	ok(t, err)
	if !strings.Contains(res.Text, "选词与数据口径") {
		t.Fatal("playbook text")
	}
}

func TestCollectorTools(t *testing.T) {
	col := &fakeCollectors{}
	env := siteEnv(t, &Deps{Collectors: col})
	tools := collectorTools(col.Tools())
	if len(tools) != 2 || tools[0].Name != "google_serp" || tools[1].Name != "people_also_ask" || tools[0].Kind != KindCollector {
		t.Fatalf("tools %+v", tools)
	}
	res, err := tools[1].Run(ctx, env, map[string]any{"q": "thumbnail maker"})
	ok(t, err)
	if !strings.Contains(res.Text, "Is it free?") || !strings.Contains(res.Text, "United States") || len(res.Card.Items) != 2 {
		t.Fatalf("paa %s", res.Text)
	}
	if len(col.calls) != 1 || col.calls[0]["q"] != "thumbnail maker" {
		t.Fatalf("collector input %v", col.calls)
	}
	col.err = fmt.Errorf("collect: %w", ErrCollectorHuman)
	_, err = tools[0].Run(ctx, env, map[string]any{"q": "x"})
	ui, model := describeError(err)
	if !strings.Contains(ui, "人工验证") || !strings.Contains(model, "谷歌搜索结果（付费）") {
		t.Fatalf("describe %q %q", ui, model)
	}
}

func TestCloudTools(t *testing.T) {
	fc := &fakeCloud{signedIn: true, quote: 3, charge: 1.5}
	env := siteEnv(t, &Deps{Cloud: fc})
	tools := cloudTools(ctx, env.Deps)
	byName := map[string]*Tool{}
	for _, tool := range tools {
		byName[tool.Name] = tool
		if !tool.Paid || tool.Kind != KindCloud || strings.Contains(tool.Description, "web.cafe") {
			t.Errorf("tool %+v", tool)
		}
	}
	if len(tools) != 2 || byName["lookup_keywords"] == nil || byName["paid_serp"] == nil || byName["paid_serp"].Label != "谷歌搜索结果（付费）" {
		t.Fatalf("tools %v", byName)
	}
	max, cached, err := byName["lookup_keywords"].Quote(ctx, env, map[string]any{"keywords": []any{"a"}})
	if err != nil || max != 3 || cached {
		t.Fatalf("quote %v %v %v", max, cached, err)
	}
	res, err := byName["paid_serp"].Run(ctx, env, map[string]any{"q": "a"})
	ok(t, err)
	if res.Credits != 1.5 || !strings.Contains(res.Card.Note, "扣 1.5 积分") || fc.data[0] != "google_serp" {
		t.Fatalf("run %+v %v", res, fc.data)
	}
	fc.err = &cloud.CreditsError{Need: 3, Used: 199, Limit: 200, Month: "2026-11"}
	_, err = byName["lookup_keywords"].Run(ctx, env, map[string]any{"keywords": []any{"a"}})
	if ui, _ := describeError(err); ui != "本月 SiteMoment 额度不够（这次最多要 3 积分，已用 199 / 200）" {
		t.Fatalf("credits error %q", ui)
	}
}
