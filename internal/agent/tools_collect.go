// SPDX-License-Identifier: AGPL-3.0-or-later
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// serpCollector is the built-in Google results collector; people_also_ask reads its output.
const serpCollector = "google-serp"

// collectorTools turns enabled collectors into tools, plus people_also_ask
// when the Google results collector is there.
func collectorTools(list []CollectorTool) []*Tool {
	var out []*Tool
	for _, ct := range list {
		ct := ct
		out = append(out, &Tool{
			Name: ct.Tool, Label: ct.Name, Kind: KindCollector,
			Description: "免费，在本机隐藏的浏览器窗口里运行（每次间隔 10 秒以上，每天有上限，结果会缓存）。" + ct.Description,
			Params:      ct.Params,
			Subject:     firstStringArg,
			Run: func(ctx context.Context, env *Env, args map[string]any) (*Result, error) {
				return runCollector(ctx, env, ct, args)
			},
		})
		if ct.ID == serpCollector {
			out = append(out, &Tool{
				Name: "people_also_ask", Label: "People Also Ask", Kind: KindCollector,
				Description: "免费，本机浏览器。一个词在谷歌第一页的 People Also Ask 问题和相关搜索，用来找场景词、用户关心的问题。和「" + ct.Name + "」共用同一次查询和缓存。",
				Params:      schema(`{"type":"object","properties":{"q":{"type":"string","description":"英文搜索词"},"gl":{"type":"string","description":"国家，默认 us"}},"required":["q"]}`),
				Subject:     subject("q"),
				Run: func(ctx context.Context, env *Env, args map[string]any) (*Result, error) {
					return runPAA(ctx, env, ct.ID, args)
				},
			})
		}
	}
	return out
}

// firstStringArg is the subject of a collector call: its q, keyword or first string arg.
func firstStringArg(args map[string]any) string {
	for _, k := range []string{"q", "keyword", "query", "url"} {
		if s := argString(args, k); s != "" {
			return s
		}
	}
	for _, v := range args {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func collectorSource(name string, r CollectorResult) string {
	s := fmt.Sprintf("本机浏览器采集（%s），取于 %s", name, unixDate(r.FetchedAt))
	if r.Cached {
		s += "，缓存"
	}
	return s
}

// serpData is the part of google-serp's output the cards use.
type serpData struct {
	Location string `json:"location"`
	Organic  []struct {
		Rank   int    `json:"rank"`
		Domain string `json:"domain"`
		Title  string `json:"title"`
	} `json:"organic"`
	Related []string `json:"related"`
	PAA     []string `json:"paa"`
}

func runCollector(ctx context.Context, env *Env, ct CollectorTool, args map[string]any) (*Result, error) {
	r, err := env.Collectors.Run(ctx, ct.ID, args)
	if err != nil {
		return nil, err
	}
	source := collectorSource(ct.Name, r)
	var card *Card
	if ct.ID == serpCollector {
		var d serpData
		_ = json.Unmarshal(r.Data, &d)
		card = &Card{Type: "table", Columns: []any{"排名", "域名", "标题"}}
		for _, o := range d.Organic {
			card.Rows = append(card.Rows, []any{o.Rank, o.Domain, o.Title})
		}
		loc := d.Location
		if loc == "" {
			loc = "未知地区"
		}
		card.Note = fmt.Sprintf("谷歌（%s）· %s", loc, source)
		if len(d.Organic) == 0 {
			card.Note += "；没有自然结果"
		}
	} else {
		var data any
		_ = json.Unmarshal(r.Data, &data)
		if card = autoCard("", data); card != nil {
			card.Note = source
		}
	}
	text := capText(fmt.Sprintf(`{"source":%q,"data":%s}`, source, string(r.Data)), maxToolText)
	return &Result{Text: text, Card: card, Cached: r.Cached}, nil
}

func runPAA(ctx context.Context, env *Env, id string, args map[string]any) (*Result, error) {
	q := argString(args, "q")
	if q == "" {
		return nil, fmt.Errorf("q is required")
	}
	in := map[string]any{"q": q}
	if gl := argString(args, "gl"); gl != "" {
		in["gl"] = gl
	}
	r, err := env.Collectors.Run(ctx, id, in)
	if err != nil {
		return nil, err
	}
	var d serpData
	_ = json.Unmarshal(r.Data, &d)
	source := collectorSource("谷歌搜索结果", r)
	card := &Card{Type: "lines", Title: "People Also Ask：" + q}
	for _, p := range d.PAA {
		card.Items = append(card.Items, Line{S: "ok", T: p})
	}
	for _, rel := range d.Related {
		card.Items = append(card.Items, Line{S: "ok", T: "相关搜索：" + rel})
	}
	card.Note = fmt.Sprintf("谷歌（%s）· %s", d.Location, source)
	out := map[string]any{"source": source, "location": d.Location, "q": q, "paa": d.PAA, "related": d.Related}
	if len(d.PAA) == 0 {
		out["note"] = "这一页没有 People Also Ask，只有相关搜索"
	}
	return &Result{Text: jsonText(out), Card: card, Cached: r.Cached}, nil
}
