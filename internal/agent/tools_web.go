// SPDX-License-Identifier: AGPL-3.0-or-later
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/rank"
	"github.com/sitemoment/sitemoment-desktop/internal/webdata"
)

// Cache lifetimes for free web data: archives change slowly, autocomplete weekly, speed daily.
const (
	waybackTTL = 7 * 24 * time.Hour
	suggestTTL = 7 * 24 * time.Hour
	psiTTL     = 24 * time.Hour
)

// webTools are the free tools that reach the web from this computer. A tool
// whose client is not set is left out.
func webTools(d *Deps) []*Tool {
	var out []*Tool
	if d.Fetcher != nil {
		out = append(out,
			&Tool{
				Name: "site_crawl", Label: "站点体检", Kind: KindWeb,
				Description: "免费。爬取整个网站（sitemap + 站内链接，最多约 150 页），返回站点级问题（P0/P1/P2：sitemap 里的错误页、死链、重复标题/H1、页面自相竞争、能抓但没进 sitemap 的页面等）和页面清单。诊断一个站时先调用。",
				Params:      schema(`{"type":"object","properties":{"site":{"type":"string","description":"域名或首页网址，如 example.com"},"maxPages":{"type":"integer","description":"最多爬多少页，默认 150"}},"required":["site"]}`),
				Subject:     func(a map[string]any) string { return hostOf(argString(a, "site")) },
				Run:         runCrawl,
			},
			&Tool{
				Name: "fetch_page", Label: "抓取网页", Kind: KindWeb,
				Description: "免费。抓取一个页面：状态码、Title、Meta Description、H1、canonical、robots、结构化数据、未通过的体检项，以及页面正文（前 4000 字）。用来判断页面在打什么词、TDK 是否重复。",
				Params:      schema(`{"type":"object","properties":{"url":{"type":"string","description":"完整网址"}},"required":["url"]}`),
				Subject:     subject("url"),
				Run:         runFetchPage,
			},
			&Tool{
				Name: "check_urls", Label: "网址状态", Kind: KindWeb,
				Description: "免费。逐个检查一组网址的状态码和跳转链（301/302/404），找出没做 301 的网址。",
				Params:      schema(`{"type":"object","properties":{"urls":{"type":"array","items":{"type":"string"},"description":"完整网址，最多 50 个"}},"required":["urls"]}`),
				Subject:     func(a map[string]any) string { return fmt.Sprintf("%d 个网址", len(argStrings(a, "urls"))) },
				Run:         runCheckURLs,
			})
		if d.Wayback != nil {
			out = append(out, &Tool{
				Name: "lost_urls", Label: "旧网址检查", Kind: KindWeb,
				Description: "免费。从互联网档案馆取这个站的历史网址，逐个检查现在是否 404 或被跳到首页，并按相似度给出 301 候选。站点改过版、换过路径时调用。先调用站点体检效果更好（用它的页面做 301 候选）。",
				Params:      schema(`{"type":"object","properties":{"site":{"type":"string","description":"域名"},"limit":{"type":"integer","description":"最多检查多少个历史网址，默认 120"}},"required":["site"]}`),
				Subject:     func(a map[string]any) string { return hostOf(argString(a, "site")) },
				Run:         runLostURLs,
			})
		}
	}
	if d.PSI != nil {
		out = append(out, &Tool{
			Name: "page_speed", Label: "页面速度", Kind: KindWeb,
			Description: "免费。PageSpeed Insights：Lighthouse 性能分、LCP、CLS、TBT，以及真实用户数据（有足够流量时）。",
			Params:      schema(`{"type":"object","properties":{"url":{"type":"string"},"strategy":{"type":"string","enum":["mobile","desktop"]}},"required":["url"]}`),
			Subject:     subject("url"),
			Run:         runPageSpeed,
		})
	}
	if d.Suggest != nil {
		out = append(out, &Tool{
			Name: "keyword_suggest", Label: "谷歌联想", Kind: KindWeb,
			Description: "免费。谷歌搜索框联想词，用来找长尾词和用户的真实说法（场景词）。alphabet=true 时再加 a–z 后缀扩展（较慢）。只说明有人搜，不代表搜索量。",
			Params:      schema(`{"type":"object","properties":{"seed":{"type":"string","description":"英文种子词"},"alphabet":{"type":"boolean"},"gl":{"type":"string","description":"国家，默认 us"}},"required":["seed"]}`),
			Subject:     subject("seed"),
			Run:         runSuggest,
		})
	}
	out = append(out, &Tool{
		Name: "load_playbook", Label: "查阅手册", Kind: KindWeb,
		Description: "免费。读方法论手册。可选：" + playbookIndex(),
		Params:      schema(`{"type":"object","properties":{"topic":{"type":"string","enum":["diagnosis","migration","keywords","cannibalization","indexing","backlinks"]}},"required":["topic"]}`),
		Subject:     func(a map[string]any) string { return Playbooks[argString(a, "topic")] },
		Run:         runPlaybook,
	})
	return out
}

func runPlaybook(_ context.Context, _ *Env, args map[string]any) (*Result, error) {
	text, err := readPlaybook(argString(args, "topic"))
	if err != nil {
		return nil, err
	}
	return &Result{Text: text}, nil
}

// cachedJSON answers from web_cache or calls fetch and stores its result. A
// fetch error is returned with whatever partial value fetch produced
// (partial values are not cached).
func cachedJSON[T any](ctx context.Context, env *Env, key string, ttl time.Duration, fetch func(context.Context) (T, error)) (T, bool, error) {
	now := env.now()
	if raw, _, hit, err := env.Store.WebCache(ctx, key, now.Unix()); err != nil {
		var zero T
		return zero, false, err
	} else if hit {
		var v T
		if json.Unmarshal([]byte(raw), &v) == nil {
			return v, true, nil
		}
	}
	v, err := fetch(ctx)
	if err != nil {
		return v, false, err
	}
	if raw, merr := json.Marshal(v); merr == nil {
		_ = env.Store.SetWebCache(ctx, key, string(raw), now.Unix(), now.Add(ttl).Unix())
	}
	return v, false, nil
}

func archivedURLs(ctx context.Context, env *Env, host string) ([]webdata.ArchivedURL, bool, error) {
	host = strings.ToLower(strings.TrimSpace(host))
	return cachedJSON(ctx, env, "wayback:"+host, waybackTTL, func(ctx context.Context) ([]webdata.ArchivedURL, error) {
		return env.Wayback.URLs(ctx, host, 5000)
	})
}

func pageSpeed(ctx context.Context, env *Env, pageURL, strategy string) (*webdata.PSIResult, bool, error) {
	if strategy != "desktop" {
		strategy = "mobile"
	}
	return cachedJSON(ctx, env, "psi:"+strategy+":"+strings.TrimSpace(pageURL), psiTTL, func(ctx context.Context) (*webdata.PSIResult, error) {
		return env.PSI.Run(ctx, pageURL, strategy)
	})
}

func suggestions(ctx context.Context, env *Env, seed, gl string, alphabet bool) ([]string, bool, error) {
	seed = strings.ToLower(strings.Join(strings.Fields(seed), " "))
	if gl == "" {
		gl = "us"
	}
	return cachedJSON(ctx, env, fmt.Sprintf("suggest:%s:%t:%s", gl, alphabet, seed), suggestTTL, func(ctx context.Context) ([]string, error) {
		return env.Suggest.Expand(ctx, seed, "en", gl, alphabet)
	})
}

var severityLabel = map[string]string{rank.SeverityP0: "P0", rank.SeverityP1: "P1", rank.SeverityP2: "P2"}

// issueTitles are the Chinese card titles of rank.SiteIssues ids (the model also gets the English detail).
var issueTitles = map[string]string{
	"robots_server_error":         "robots.txt 返回服务器错误",
	"robots_blocks_site":          "robots.txt 屏蔽了整站",
	"no_sitemap":                  "没有找到 sitemap",
	"sitemap_broken":              "sitemap 里有报错的页面",
	"broken_internal_links":       "站内链接指向错误页",
	"sitemap_redirect":            "sitemap 里有跳转的网址",
	"sitemap_noindex":             "sitemap 里有 noindex 页面",
	"sitemap_canonical_elsewhere": "sitemap 页面的 canonical 指向别处",
	"missing_title":               "页面缺少标题",
	"duplicate_title":             "多个页面标题相同",
	"duplicate_h1":                "多个页面 H1 相同",
	"cannibalization":             "页面自相竞争（抢同一个词）",
	"overlapping_posts":           "文章主题重叠",
	"not_in_sitemap":              "能抓到但没进 sitemap 的页面",
	"fetch_failed":                "抓取失败的页面",
	"lost_pages":                  "以前正常、现在报错的页面",
	"gsc_clicks_drop":             "谷歌点击明显下降",
}

func issueTitle(is rank.Issue) string { return IssueTitle(is.ID, is.Title) }

// IssueTitle is the Chinese title of a site issue id, or fallback when there is none.
func IssueTitle(id, fallback string) string {
	if t, ok := issueTitles[id]; ok {
		return t
	}
	return fallback
}

func runCrawl(ctx context.Context, env *Env, args map[string]any) (*Result, error) {
	start := siteURL(argString(args, "site"))
	limit := env.CrawlPages
	if limit <= 0 {
		limit = 150
	}
	res, err := rank.Crawl(ctx, env.Fetcher, start, rank.CrawlOptions{MaxPages: argInt(args, "maxPages", limit, 10, limit), Concurrency: 3, Budget: 150 * time.Second})
	if err != nil {
		return nil, err
	}
	env.rememberCrawl(res)
	issues := rank.SiteIssues(res)

	ok := 0
	type pageBrief struct {
		URL    string `json:"url"`
		Status int    `json:"status"`
		Title  string `json:"title,omitempty"`
		H1     string `json:"h1,omitempty"`
		Index  bool   `json:"indexable"`
	}
	var pages []pageBrief
	for _, p := range res.Pages {
		if p.OK() {
			ok++
		}
		if len(pages) < 60 {
			h1 := ""
			if len(p.H1) > 0 {
				h1 = p.H1[0]
			}
			pages = append(pages, pageBrief{p.URL, p.Status, truncateRunes(p.Title, 90), truncateRunes(h1, 90), p.Indexable()})
		}
	}
	type issueBrief struct {
		Severity string            `json:"severity"`
		ID       string            `json:"id"`
		Title    string            `json:"title"`
		Detail   string            `json:"detail"`
		Count    int               `json:"count"`
		URLs     []string          `json:"urls,omitempty"`
		Groups   []rank.IssueGroup `json:"groups,omitempty"`
	}
	var briefs []issueBrief
	card := &Card{Type: "table", Title: "站点体检：" + res.Host, Columns: []any{"级别", "问题", "数量"}}
	for _, is := range issues {
		b := issueBrief{Severity: severityLabel[is.Severity], ID: is.ID, Title: issueTitle(is), Detail: is.Detail, Count: is.Count, URLs: capStrings(is.URLs, 10)}
		for i, g := range is.Groups {
			if i == 5 {
				break
			}
			g.URLs = capStrings(g.URLs, 8)
			b.Groups = append(b.Groups, g)
		}
		briefs = append(briefs, b)
		card.Rows = append(card.Rows, []any{severityLabel[is.Severity], issueTitle(is), is.Count})
	}
	card.Note = fmt.Sprintf("本次实测抓取 %d 页（%d 页正常），sitemap %d 个网址", len(res.Pages), ok, len(res.SitemapURLs))
	if res.Truncated {
		card.Note += "；达到页数上限，未爬完"
	}
	if len(issues) == 0 {
		card = &Card{Type: "lines", Title: "站点体检：" + res.Host, Items: []Line{{S: "ok", T: "没有发现站点级问题"}}, Note: card.Note}
	}
	out := map[string]any{
		"host": res.Host, "robotsTxt": res.RobotsTxt, "robotsBlocksSite": res.RobotsBlocksRoot, "sitemaps": res.Sitemaps,
		"sitemapUrls": len(res.SitemapURLs), "crawled": len(res.Pages), "ok": ok, "truncated": res.Truncated,
		"issues": briefs, "pages": pages, "source": "本次实测抓取（" + env.now().Format("2006-01-02") + "）",
	}
	// Shrink the page sample, then the issue examples, until the JSON fits whole.
	text := fitJSON(out, func() bool {
		switch {
		case len(pages) > 0:
			pages = pages[:len(pages)/2]
			out["pages"] = pages
		default:
			trimmed := false
			for i := range briefs {
				if len(briefs[i].URLs) > 3 {
					briefs[i].URLs, trimmed = briefs[i].URLs[:3], true
				}
				for j := range briefs[i].Groups {
					if len(briefs[i].Groups[j].URLs) > 3 {
						briefs[i].Groups[j].URLs, trimmed = briefs[i].Groups[j].URLs[:3], true
					}
				}
			}
			out["issues"] = briefs
			return trimmed
		}
		return true
	})
	return &Result{Text: text, Card: card}, nil
}

func capStrings(l []string, n int) []string {
	if len(l) > n {
		return l[:n]
	}
	return l
}

func runFetchPage(ctx context.Context, env *Env, args map[string]any) (*Result, error) {
	u := siteURL(argString(args, "url"))
	rep, text, err := rank.FetchPage(ctx, env.Fetcher, u)
	if err != nil {
		var fe *rank.FetchError
		if errors.As(err, &fe) && fe.Reason == "status" {
			// A 404 is an answer, not a failure: say so, with the redirect chain.
			if t, terr := env.Fetcher.Trace(ctx, u); terr == nil {
				return &Result{Text: jsonText(t), Card: &Card{Type: "lines", Items: []Line{{S: "fail", T: fmt.Sprintf("%s → HTTP %d", u, t.FinalStatus)}}}}, nil
			}
		}
		return nil, err
	}
	var failing []map[string]string
	card := &Card{Type: "lines", Title: rep.Page.Title}
	for _, c := range rep.Checks {
		if c.Status == rank.StatusPass {
			continue
		}
		failing = append(failing, map[string]string{"id": c.ID, "status": c.Status, "detail": c.Detail})
		if len(card.Items) < 8 {
			card.Items = append(card.Items, Line{S: c.Status, T: c.Title + "：" + c.Detail})
		}
	}
	if len(card.Items) == 0 {
		card.Items = []Line{{S: "ok", T: fmt.Sprintf("体检 %d 分，全部通过", rep.Score)}}
	}
	card.Note = fmt.Sprintf("HTTP %d · 体检 %d 分 · %d 词", rep.StatusCode, rep.Score, rep.Page.Words)
	out := map[string]any{
		"url": rep.URL, "finalUrl": rep.FinalURL, "status": rep.StatusCode, "score": rep.Score,
		"title": rep.Page.Title, "description": rep.Page.Description, "h1": rep.Page.H1, "canonical": rep.Page.Canonical,
		"robots": rep.Page.Robots, "lang": rep.Page.Lang, "words": rep.Page.Words, "jsonLdTypes": rep.Page.JSONLDTypes,
		"links": rep.Page.Links, "failingChecks": failing, "text": capText(text, 4000),
	}
	raw, _ := json.Marshal(out)
	return &Result{Text: capText(string(raw), maxToolText), Card: card}, nil
}

func runCheckURLs(ctx context.Context, env *Env, args map[string]any) (*Result, error) {
	urls := argStrings(args, "urls")
	if len(urls) == 0 {
		return nil, fmt.Errorf("urls is required")
	}
	if len(urls) > 50 {
		urls = urls[:50]
	}
	var pages []rank.CrawledPage
	if c := env.crawlOf(hostOf(urls[0])); c != nil {
		pages = c.Pages
	}
	checks := rank.CheckURLs(ctx, env.Fetcher, urls, pages, 3)
	return &Result{Text: jsonText(checks), Card: urlCheckCard("网址状态", checks, false)}, nil
}

func urlCheckCard(title string, checks []rank.URLCheck, onlyBad bool) *Card {
	c := &Card{Type: "table", Title: title, Columns: []any{"网址", "状态", "跳转到", "建议 301 到"}}
	for _, ch := range checks {
		if onlyBad && !ch.Lost && !ch.ToHome {
			continue
		}
		status := fmt.Sprint(ch.FinalStatus)
		switch {
		case ch.Error != "":
			status = ch.Error
		case ch.Redirected && ch.Permanent:
			status = fmt.Sprintf("301 → %d", ch.FinalStatus)
		case ch.Redirected:
			status = fmt.Sprintf("302 → %d", ch.FinalStatus)
		}
		to := ""
		if ch.Redirected {
			to = ch.FinalURL
		}
		c.Rows = append(c.Rows, []any{ch.URL, status, to, ch.Suggest})
		if len(c.Rows) == 30 {
			break
		}
	}
	return c
}

func runLostURLs(ctx context.Context, env *Env, args map[string]any) (*Result, error) {
	host := hostOf(argString(args, "site"))
	if host == "" {
		return nil, fmt.Errorf("site is required")
	}
	limit := argInt(args, "limit", 120, 10, 300)
	archived, cacheHit, err := archivedURLs(ctx, env, host)
	if err != nil && len(archived) == 0 {
		return nil, err
	}
	crawl := env.crawlOf(host)
	known := map[string]bool{}
	if crawl != nil {
		for _, p := range crawl.Pages {
			if u, err := url.Parse(p.URL); err == nil {
				known[u.Path] = true
			}
		}
	}
	var old []string
	for _, a := range archived {
		u, err := url.Parse(a.URL)
		if err != nil || known[u.Path] {
			continue
		}
		u.Scheme, u.Host = "https", host
		old = append(old, u.String())
		if len(old) == limit {
			break
		}
	}
	var pages []rank.CrawledPage
	if crawl != nil {
		pages = crawl.Pages
	}
	checks := rank.CheckURLs(ctx, env.Fetcher, old, pages, 3)
	var bad []rank.URLCheck
	redirected := 0
	for _, c := range checks {
		if c.Lost || c.ToHome {
			bad = append(bad, c)
		} else if c.Redirected {
			redirected++
		}
	}
	card := urlCheckCard("旧网址检查："+host, bad, true)
	card.Note = fmt.Sprintf("互联网档案馆历史网址 %d 个，检查了 %d 个不在本次爬取里的；%d 个丢失或跳首页，%d 个已正确跳转", len(archived), len(old), len(bad), redirected)
	if len(bad) == 0 {
		card = &Card{Type: "lines", Title: card.Title, Items: []Line{{S: "ok", T: "没有发现 404 或跳首页的旧网址"}}, Note: card.Note}
	}
	text := jsonText(map[string]any{
		"archived": len(archived), "checked": len(old), "redirectedOK": redirected, "lostOrToHome": bad,
		"crawlUsedForSuggestions": crawl != nil, "source": "Wayback Machine CDX + 本次实测",
	})
	return &Result{Text: text, Card: card, Cached: cacheHit}, nil
}

func runPageSpeed(ctx context.Context, env *Env, args map[string]any) (*Result, error) {
	u := siteURL(argString(args, "url"))
	r, hit, err := pageSpeed(ctx, env, u, argString(args, "strategy"))
	if err != nil {
		return nil, err
	}
	card := &Card{Type: "table", Title: "页面速度（" + r.Strategy + "）", Columns: []any{"指标", "实验室", "真实用户 p75"}}
	perf := any("—")
	if r.Performance != nil {
		perf = *r.Performance
	}
	field := func(k string) any {
		if m, ok := r.Field[k]; ok {
			return fmt.Sprintf("%v（%s）", m.P75, m.Category)
		}
		return "—"
	}
	card.Rows = [][]any{
		{"性能分", perf, r.FieldOverall},
		{"LCP (ms)", r.LCPms, field("LCP")},
		{"CLS", r.CLS, field("CLS")},
		{"TBT (ms) / INP", r.TBTms, field("INP")},
	}
	card.Note = "PageSpeed Insights（Lighthouse 实验室数据 + Chrome UX Report 真实用户数据）"
	return &Result{Text: jsonText(r), Card: card, Cached: hit}, nil
}

func runSuggest(ctx context.Context, env *Env, args map[string]any) (*Result, error) {
	seed := argString(args, "seed")
	if seed == "" {
		return nil, fmt.Errorf("seed is required")
	}
	list, hit, err := suggestions(ctx, env, seed, argString(args, "gl"), argBool(args, "alphabet"))
	if err != nil && len(list) == 0 {
		return nil, err
	}
	card := &Card{Type: "lines", Title: "谷歌联想：" + seed}
	for i, s := range list {
		if i == 30 {
			break
		}
		card.Items = append(card.Items, Line{S: "ok", T: s})
	}
	card.Note = fmt.Sprintf("共 %d 个联想词；只说明有人搜，不代表搜索量", len(list))
	return &Result{Text: jsonText(map[string]any{"seed": seed, "suggestions": list}), Card: card, Cached: hit}, nil
}
