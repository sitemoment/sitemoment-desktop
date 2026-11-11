// SPDX-License-Identifier: AGPL-3.0-or-later
package agent

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

// windowParams are the date params every local data tool takes.
const windowParams = `"days":{"type":"integer","description":"最近多少天（以本机最新一天为准），7–480，默认 28"},` +
	`"from":{"type":"string","description":"开始日期 YYYY-MM-DD（和 to 一起给时忽略 days）"},"to":{"type":"string","description":"结束日期 YYYY-MM-DD"},` +
	`"contains":{"type":"string","description":"只看包含这段文字的值（查询词、网址、渠道）"},"exclude":{"type":"string","description":"去掉包含这段文字的值"},` +
	`"asc":{"type":"boolean","description":"升序（默认降序；date 维度默认按日期升序）"},"limit":{"type":"integer","description":"最多返回多少行，默认 25，最多 200"},` +
	`"compare":{"type":"boolean","description":"附上前一个同样长度周期里同一个值的数，用来看变化"}`

func localTools() []*Tool {
	return []*Tool{
		{
			Name: "gsc_query", Label: "GSC 数据", Kind: KindLocal,
			Description: "免费，本机数据。站长自己 Google Search Console 的真实点击、展示、CTR、平均排名（网页搜索），按查询词 / 页面 / 日期分组，可筛选、排序、和上一个周期对比。数据比实际晚 2–3 天。查词的表现、掉量原因、机会词（排名 8–20、展示高点击少）时先用它。",
			Params: schema(`{"type":"object","properties":{"dimension":{"type":"string","enum":["query","page","date"],"description":"按什么分组，默认 query"},` +
				`"orderBy":{"type":"string","enum":["clicks","impressions","ctr","position"]},` + windowParams + `}}`),
			Subject: func(a map[string]any) string {
				return orDefault(argString(a, "dimension"), "query") + " " + argString(a, "contains")
			},
			Run: runGSCQuery,
		},
		{
			Name: "ga_query", Label: "GA4 数据", Kind: KindLocal,
			Description: "免费，本机数据。站长自己 GA4 的会话、用户（按天相加）、互动会话、关键事件，按日期 / 着陆页 / 默认渠道组分组。看自然搜索流量占比、哪些着陆页带来转化时用。",
			Params: schema(`{"type":"object","properties":{"dimension":{"type":"string","enum":["landing","channel","date"],"description":"按什么分组，默认 landing"},` +
				`"orderBy":{"type":"string","enum":["sessions","users","engagedSessions","keyEvents"]},` + windowParams + `}}`),
			Subject: func(a map[string]any) string {
				return orDefault(argString(a, "dimension"), "landing") + " " + argString(a, "contains")
			},
			Run: runGAQuery,
		},
		{
			Name: "bing_query", Label: "Bing 数据", Kind: KindLocal,
			Description: "免费，本机数据。站长自己 Bing Webmaster 的点击、展示：按日期（每天）或按查询词 / 页面（Bing 按周汇总，只列头部，加起来不等于全站）。",
			Params: schema(`{"type":"object","properties":{"dimension":{"type":"string","enum":["query","page","date"],"description":"按什么分组，默认 query"},` +
				`"orderBy":{"type":"string","enum":["clicks","impressions","position"]},` + windowParams + `}}`),
			Subject: func(a map[string]any) string {
				return orDefault(argString(a, "dimension"), "query") + " " + argString(a, "contains")
			},
			Run: runBingQuery,
		},
		{
			Name: "keywords", Label: "关键词", Kind: KindLocal,
			Description: "免费，本机数据。关键词工作台里这个站的词：类型（target 目标词 / found 意外流量词 / scene 场景词）、已经取到的搜索量 / KD / CPC（带来源和日期）、Search Console 近 28 天的点击、展示和平均排名。",
			Params: schema(`{"type":"object","properties":{"kind":{"type":"string","enum":["target","found","scene"],"description":"只看一类，默认全部"},` +
				`"contains":{"type":"string"},"limit":{"type":"integer","description":"默认 50，最多 200"}}}`),
			Subject: subject("kind"),
			Run:     runKeywords,
		},
	}
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func round(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}

// window resolves the date params against the newest synced day of a
// source: [from, to] and the same-length period before it.
func window(ctx context.Context, env *Env, source, slice, name string, args map[string]any) (from, to, prevFrom, prevTo string, err error) {
	from, to = argString(args, "from"), argString(args, "to")
	parse := func(s string) (time.Time, error) { return time.Parse(store.DayLayout, s) }
	var f, t time.Time
	switch {
	case from != "" && to != "":
		if f, err = parse(from); err == nil {
			t, err = parse(to)
		}
		if err != nil || t.Before(f) {
			return "", "", "", "", errors.New("from / to 要是 YYYY-MM-DD，且 from 不晚于 to")
		}
		if today := env.now().Format(store.DayLayout); to > today {
			return "", "", "", "", errors.New("to 不能晚于今天")
		}
		if days := int(t.Sub(f).Hours()/24) + 1; days > 480 {
			return "", "", "", "", errors.New("from 到 to 最多 480 天")
		}
	case from != "" || to != "":
		return "", "", "", "", errors.New("from 和 to 要一起给")
	default:
		latest, lerr := env.Store.LatestDay(ctx, env.Site.ID, source, slice)
		if lerr != nil {
			return "", "", "", "", lerr
		}
		if latest == "" {
			return "", "", "", "", fmt.Errorf("这个站点在本机还没有 %s 数据（没有绑定或还没同步完）", name)
		}
		t, _ = parse(latest)
		f = t.AddDate(0, 0, -(argInt(args, "days", 28, 7, 480) - 1))
	}
	n := int(t.Sub(f).Hours()/24) + 1
	pt := f.AddDate(0, 0, -1)
	pf := pt.AddDate(0, 0, -(n - 1))
	return f.Format(store.DayLayout), t.Format(store.DayLayout), pf.Format(store.DayLayout), pt.Format(store.DayLayout), nil
}

func factQuery(env *Env, args map[string]any, dim, from, to string) store.FactQuery {
	return store.FactQuery{SiteID: env.Site.ID, Dim: dim, From: from, To: to, Contains: argString(args, "contains"), Exclude: argString(args, "exclude"),
		OrderBy: argString(args, "orderBy"), Asc: argBool(args, "asc"), Limit: argInt(args, "limit", 25, 1, 200)}
}

func needSite(env *Env) error {
	if env.Site.ID == 0 {
		return errors.New("这个会话没有选站点")
	}
	return nil
}

func runGSCQuery(ctx context.Context, env *Env, args map[string]any) (*Result, error) {
	if err := needSite(env); err != nil {
		return nil, err
	}
	dim := orDefault(argString(args, "dimension"), "query")
	from, to, pf, pt, err := window(ctx, env, "gsc", "date", "Search Console", args)
	if err != nil {
		return nil, err
	}
	rows, err := env.Store.GSCQuery(ctx, factQuery(env, args, dim, from, to))
	if err != nil {
		return nil, err
	}
	compare := argBool(args, "compare")
	prev := map[string]store.GSCStat{}
	if compare {
		q := factQuery(env, args, dim, pf, pt)
		q.Limit = 500
		old, err := env.Store.GSCQuery(ctx, q)
		if err != nil {
			return nil, err
		}
		for _, r := range old {
			prev[r.Key] = r
		}
	}
	type row struct {
		Key          string   `json:"key"`
		Clicks       float64  `json:"clicks"`
		Impressions  float64  `json:"impressions"`
		CTR          float64  `json:"ctr"`
		Position     float64  `json:"position"`
		PrevClicks   *float64 `json:"prevClicks,omitempty"`
		PrevImpr     *float64 `json:"prevImpressions,omitempty"`
		PrevPosition *float64 `json:"prevPosition,omitempty"`
	}
	cols := []any{dim, "点击", "展示", "CTR", "排名"}
	if compare {
		cols = append(cols, "上期排名")
	}
	card := &Card{Type: "table", Columns: cols}
	out := make([]row, 0, len(rows))
	for _, r := range rows {
		o := row{Key: r.Key, Clicks: r.Clicks, Impressions: r.Impressions, CTR: round(r.CTR, 4), Position: round(r.Position, 1)}
		cr := []any{r.Key, r.Clicks, r.Impressions, fmt.Sprintf("%.1f%%", r.CTR*100), round(r.Position, 1)}
		if compare {
			if p, ok := prev[r.Key]; ok {
				c, i, pos := p.Clicks, p.Impressions, round(p.Position, 1)
				o.PrevClicks, o.PrevImpr, o.PrevPosition = &c, &i, &pos
				cr = append(cr, pos)
			} else {
				cr = append(cr, "—")
			}
		}
		out = append(out, o)
		if len(card.Rows) < 20 {
			card.Rows = append(card.Rows, cr)
		}
	}
	card.Note = fmt.Sprintf("Google Search Console · 本机数据 · %s 至 %s", from, to)
	res := map[string]any{"source": "Google Search Console（本机同步，网页搜索）", "from": from, "to": to, "dimension": dim, "rows": out}
	if compare {
		res["prevFrom"], res["prevTo"] = pf, pt
	}
	return &Result{Text: fitRows(res, &out), Card: card}, nil
}

// fitRows marshals res, halving *rows (which res["rows"] holds) until it fits.
func fitRows[T any](res map[string]any, rows *[]T) string {
	return fitJSON(res, func() bool {
		if len(*rows) <= 1 {
			return false
		}
		*rows = (*rows)[:len(*rows)/2]
		res["rows"] = *rows
		res["truncated"] = true
		return true
	})
}

func runGAQuery(ctx context.Context, env *Env, args map[string]any) (*Result, error) {
	if err := needSite(env); err != nil {
		return nil, err
	}
	dim := orDefault(argString(args, "dimension"), "landing")
	from, to, pf, pt, err := window(ctx, env, "ga", "date", "GA4", args)
	if err != nil {
		return nil, err
	}
	rows, err := env.Store.GAQuery(ctx, factQuery(env, args, dim, from, to))
	if err != nil {
		return nil, err
	}
	res := map[string]any{"source": "GA4（本机同步，媒体资源时区）", "from": from, "to": to, "dimension": dim, "rows": rows}
	if argBool(args, "compare") {
		q := factQuery(env, args, dim, pf, pt)
		q.Limit = 500
		old, err := env.Store.GAQuery(ctx, q)
		if err != nil {
			return nil, err
		}
		prev := map[string]float64{}
		for _, r := range old {
			prev[r.Key] = r.Sessions
		}
		res["prevFrom"], res["prevTo"], res["prevSessions"] = pf, pt, prev
	}
	card := &Card{Type: "table", Columns: []any{dim, "会话", "互动会话", "关键事件"}, Note: fmt.Sprintf("GA4 · 本机数据 · %s 至 %s", from, to)}
	for i, r := range rows {
		if i == 20 {
			break
		}
		card.Rows = append(card.Rows, []any{r.Key, r.Sessions, r.EngagedSessions, r.KeyEvents})
	}
	return &Result{Text: fitRows(res, &rows), Card: card}, nil
}

func runBingQuery(ctx context.Context, env *Env, args map[string]any) (*Result, error) {
	if err := needSite(env); err != nil {
		return nil, err
	}
	dim := orDefault(argString(args, "dimension"), "query")
	from, to, pf, pt, err := window(ctx, env, "bing", "traffic", "Bing", args)
	if err != nil {
		return nil, err
	}
	q := factQuery(env, args, dim, from, to)
	if dim != "date" {
		// Weekly rows are keyed by the week's first day: include the week the window starts in.
		f, _ := time.Parse(store.DayLayout, from)
		q.From = f.AddDate(0, 0, -6).Format(store.DayLayout)
	}
	rows, err := env.Store.BingQuery(ctx, q)
	if err != nil {
		return nil, err
	}
	res := map[string]any{"source": "Bing Webmaster（本机同步）", "from": from, "to": to, "dimension": dim, "rows": rows}
	if dim != "date" {
		res["note"] = "Bing 的查询和页面数据按周汇总、只列头部，加起来不等于全站"
	}
	if argBool(args, "compare") {
		pq := factQuery(env, args, dim, pf, pt)
		pq.Limit = 500
		if dim != "date" {
			// Same widening as the current window: include the week the
			// comparison period starts in.
			f, _ := time.Parse(store.DayLayout, pf)
			pq.From = f.AddDate(0, 0, -6).Format(store.DayLayout)
		}
		old, err := env.Store.BingQuery(ctx, pq)
		if err != nil {
			return nil, err
		}
		prev := map[string]float64{}
		for _, r := range old {
			prev[r.Key] = r.Clicks
		}
		res["prevFrom"], res["prevTo"], res["prevClicks"] = pf, pt, prev
	}
	card := &Card{Type: "table", Columns: []any{dim, "点击", "展示", "排名"}, Note: fmt.Sprintf("Bing · 本机数据 · %s 至 %s", from, to)}
	for i, r := range rows {
		if i == 20 {
			break
		}
		pos := any("—")
		if r.Position > 0 {
			pos = round(r.Position, 1)
		}
		card.Rows = append(card.Rows, []any{r.Key, r.Clicks, r.Impressions, pos})
	}
	return &Result{Text: fitRows(res, &rows), Card: card}, nil
}

func runKeywords(ctx context.Context, env *Env, args map[string]any) (*Result, error) {
	if err := needSite(env); err != nil {
		return nil, err
	}
	from, to := "", ""
	if latest, err := env.Store.LatestDay(ctx, env.Site.ID, "gsc", "query"); err == nil && latest != "" {
		t, _ := time.Parse(store.DayLayout, latest)
		from, to = t.AddDate(0, 0, -27).Format(store.DayLayout), latest
	}
	kws, err := env.Store.Keywords(ctx, env.Site.ID, argString(args, "kind"), from, to)
	if err != nil {
		return nil, err
	}
	contains := argString(args, "contains")
	limit := argInt(args, "limit", 50, 1, 200)
	type metric struct {
		Value  *float64 `json:"value"`
		Source string   `json:"source,omitempty"`
		Date   string   `json:"date,omitempty"`
	}
	m := func(x store.Metric) metric {
		out := metric{Value: x.Value, Source: x.Source}
		if x.At > 0 {
			out.Date = unixDate(x.At)
		}
		return out
	}
	type row struct {
		Keyword     string   `json:"keyword"`
		Kind        string   `json:"kind"`
		Volume      metric   `json:"volume"`
		KD          metric   `json:"kd"`
		CPC         metric   `json:"cpc"`
		Clicks      float64  `json:"clicks"`
		Impressions float64  `json:"impressions"`
		Position    *float64 `json:"position"`
		Notes       string   `json:"notes,omitempty"`
	}
	rows := []row{}
	card := &Card{Type: "table", Columns: []any{"关键词", "类型", "搜索量", "KD", "排名"}}
	val := func(p *float64) any {
		if p == nil {
			return "—"
		}
		return round(*p, 1)
	}
	for _, k := range kws {
		if contains != "" && !containsFold(k.Keyword, contains) {
			continue
		}
		if len(rows) == limit {
			break
		}
		rows = append(rows, row{k.Keyword, k.Kind, m(k.Volume), m(k.KD), m(k.CPC), k.Clicks, k.Impressions, k.Position, k.Notes})
		if len(card.Rows) < 20 {
			card.Rows = append(card.Rows, []any{k.Keyword, k.Kind, val(k.Volume.Value), val(k.KD.Value), val(k.Position)})
		}
	}
	card.Note = "关键词工作台（本机）；搜索量来源 bing = Bing 近 4 周精确匹配，sitemoment = SiteMoment 数据"
	res := map[string]any{"source": "关键词工作台（本机）", "gscFrom": from, "gscTo": to, "total": len(kws), "rows": rows}
	return &Result{Text: fitRows(res, &rows), Card: card}, nil
}
