// SPDX-License-Identifier: AGPL-3.0-or-later
package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/store"
	"github.com/sitemoment/sitemoment-desktop/internal/tasks"
)

// svcTasks backs the task tools with internal/tasks directly (app.Ops adds notices on top).
type svcTasks struct{ s *tasks.Service }

func (t svcTasks) Propose(ctx context.Context, p tasks.Proposal) (tasks.View, error) {
	return t.s.Propose(ctx, p)
}

func (t svcTasks) List(ctx context.Context, siteID int64) ([]tasks.View, error) {
	list, err := t.s.Store.Tasks(ctx, siteID)
	out := make([]tasks.View, len(list))
	for i, x := range list {
		out[i] = tasks.ViewOf(x)
	}
	return out, err
}

func TestHealthReportAndSitePages(t *testing.T) {
	env := siteEnv(t, &Deps{})
	if _, err := runHealthReport(ctx, env, nil); err == nil || !strings.Contains(err.Error(), "还没有每日体检记录") {
		t.Fatalf("no check yet: %v", err)
	}
	st := env.Store
	id, err := st.StartCrawl(ctx, env.Site.ID, "schedule", 100)
	ok(t, err)
	ok(t, st.FinishCrawl(ctx, store.Crawl{ID: id, FinishedAt: time.Date(2026, 11, 10, 8, 3, 0, 0, time.Local).Unix(), Pages: 40, OKPages: 38, Score: 75, P0: 1,
		Summary: `{"host":"example.com","pages":40}`},
		[]store.Issue{{Code: "lost_pages", Severity: "p0", Title: "以前正常、现在报错的页面", Detail: "恢复或 301", Count: 1, URLs: []string{"https://example.com/old → 404"}, New: true}}))
	res, err := runHealthReport(ctx, env, nil)
	ok(t, err)
	var out struct {
		Score   int
		Issues  []struct{ Severity, Code string }
		History []struct{ Date string }
		Summary map[string]any
	}
	ok(t, json.Unmarshal([]byte(res.Text), &out))
	if out.Score != 75 || len(out.Issues) != 1 || out.Issues[0].Severity != "P0" || len(out.History) != 1 || out.Summary["pages"] != 40.0 {
		t.Fatalf("health report = %s", res.Text)
	}
	if res.Card == nil || res.Card.Rows[0][3] != "新" || !strings.Contains(res.Card.Note, "2026-11-10 08:03") {
		t.Fatalf("card = %+v", res.Card)
	}

	ok(t, st.SavePages(ctx, env.Site.ID, []store.Page{
		{URL: "https://example.com/", FinalURL: "https://example.com/", Status: 200, OK: true, Title: "Home", LastSeen: 100},
		{URL: "https://example.com/old", FinalURL: "https://example.com/old", Status: 404, LastSeen: 100},
	}))
	ok(t, st.SetInspection(ctx, env.Site.ID, "https://example.com/", "PASS", "Submitted and indexed", 100))
	res, err = runSitePages(ctx, env, map[string]any{"filter": "error"})
	ok(t, err)
	if !strings.Contains(res.Text, `"total":1`) || !strings.Contains(res.Text, "/old") || strings.Contains(res.Text, `"url":"https://example.com/"`) {
		t.Fatalf("error pages = %s", res.Text)
	}
	res, err = runSitePages(ctx, env, nil)
	ok(t, err)
	if len(res.Card.Rows) != 2 || res.Card.Rows[1][2] != "是" {
		t.Fatalf("all pages card = %+v", res.Card.Rows)
	}
}

func TestCreateAndListTasks(t *testing.T) {
	env := siteEnv(t, &Deps{})
	env.Tasks = svcTasks{&tasks.Service{Store: env.Store, Now: env.Now}}
	sess, err := env.Store.CreateAgentSession(ctx, env.Site.ID, ModeConfirm, 1)
	ok(t, err)
	env.SessionID = sess.ID
	args := map[string]any{}
	ok(t, json.Unmarshal([]byte(`{"title":"首页标题改成主打 thumbnail maker","days":28,
		"conditions":[{"kind":"keyword_position","keyword":"Thumbnail Maker","value":10},{"kind":"url_status","url":"https://example.com/"}]}`), &args))
	res, err := runCreateTask(ctx, env, args)
	ok(t, err)
	if !strings.Contains(res.Text, `"status":"proposed"`) || len(res.Card.Items) != 3 || !strings.Contains(res.Card.Items[0].T, "确认后开始计时") ||
		res.Card.Items[1].T != "验收：「thumbnail maker」近 28 天平均排名 < 10" {
		t.Fatalf("create_task = %s / %+v", res.Text, res.Card)
	}
	list, _ := env.Store.Tasks(ctx, env.Site.ID)
	if len(list) != 1 || list[0].SessionID != sess.ID {
		t.Fatalf("stored = %+v", list)
	}
	if _, err := runCreateTask(ctx, env, map[string]any{"title": "x", "conditions": []any{map[string]any{"kind": "url_status", "url": "https://other.com/"}}}); err == nil ||
		!strings.Contains(err.Error(), "这个站点") {
		t.Fatalf("other site's URL: %v", err)
	}
	res, err = runListTasks(ctx, env, map[string]any{"status": "proposed"})
	ok(t, err)
	if !strings.Contains(res.Text, "首页标题改成主打 thumbnail maker") || res.Card.Rows[0][1] != "待确认" {
		t.Fatalf("list_tasks = %s", res.Text)
	}
	res, err = runListTasks(ctx, env, map[string]any{"status": "open"})
	ok(t, err)
	if !strings.Contains(res.Text, `"rows":[]`) {
		t.Fatalf("open tasks = %s", res.Text)
	}
	if _, err := runCreateTask(ctx, &Env{Deps: &Deps{Store: env.Store}, Site: env.Site}, args); err == nil {
		t.Fatal("created a task without a Tasks backend")
	}
}

func TestOpsToolsNeedASite(t *testing.T) {
	a := New(openStore(t), nil, &Deps{}, nil)
	names := func(env *Env) map[string]bool {
		tools, _ := a.toolset(ctx, env, ModeFreeOnly)
		out := map[string]bool{}
		for _, t := range tools {
			out[t.Name] = true
		}
		return out
	}
	if n := names(&Env{Deps: a.Deps}); n["create_task"] || n["health_report"] {
		t.Fatal("task tools offered without a site")
	}
	if n := names(&Env{Deps: a.Deps, Site: store.Site{ID: 1, Host: "example.com"}}); !n["create_task"] || !n["list_tasks"] || !n["site_pages"] || !n["health_report"] {
		t.Fatalf("tools with a site = %v", n)
	}
}
