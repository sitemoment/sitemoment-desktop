// SPDX-License-Identifier: AGPL-3.0-or-later
package report

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/store"
	"github.com/sitemoment/sitemoment-desktop/internal/tasks"
)

var ctx = context.Background()

func ok(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestBuildWeeklyReport(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	ok(t, err)
	defer st.Close()
	ok(t, st.Migrate(ctx))
	site, err := st.AddSite(ctx, "example.com", 1)
	ok(t, err)
	ok(t, st.BindGSC(ctx, site.ID, "me@example.com", "sc-domain:example.com"))
	ok(t, st.BindGA(ctx, site.ID, "me@example.com", "properties/1"))
	site, _ = st.Site(ctx, site.ID)
	now := time.Date(2026, 11, 10, 9, 0, 0, 0, time.Local)

	// GSC to 2026-11-08: this week 2026-11-02..08, last week 2026-10-26..11-01.
	var days []store.GSCRow
	for d := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC); !d.After(time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)); d = d.AddDate(0, 0, 1) {
		clicks := 10.0
		if d.After(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
			clicks = 20
		}
		days = append(days, store.GSCRow{Day: d.Format(store.DayLayout), Clicks: clicks, Impressions: clicks * 20, Position: 12})
	}
	ok(t, st.ReplaceGSC(ctx, site.ID, "date", "2026-10-12", "2026-11-08", days, 1))
	ok(t, st.ReplaceGSC(ctx, site.ID, "query", "2026-10-26", "2026-11-08", []store.GSCRow{
		{Day: "2026-10-27", Query: "thumbnail maker", Clicks: 5, Impressions: 300, Position: 14},
		{Day: "2026-11-03", Query: "thumbnail maker", Clicks: 30, Impressions: 400, Position: 9},
		{Day: "2026-10-27", Query: "old <b>query</b> | x", Clicks: 8, Impressions: 50, Position: 6},
		{Day: "2026-11-03", Query: "youtube banner size", Clicks: 1, Impressions: 120, Position: 11.4},
	}, 1))
	ok(t, st.ReplaceGA(ctx, site.ID, "date", "2026-10-26", "2026-11-08", []store.GARow{
		{Day: "2026-10-27", Sessions: 100}, {Day: "2026-11-03", Sessions: 150}}, 1))
	id, err := st.StartCrawl(ctx, site.ID, "schedule", now.Add(-time.Hour).Unix())
	ok(t, err)
	ok(t, st.FinishCrawl(ctx, store.Crawl{ID: id, FinishedAt: now.Add(-50 * time.Minute).Unix(), Pages: 120, OKPages: 118, Score: 73, P0: 1, P2: 1},
		[]store.Issue{{Code: "lost_pages", Severity: "p0", Title: "以前正常、现在报错的页面", Count: 2, New: true}, {Code: "not_in_sitemap", Severity: "p2", Title: "能抓到但没进 sitemap 的页面", Count: 4}}))
	ts := &tasks.Service{Store: st, Now: func() time.Time { return now.AddDate(0, 0, -3) }}
	_, err = ts.Propose(ctx, tasks.Proposal{SiteID: site.ID, Title: "首页标题改成主打 thumbnail maker", Days: 5, Open: true,
		Conditions: []tasks.Condition{{Kind: tasks.KindPosition, Keyword: "thumbnail maker", Value: 10}}})
	ok(t, err)

	r, err := Build(ctx, st, site, now, "weekly")
	ok(t, err)
	if r.ID == 0 || r.From != "2026-11-02" || r.To != "2026-11-08" || r.Title != "example.com 周报 · 2026-11-02 至 2026-11-08" {
		t.Fatalf("report = %+v", r)
	}
	for _, want := range []string{
		"| GSC 点击 | 140 | 70 | +100.0% |",
		"| GA4 会话 | 150 | 100 | +50.0% |",
		"| 健康分 | 73 | — | — |",
		"| thumbnail maker | 30 | 5 | +25 | 9.0 |",
		// cell() neutralizes Markdown/HTML metacharacters from GSC/agent text with
		// full-width lookalikes, so a third-party Markdown reader of the exported
		// .md file cannot turn "<b>" into live HTML or "<!--" into a comment.
		"| old ＜b＞query＜/b＞ ｜ x | 0 | 8 | -8 | — |",
		"| youtube banner size | 120 | 1 | 11.4 |",
		"- **P0** 以前正常、现在报错的页面（2） · 新",
		"- 另有 1 类 P2 问题",
		"- · 首页标题改成主打 thumbnail maker（到期 2026-11-12）",
		"「thumbnail maker」近 28 天平均排名 ＜ 10：",
		"1. 先处理 P0：「以前正常、现在报错的页面」（2）。",
		"任务「首页标题改成主打 thumbnail maker」11-12 到期",
	} {
		if !strings.Contains(r.Markdown, want) {
			t.Errorf("markdown lacks %q", want)
		}
	}
	h := r.HTML
	for _, want := range []string{"<svg", "每日 GSC 点击", "<title>2026-11-08：20 次点击，400 次展示</title>", `<div class="k">GSC 点击</div><div class="v">140</div>`,
		"<td>old ＜b＞query＜/b＞ ｜ x</td>", "<h2>体检</h2>", "<strong>P0</strong>"} {
		if !strings.Contains(h, want) {
			t.Errorf("html lacks %q", want)
		}
	}
	for _, bad := range []string{"<script", "<b>query", "http://", "<!-- chart -->", "<link"} {
		if strings.Contains(h, bad) {
			t.Errorf("html contains %q", bad)
		}
	}
	if os.Getenv("REPORT_OUT") != "" {
		os.WriteFile(os.Getenv("REPORT_OUT"), []byte(h), 0o644)
	}
}

func TestEmptySiteReport(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	ok(t, err)
	defer st.Close()
	ok(t, st.Migrate(ctx))
	site, _ := st.AddSite(ctx, "example.com", 1)
	d, err := Gather(ctx, st, site, time.Date(2026, 11, 10, 9, 0, 0, 0, time.Local))
	ok(t, err)
	md := Markdown(d)
	if d.To != "2026-11-09" || !strings.Contains(md, "还没有同步的数据") || !strings.Contains(md, "还没有体检记录") || strings.Contains(HTML(d, md), "<svg") {
		t.Fatalf("empty report:\n%s", md)
	}
}

func TestMarkdownToHTML(t *testing.T) {
	got := mdToHTML("# T\n\n> note\n\n- a **b**\n  - c `d`\n- e\n\n1. one\n2. two\n\n| A | B |\n|---|---|\n| <x> | [l](javascript:x) |\n\ntext & more")
	want := strings.Join([]string{"<h1>T</h1>", "<blockquote>note</blockquote>", "<ul>", "<li>a <strong>b</strong></li>", "<ul>", "<li>c <code>d</code></li>", "</ul>",
		"<li>e</li>", "</ul>", "<ol>", "<li>one</li>", "<li>two</li>", "</ol>",
		"<table><thead><tr><th>A</th><th>B</th></tr></thead><tbody><tr><td>&lt;x&gt;</td><td>[l](javascript:x)</td></tr></tbody></table>", "<p>text &amp; more</p>"}, "\n")
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestMdToHTMLKeepsArbitraryComments(t *testing.T) {
	// Only the literal chart marker is special; any other "<!-- -->" in the
	// text (e.g. from a GSC query) must be escaped and shown, not silently
	// dropped by a generic comment-stripping regex.
	got := mdToHTML("before <!-- not the chart marker --> after")
	if !strings.Contains(got, "&lt;!-- not the chart marker --&gt;") {
		t.Fatalf("an arbitrary HTML comment must be escaped, not stripped: %q", got)
	}
}

func TestCellNeutralizesMarkdownAndHTMLMetacharacters(t *testing.T) {
	got := cell("a | b\nc <!-- x --> <b>bold</b> [link](javascript:x)")
	for _, bad := range []string{"|", "\n", "<!--", "<", ">", "[", "]", "(", ")"} {
		if strings.Contains(got, bad) {
			t.Fatalf("cell(%q) = %q still contains %q", "a | b\\nc...", got, bad)
		}
	}
}

func TestClicksChartGuardsDayLabels(t *testing.T) {
	days := []store.DayPoint{
		{Day: "x", Clicks: 1, Impressions: 2},                 // shorter than len("YYYY-MM-DD"): must not panic
		{Day: "2026-11-08<script>", Clicks: 3, Impressions: 4}, // must be escaped, not injected raw into the SVG
	}
	got := clicksChart(days, "2026-11-01")
	if strings.Contains(got, "<script>") {
		t.Fatalf("a day label must be HTML-escaped: %s", got)
	}
}

func TestNiceMax(t *testing.T) {
	for _, c := range []struct{ hi, top, step float64 }{{0, 4, 1}, {3, 4, 1}, {20, 20, 5}, {37, 40, 10}, {101, 200, 50}, {950, 1000, 250}} {
		top, step := niceMax(c.hi)
		if top != c.top || step != c.step {
			t.Errorf("niceMax(%v) = %v, %v; want %v, %v", c.hi, top, step, c.top, c.step)
		}
	}
	if s := fmt.Sprint(fmtInt(1234567), " ", fmtInt(-1200), " ", pct(3, 0), " ", posChange(9, 14)); s != "1,234,567 -1,200 — ↑ 5.0" {
		t.Fatalf("formatting = %q", s)
	}
}
