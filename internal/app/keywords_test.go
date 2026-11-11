// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/bing"
	"github.com/sitemoment/sitemoment-desktop/internal/cloud"
	"github.com/sitemoment/sitemoment-desktop/internal/secret"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

// fakeCloud answers lookup_keywords with volume = 100 x the keyword's
// length, KD 20 and CPC "0.5" for every keyword except "ghost".
type fakeCloud struct {
	batches [][]string
	err     error
}

func (f *fakeCloud) words(params map[string]any) []string { return params["keywords"].([]string) }

func (f *fakeCloud) Quote(_ context.Context, tool string, params map[string]any) (cloud.Quote, error) {
	if f.err != nil {
		return cloud.Quote{}, f.err
	}
	return cloud.Quote{Tool: tool, Label: "关键词库存数据", MaxCredits: 1.5}, nil
}

func (f *fakeCloud) Data(_ context.Context, tool string, params map[string]any) (cloud.DataResult, error) {
	if f.err != nil {
		return cloud.DataResult{}, f.err
	}
	words := f.words(params)
	f.batches = append(f.batches, words)
	var items []map[string]any
	for _, w := range words {
		if w != "ghost" {
			items = append(items, map[string]any{"keyword": w, "volume": 100 * len(w), "kd": 20, "cpc": "0.5"})
		}
	}
	raw, _ := json.Marshal(map[string]any{"items": items})
	return cloud.DataResult{Data: raw, Credits: 1.5, FetchedAt: 1790000000}, nil
}

// fakeBingStats has six weeks of history for "ai chat" only.
type fakeBingStats struct{ keys []string }

func (f *fakeBingStats) KeywordStats(_ context.Context, key, q, country, language string) ([]bing.KeywordWeek, error) {
	f.keys = append(f.keys, key)
	if q != "ai chat" || country != "us" || language != "en-US" {
		return nil, nil
	}
	var out []bing.KeywordWeek
	for i := 1; i <= 6; i++ {
		out = append(out, bing.KeywordWeek{Week: "2026-10-0" + strconv.Itoa(i), Impressions: float64(10 * i)})
	}
	return out, nil
}

func keywordService(t *testing.T) (*KeywordService, *store.Store, store.Site, *fakeCloud, *fakeBingStats) {
	t.Helper()
	st, sec := openStore(t), secret.NewMemory()
	site, _ := st.AddSite(ctx, "example.com", 1)
	sec.Set("bing:bing-1234", "k1")
	st.UpsertConnection(ctx, "bing", "bing-1234", 1)
	fc, fb := &fakeCloud{}, &fakeBingStats{}
	k := &KeywordService{Store: st, Cloud: fc, Bing: fb, BingKeys: &BingKeys{Secrets: sec}, BingGap: time.Millisecond,
		Now: func() time.Time { return time.Unix(1790000500, 0) }}
	return k, st, site, fc, fb
}

func ids(list []store.Keyword) []int64 {
	out := make([]int64, len(list))
	for i, k := range list {
		out[i] = k.ID
	}
	return out
}

func TestKeywordListAddsFoundWords(t *testing.T) {
	k, st, site, _, _ := keywordService(t)
	if l, err := k.List(ctx, site.ID, ""); err != nil || l.To != "" || len(l.Keywords) != 0 {
		t.Fatalf("before any sync = %+v, %v", l, err)
	}
	st.ReplaceGSC(ctx, site.ID, "query", "2026-11-08", "2026-11-08", []store.GSCRow{
		{Day: "2026-11-08", Query: "ai chat", Clicks: 3, Impressions: 30, Position: 8},
	}, 1)
	if n, err := k.Add(ctx, site.ID, "Logo Maker\nthumbnail maker， banner maker,\n\n", store.KindTarget); err != nil || n != 3 {
		t.Fatalf("Add = %d, %v", n, err)
	}
	if _, err := k.Add(ctx, site.ID, " \n ", store.KindTarget); err == nil {
		t.Fatal("empty add accepted")
	}
	l, err := k.List(ctx, site.ID, "")
	if err != nil || l.From != "2026-10-12" || l.To != "2026-11-08" || len(l.Keywords) != 4 {
		t.Fatalf("List = %+v, %v", l, err)
	}
	last := l.Keywords[3]
	if last.Keyword != "ai chat" || last.Kind != store.KindFound || last.Position == nil || *last.Position != 8 {
		t.Fatalf("found word = %+v", last)
	}
	if l, _ := k.List(ctx, site.ID, store.KindTarget); len(l.Keywords) != 3 {
		t.Fatalf("targets = %d", len(l.Keywords))
	}
}

func TestKeywordFetchBingThenCloud(t *testing.T) {
	k, _, site, fc, fb := keywordService(t)
	k.Add(ctx, site.ID, "ai chat\nghost", store.KindTarget)
	l, _ := k.List(ctx, site.ID, "")

	res, err := k.FetchBing(ctx, site.ID, ids(l.Keywords))
	if err != nil || res.Updated != 1 || res.Skipped != 1 || res.Source != "bing" {
		t.Fatalf("FetchBing = %+v, %v", res, err)
	}
	if len(fb.keys) != 2 || fb.keys[0] != "k1" {
		t.Fatalf("bing keys used = %v", fb.keys)
	}
	l, _ = k.List(ctx, site.ID, "")
	ai := l.Keywords[0]
	if ai.Keyword != "ai chat" || ai.Volume.Value == nil || *ai.Volume.Value != 30+40+50+60 || ai.Volume.Source != "bing" {
		t.Fatalf("after Bing = %+v", ai)
	}

	q, err := k.QuoteCloud(ctx, site.ID, ids(l.Keywords))
	if err != nil || q.MaxCredits != 1.5 || q.Keywords != 2 || q.Label != "关键词库存数据" {
		t.Fatalf("QuoteCloud = %+v, %v", q, err)
	}
	res, err = k.FetchCloud(ctx, site.ID, ids(l.Keywords))
	if err != nil || res.Updated != 1 || res.Skipped != 1 || res.Credits != 1.5 || res.Source != "sitemoment" {
		t.Fatalf("FetchCloud = %+v, %v", res, err)
	}
	l, _ = k.List(ctx, site.ID, "")
	ai = l.Keywords[0]
	if *ai.Volume.Value != 700 || ai.Volume.Source != "sitemoment" || ai.Volume.At != 1790000000 || *ai.KD.Value != 20 || *ai.CPC.Value != 0.5 {
		t.Fatalf("after SiteMoment = %+v", ai)
	}
	// Bing never replaces SiteMoment numbers.
	if res, _ := k.FetchBing(ctx, site.ID, []int64{ai.ID}); res.Updated != 0 || res.Skipped != 1 {
		t.Fatalf("Bing over SiteMoment = %+v", res)
	}
	if len(fc.batches) != 1 {
		t.Fatalf("batches = %v", fc.batches)
	}
}

func TestKeywordCloudBatchesAndErrors(t *testing.T) {
	k, st, site, fc, _ := keywordService(t)
	var lines []string
	for i := 0; i < 150; i++ {
		lines = append(lines, "word "+strconv.Itoa(i))
	}
	k.Add(ctx, site.ID, strings.Join(lines, "\n"), store.KindScene)
	l, _ := k.List(ctx, site.ID, "")
	q, err := k.QuoteCloud(ctx, site.ID, ids(l.Keywords))
	if err != nil || q.MaxCredits != 3 || q.Keywords != 150 {
		t.Fatalf("QuoteCloud = %+v, %v", q, err)
	}
	res, err := k.FetchCloud(ctx, site.ID, ids(l.Keywords))
	if err != nil || res.Updated != 150 || res.Credits != 3 || len(fc.batches) != 2 || len(fc.batches[0]) != 100 {
		t.Fatalf("FetchCloud = %+v, %v (batches %d)", res, err, len(fc.batches))
	}

	other, _ := st.AddSite(ctx, "other.example.com", 1)
	if _, err := k.QuoteCloud(ctx, other.ID, ids(l.Keywords)); err == nil {
		t.Fatal("another site's keywords were accepted")
	}
	if _, err := k.FetchBing(ctx, site.ID, nil); err == nil {
		t.Fatal("empty selection accepted")
	}
	fc.err = &cloud.CreditsError{Need: 1.5, Used: 199, Limit: 200, Month: "2026-11"}
	if _, err := k.FetchCloud(ctx, site.ID, ids(l.Keywords)[:1]); err == nil || !strings.Contains(err.Error(), "额度不够") || !strings.Contains(err.Error(), "199 / 200") {
		t.Fatalf("credits error = %v", err)
	}
	fc.err = cloud.ErrSignedOut
	if _, err := k.QuoteCloud(ctx, site.ID, ids(l.Keywords)[:1]); err == nil || !strings.Contains(err.Error(), "登录") {
		t.Fatalf("signed out = %v", err)
	}
	if _, err := k.FetchCloud(ctx, site.ID, ids(l.Keywords)[:1]); err == nil || !strings.Contains(err.Error(), "登录") {
		t.Fatalf("FetchCloud signed out = %v", err)
	}
}

// TestKeywordChosenDedupesBeforeTheLimit covers chosen() collapsing
// duplicate ids before checking maxChosen: a selection that repeats one
// keyword many times must not be rejected as "too many".
func TestKeywordChosenDedupesBeforeTheLimit(t *testing.T) {
	k, _, site, _, _ := keywordService(t)
	k.Add(ctx, site.ID, "ai chat", store.KindTarget)
	l, _ := k.List(ctx, site.ID, "")
	id := l.Keywords[0].ID
	many := make([]int64, maxChosen+50)
	for i := range many {
		many[i] = id
	}
	q, err := k.QuoteCloud(ctx, site.ID, many)
	if err != nil || q.Keywords != 1 {
		t.Fatalf("QuoteCloud with a repeated id = %+v, %v", q, err)
	}
}

func TestKeywordFetchBingNeedsAKey(t *testing.T) {
	k, st, site, _, _ := keywordService(t)
	st.DeleteConnection(ctx, "bing", "bing-1234")
	k.Add(ctx, site.ID, "ai chat", store.KindTarget)
	l, _ := k.List(ctx, site.ID, "")
	if _, err := k.FetchBing(ctx, site.ID, ids(l.Keywords)); err != ErrNoBingKey {
		t.Fatalf("err = %v, want ErrNoBingKey", err)
	}
}
