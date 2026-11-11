// SPDX-License-Identifier: AGPL-3.0-or-later
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/cloud"
)

// catalogTTL is how long the paid data catalog is reused.
const catalogTTL = 10 * time.Minute

// catalogFetchTimeout bounds a catalog fetch so a slow network can't block
// the caller (Start/Resume build the toolset synchronously): on timeout the
// turn just runs without paid tools instead of hanging.
const catalogFetchTimeout = 2 * time.Second

// paidSpec describes one SiteMoment data tool to the model.
type paidSpec struct {
	name        string // agent tool name
	cloudTool   string // /v1/desktop/data/:tool
	label       string // used when the catalog has none
	description string
	params      string
	subject     string
}

// paidSpecs are ported from the backend agent (internal/seoagent/tools_paid.go).
// web.cafe's own "serp" is left out: paid_serp gives the same page for less.
var paidSpecs = []paidSpec{
	{"lookup_keywords", "lookup_keywords", "关键词库存数据", "关键词库存数据（只读库，50 个词 1.5 积分）：搜索量、逐月、CPC、意图、预筛难度、哥飞版 KD。查搜索量时先用它；found=false 的词再用 keyword_volume。",
		`{"type":"object","properties":{"keywords":{"type":"array","items":{"type":"string"},"description":"1–100 个英文词"},"gl":{"type":"string"}},"required":["keywords"]}`, "keywords"},
	{"keyword_volume", "keyword_volume", "搜索量核实", "搜索量（实时，2–5 积分/次）：Google Ads 关键字规划师口径的月均搜索量和近 12 个月曲线。只对库存数据里没有的词用。",
		`{"type":"object","properties":{"keywords":{"type":"array","items":{"type":"string"},"description":"1–50 个英文词"},"gl":{"type":"string"}},"required":["keywords"]}`, "keywords"},
	{"keyword_difficulty", "keyword_difficulty", "关键词难度", "关键词难度（2 积分）：哥飞版 KD 0–100、判断原因、月搜索量、进前十的链接预算、前十竞争盘面。只查真正的主攻候选词。",
		`{"type":"object","properties":{"keyword":{"type":"string"},"gl":{"type":"string"}},"required":["keyword"]}`, "keyword"},
	{"serp_review", "serp_review", "SERP 解密", "SERP 解密（2 积分）：逐位点评一个词谷歌第一页 10 个结果，谁是薄弱占位、哪里可超越。",
		`{"type":"object","properties":{"keyword":{"type":"string"},"gl":{"type":"string"}},"required":["keyword"]}`, "keyword"},
	{"lookup_domains", "lookup_domains", "域名库存数据", "域名库存数据（只读库，50 个域名 1.5 积分）：月访问量、逐月访问、DR、注册日期、自然搜索快照。查流量盘子时先用它。",
		`{"type":"object","properties":{"domains":{"type":"array","items":{"type":"string"}}},"required":["domains"]}`, "domains"},
	{"domain_overview", "domain_overview", "流量概览", "流量概览（2 积分）：DR、月访问量与近 6 个月走势、渠道与地区、用户行为、域龄、主力流量词。库存数据不够时用。",
		`{"type":"object","properties":{"domain":{"type":"string"}},"required":["domain"]}`, "domain"},
	{"domain_dr", "domain_dr", "DR 批量查询", "DR 批量查询（每 20 个域名 1.5 积分）：Ahrefs Domain Rating。",
		`{"type":"object","properties":{"domains":{"type":"array","items":{"type":"string"}}},"required":["domains"]}`, "domains"},
	{"site_keywords", "site_keywords", "站点出词", "站点出词（贵：每 50 词 7.5 积分，最低 8）：一个域名或网址在谷歌排名的关键词、位置、搜索量、落地页。查自己的站优先用 GSC 数据；主要用于竞品。limit 尽量小。",
		`{"type":"object","properties":{"target":{"type":"string","description":"域名或完整网址"},"limit":{"type":"integer","description":"默认 50"}},"required":["target"]}`, "target"},
	{"keyword_ideas", "keyword_ideas", "拓词", "拓词（贵：每 50 词 7.5 积分，最低 8）：快照库拓词，带搜索量、趋势、CPC、预筛难度、意图。先用免费的谷歌联想。",
		`{"type":"object","properties":{"seeds":{"type":"array","items":{"type":"string"}},"mode":{"type":"string","enum":["ideas","suggestions","related"]},"limit":{"type":"integer"}},"required":["seeds"]}`, "seeds"},
	{"site_history", "site_history", "搜索流量历史", "搜索流量历史（8 积分）：一个域名逐月的排名词数、估算搜索流量、新进/掉出。",
		`{"type":"object","properties":{"target":{"type":"string"}},"required":["target"]}`, "target"},
	{"page_coach", "page_coach", "页面军师", "页面军师（3 积分）：推断页面目标词、核查谷歌实际排位与站内自相竞争，给 P0/P1/P2 建议。",
		`{"type":"object","properties":{"url":{"type":"string"}},"required":["url"]}`, "url"},
	{"backlink_value", "backlink_value", "外链评估", "外链评估（2 积分）：从某个站买外链值不值、合理价区间。",
		`{"type":"object","properties":{"domain":{"type":"string"},"price":{"type":"number"},"linkType":{"type":"string","enum":["guest","home","niche","nav","nofollow"]}},"required":["domain"]}`, "domain"},
	{"find_link_prospects", "find_link_prospects", "外链市场筛选", "外链市场筛选（1 积分）：按 DR / 月流量 / 自然搜索占比 / 价格筛选卖外链的站。",
		`{"type":"object","properties":{"minDr":{"type":"number"},"maxDr":{"type":"number"},"minTraffic":{"type":"number"},"maxPrice":{"type":"number"},"limit":{"type":"number"}}}`, ""},
	{"paid_serp", "google_serp", "谷歌搜索结果（付费）", "谷歌第一页（约 1 积分，7 天缓存）：自然结果的排名、网址、域名、标题、摘要，相关搜索。只在本机「谷歌搜索结果」查不了（需要人工验证、超过每天上限）时用。",
		`{"type":"object","properties":{"q":{"type":"string","description":"搜索词，支持 site: 等谷歌指令"},"gl":{"type":"string","description":"两位国家代码，默认 us"}},"required":["q"]}`, "q"},
}

// catalog returns SiteMoment's paid data catalog, cached for catalogTTL.
func (d *Deps) cloudCatalog(ctx context.Context) ([]cloud.Tool, error) {
	d.mu.Lock()
	if d.catalog != nil && d.now().Sub(d.catalogAt) < catalogTTL {
		defer d.mu.Unlock()
		return d.catalog, nil
	}
	d.mu.Unlock()
	cctx, cancel := context.WithTimeout(ctx, catalogFetchTimeout)
	defer cancel()
	list, err := d.Cloud.Catalog(cctx)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	d.catalog, d.catalogAt = list, d.now()
	d.mu.Unlock()
	return list, nil
}

// cloudTools are the paid tools the catalog lists as enabled.
func cloudTools(ctx context.Context, d *Deps) []*Tool {
	list, err := d.cloudCatalog(ctx)
	if err != nil {
		return nil
	}
	enabled := map[string]cloud.Tool{}
	for _, t := range list {
		if t.Enabled {
			enabled[t.Name] = t
		}
	}
	var out []*Tool
	for _, sp := range paidSpecs {
		sp := sp
		ct, ok := enabled[sp.cloudTool]
		if !ok {
			continue
		}
		label := sp.label
		if ct.Label != "" && sp.name != "paid_serp" {
			label = ct.Label
		}
		t := &Tool{
			Name: sp.name, Label: label, Kind: KindCloud, Paid: true,
			Description: sp.description,
			Params:      schema(sp.params),
			Quote: func(ctx context.Context, env *Env, args map[string]any) (float64, bool, error) {
				q, err := env.Cloud.Quote(ctx, sp.cloudTool, args)
				return q.MaxCredits, q.Cached, err
			},
			Run: func(ctx context.Context, env *Env, args map[string]any) (*Result, error) {
				r, err := env.Cloud.Data(ctx, sp.cloudTool, args)
				if err != nil {
					return nil, err
				}
				var data any
				_ = json.Unmarshal(r.Data, &data)
				card := autoCard("", data)
				note := fmt.Sprintf("SiteMoment 数据 · %s，取于 %s", r.Source, unixDate(r.FetchedAt))
				if r.Cached {
					note += "（缓存，未扣积分）"
				} else {
					note += fmt.Sprintf("（扣 %s 积分）", fmtCredits(r.Credits))
				}
				if card != nil {
					card.Note = note
				}
				text := capText(fmt.Sprintf(`{"source":%q,"data":%s}`, note, string(r.Data)), maxToolText)
				return &Result{Text: text, Card: card, Credits: r.Credits, Cached: r.Cached}, nil
			},
		}
		if sp.subject != "" {
			key := sp.subject
			t.Subject = func(a map[string]any) string {
				if l := argStrings(a, key); len(l) > 1 {
					return fmt.Sprintf("%s 等 %d 个", l[0], len(l))
				} else if len(l) == 1 {
					return l[0]
				}
				return argString(a, key)
			}
		}
		out = append(out, t)
	}
	return out
}
