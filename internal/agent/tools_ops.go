// SPDX-License-Identifier: AGPL-3.0-or-later
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/store"
	"github.com/sitemoment/sitemoment-desktop/internal/tasks"
)

// Tasks creates and lists the site's tasks (app.Ops: it also applies the
// auto-accept setting and notifies the user).
type Tasks interface {
	Propose(ctx context.Context, p tasks.Proposal) (tasks.View, error)
	List(ctx context.Context, siteID int64) ([]tasks.View, error)
}

// opsTools read the daily health checks and manage tasks; they need a site.
func opsTools() []*Tool {
	return []*Tool{
		{
			Name: "health_report", Label: "体检报告", Kind: KindLocal,
			Description: "免费，本机数据。这个站最近一次每日体检的结果：健康分、P0/P1/P2 问题（含以前正常现在报错的页面、新出现的 noindex、canonical 变化、sitemap 错误、GSC 28 天点击/展示骤降、谷歌没有收录的页面），以及最近几次的分数。比「站点体检」快，不用重新爬；要看现在的实时状态再用「站点体检」。",
			Params:      schema(`{"type":"object","properties":{}}`),
			Run:         runHealthReport,
		},
		{
			Name: "site_pages", Label: "站点页面", Kind: KindLocal,
			Description: "免费，本机数据。每日体检记住的页面：状态码、标题、noindex、canonical、是否在 sitemap、谷歌是否收录（Search Console 网址检查）、上次正常的日期。filter 只看一类：error（4xx/5xx 或抓取失败）、noindex、redirect、not_indexed（网址检查说没收录）。",
			Params: schema(`{"type":"object","properties":{"filter":{"type":"string","enum":["all","error","noindex","redirect","not_indexed"],"description":"默认 all"},` +
				`"contains":{"type":"string","description":"网址里包含的文字"},"limit":{"type":"integer","description":"默认 50，最多 200"}}}`),
			Subject: subject("filter"),
			Run:     runSitePages,
		},
		{
			Name: "create_task", Label: "创建任务", Kind: KindLocal,
			Description: "免费。把一条建议变成任务，带可以自动验收的条件；用户确认后开始计时，客户端每次同步和体检后自动复查，达标或到期时通知用户。每条建议一个任务。条件（全部满足才算通过，1–5 个）：" +
				"keyword_position（keyword 的 GSC 平均排名 < value，window 天数，默认 28）；" +
				"url_status（url 返回 status：200、301、404、410，默认 200；301 指永久跳转）；" +
				"page_indexed（url 被谷歌收录，靠体检时的 Search Console 网址检查）；" +
				"metric（GSC 的 clicks / impressions / ctr / position，scope = site 全站、page（url）或 query（keyword），op = lt/lte/gt/gte，value；ctr 用小数，0.05 = 5%）。" +
				"排名和指标只统计任务开始之后的数据。url 必须是这个站点的完整网址。",
			Params: schema(`{"type":"object","properties":{"title":{"type":"string","description":"要做什么，一句话"},"detail":{"type":"string","description":"具体怎么做（可选）"},` +
				`"days":{"type":"integer","description":"多少天内验收，1–180，默认 28"},"conditions":{"type":"array","items":{"type":"object","properties":{` +
				`"kind":{"type":"string","enum":["keyword_position","url_status","page_indexed","metric"]},"keyword":{"type":"string"},"url":{"type":"string"},` +
				`"metric":{"type":"string","enum":["clicks","impressions","ctr","position"]},"scope":{"type":"string","enum":["site","page","query"]},` +
				`"op":{"type":"string","enum":["lt","lte","gt","gte"]},"value":{"type":"number"},"window":{"type":"integer"},"status":{"type":"integer"}},"required":["kind"]}}},` +
				`"required":["title","conditions"]}`),
			Subject: subject("title"),
			Run:     runCreateTask,
		},
		{
			Name: "list_tasks", Label: "任务列表", Kind: KindLocal,
			Description: "免费，本机数据。这个站的任务和验收进度：每个条件现在的数、开始前的数、达标没有。建议新任务前先看看，避免重复。",
			Params:      schema(`{"type":"object","properties":{"status":{"type":"string","enum":["all","proposed","open","passed","failed"],"description":"默认 all（不含已忽略的）"}}}`),
			Subject:     subject("status"),
			Run:         runListTasks,
		},
	}
}

func dateOf(ts int64) string {
	if ts <= 0 {
		return ""
	}
	return time.Unix(ts, 0).Format("2006-01-02")
}

func runHealthReport(ctx context.Context, env *Env, args map[string]any) (*Result, error) {
	if err := needSite(env); err != nil {
		return nil, err
	}
	latest, err := env.Store.LatestCrawl(ctx, env.Site.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, errors.New("这个站点还没有每日体检记录（每天 08:00 自动体检，也可以在「体检」页立即体检）；现在要看就用「站点体检」现场爬一次")
	}
	if err != nil {
		return nil, err
	}
	issues, err := env.Store.CrawlIssues(ctx, latest.ID)
	if err != nil {
		return nil, err
	}
	hist, err := env.Store.Crawls(ctx, env.Site.ID, 10)
	if err != nil {
		return nil, err
	}
	type issueBrief struct {
		Severity string   `json:"severity"`
		Code     string   `json:"code"`
		Title    string   `json:"title"`
		Detail   string   `json:"detail"`
		Count    int      `json:"count"`
		New      bool     `json:"new"`
		URLs     []string `json:"urls,omitempty"`
	}
	briefs := []issueBrief{}
	card := &Card{Type: "table", Title: fmt.Sprintf("体检报告：健康分 %d", latest.Score), Columns: []any{"级别", "问题", "数量", ""}}
	for _, is := range issues {
		briefs = append(briefs, issueBrief{severityLabel[is.Severity], is.Code, is.Title, is.Detail, is.Count, is.New, capStrings(is.URLs, 10)})
		isNew := ""
		if is.New {
			isNew = "新"
		}
		card.Rows = append(card.Rows, []any{severityLabel[is.Severity], is.Title, is.Count, isNew})
	}
	type point struct {
		Date   string `json:"date"`
		Status string `json:"status"`
		Score  int    `json:"score,omitempty"`
		Error  string `json:"error,omitempty"`
	}
	var history []point
	for _, c := range hist {
		history = append(history, point{dateOf(c.StartedAt), c.Status, c.Score, c.Error})
	}
	var summary any
	json.Unmarshal([]byte(latest.Summary), &summary)
	when := time.Unix(latest.FinishedAt, 0).Format("2006-01-02 15:04")
	card.Note = fmt.Sprintf("每日体检 · 本机记录 · %s · 抓取 %d 页（%d 页正常）", when, latest.Pages, latest.OKPages)
	if len(issues) == 0 {
		card = &Card{Type: "lines", Title: card.Title, Items: []Line{{S: "ok", T: "没有发现问题"}}, Note: card.Note}
	}
	out := map[string]any{"source": "每日体检（本机记录，" + when + "）", "score": latest.Score, "p0": latest.P0, "p1": latest.P1, "p2": latest.P2,
		"summary": summary, "issues": briefs, "history": history}
	text := fitJSON(out, func() bool {
		trimmed := false
		for i := range briefs {
			if len(briefs[i].URLs) > 3 {
				briefs[i].URLs, trimmed = briefs[i].URLs[:3], true
			}
		}
		out["issues"] = briefs
		return trimmed
	})
	return &Result{Text: text, Card: card}, nil
}

func runSitePages(ctx context.Context, env *Env, args map[string]any) (*Result, error) {
	if err := needSite(env); err != nil {
		return nil, err
	}
	filter := orDefault(argString(args, "filter"), "all")
	pages, total, err := env.Store.Pages(ctx, store.PageQuery{SiteID: env.Site.ID, Filter: filter, Contains: argString(args, "contains"), Limit: argInt(args, "limit", 50, 1, 200)})
	if err != nil {
		return nil, err
	}
	type row struct {
		URL       string `json:"url"`
		Status    int    `json:"status,omitempty"`
		Final     string `json:"finalUrl,omitempty"`
		Title     string `json:"title,omitempty"`
		Noindex   bool   `json:"noindex,omitempty"`
		Canonical string `json:"canonical,omitempty"`
		InSitemap bool   `json:"inSitemap"`
		Indexed   string `json:"indexed,omitempty"`
		LastOK    string `json:"lastOk,omitempty"`
		Error     string `json:"error,omitempty"`
	}
	rows := []row{}
	card := &Card{Type: "table", Columns: []any{"网址", "状态", "收录", "标题"}}
	for _, p := range pages {
		r := row{URL: p.URL, Status: p.Status, Title: truncateRunes(p.Title, 90), Noindex: p.Noindex, Canonical: p.Canonical, InSitemap: p.InSitemap,
			LastOK: dateOf(p.LastOK), Error: p.Error}
		if p.FinalURL != p.URL {
			r.Final = p.FinalURL
		}
		indexed := "—"
		if p.IndexVerdict != "" {
			r.Indexed = p.IndexCoverage + "（" + dateOf(p.IndexCheckedAt) + "）"
			indexed = "否"
			if p.IndexVerdict == "PASS" {
				indexed = "是"
			}
		}
		rows = append(rows, r)
		if len(card.Rows) < 20 {
			card.Rows = append(card.Rows, []any{p.URL, p.Status, indexed, truncateRunes(p.Title, 40)})
		}
	}
	card.Note = fmt.Sprintf("每日体检记住的页面（本机）· %s · 共 %d 个", filter, total)
	res := map[string]any{"source": "每日体检记住的页面（本机）", "filter": filter, "total": total, "rows": rows}
	return &Result{Text: fitRows(res, &rows), Card: card}, nil
}

func runCreateTask(ctx context.Context, env *Env, args map[string]any) (*Result, error) {
	if err := needSite(env); err != nil {
		return nil, err
	}
	if env.Tasks == nil {
		return nil, errors.New("任务功能不可用")
	}
	var conds []tasks.Condition
	raw, _ := json.Marshal(args["conditions"])
	if err := json.Unmarshal(raw, &conds); err != nil {
		return nil, errors.New("conditions 要是条件对象的数组")
	}
	v, err := env.Tasks.Propose(ctx, tasks.Proposal{SiteID: env.Site.ID, SessionID: env.SessionID, Title: argString(args, "title"),
		Detail: argString(args, "detail"), Days: argInt(args, "days", 0, 0, 1000), Conditions: conds})
	if err != nil {
		return nil, err
	}
	head := "已建议任务「" + v.Title + "」：等用户在「任务」页确认后开始计时"
	if v.Status == tasks.StatusOpen {
		head = fmt.Sprintf("已加入任务「%s」：%s 前验收", v.Title, dateOf(v.DueAt))
	}
	card := &Card{Type: "lines", Title: "任务", Items: []Line{{S: "ok", T: head}}}
	for _, l := range v.Labels {
		card.Items = append(card.Items, Line{S: "warn", T: "验收：" + l})
	}
	return &Result{Text: jsonText(map[string]any{"id": v.ID, "status": v.Status, "title": v.Title, "days": v.Days, "conditions": v.Labels,
		"note": "告诉用户这个任务" + map[bool]string{true: "已经加入任务列表", false: "要在「任务」页确认后才开始计时"}[v.Status == tasks.StatusOpen]}), Card: card}, nil
}

var taskStatusText = map[string]string{tasks.StatusProposed: "待确认", tasks.StatusOpen: "进行中", tasks.StatusPassed: "已通过", tasks.StatusFailed: "未通过", tasks.StatusDismissed: "已忽略"}

func runListTasks(ctx context.Context, env *Env, args map[string]any) (*Result, error) {
	if err := needSite(env); err != nil {
		return nil, err
	}
	if env.Tasks == nil {
		return nil, errors.New("任务功能不可用")
	}
	list, err := env.Tasks.List(ctx, env.Site.ID)
	if err != nil {
		return nil, err
	}
	want := orDefault(argString(args, "status"), "all")
	type cond struct {
		Label    string   `json:"label"`
		State    string   `json:"state,omitempty"`
		Current  *float64 `json:"current,omitempty"`
		Baseline *float64 `json:"baseline,omitempty"`
		Note     string   `json:"note,omitempty"`
	}
	type row struct {
		ID         int64  `json:"id"`
		Title      string `json:"title"`
		Status     string `json:"status"`
		Due        string `json:"due,omitempty"`
		Conditions []cond `json:"conditions"`
	}
	rows := []row{}
	card := &Card{Type: "table", Columns: []any{"任务", "状态", "到期"}}
	for _, v := range list {
		if v.Status == tasks.StatusDismissed || (want != "all" && v.Status != want) {
			continue
		}
		r := row{ID: v.ID, Title: v.Title, Status: v.Status, Due: dateOf(v.DueAt), Conditions: []cond{}}
		for i, l := range v.Labels {
			c := cond{Label: l}
			if i < len(v.Outcomes) {
				o := v.Outcomes[i]
				c.State, c.Current, c.Baseline, c.Note = o.State, o.Current, o.Baseline, o.Note
			}
			r.Conditions = append(r.Conditions, c)
		}
		rows = append(rows, r)
		if len(card.Rows) < 20 {
			card.Rows = append(card.Rows, []any{v.Title, taskStatusText[v.Status], orDefault(r.Due, "—")})
		}
	}
	card.Note = fmt.Sprintf("本机任务 · %d 个", len(rows))
	res := map[string]any{"source": "任务（本机）", "rows": rows}
	return &Result{Text: fitRows(res, &rows), Card: card}, nil
}
