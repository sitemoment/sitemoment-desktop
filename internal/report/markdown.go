// SPDX-License-Identifier: AGPL-3.0-or-later
package report

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/tasks"
)

// Title is the report's title.
func Title(d Data) string { return fmt.Sprintf("%s 周报 · %s 至 %s", d.Site.Host, d.From, d.To) }

// KPI is one row of the overview table and one card of the HTML version.
type KPI struct {
	Label, Cur, Prev, Change string
}

// KPIs are the week's headline numbers.
func KPIs(d Data) []KPI {
	var out []KPI
	if g := d.GSC; g != nil {
		out = append(out,
			KPI{"GSC 点击", fmtInt(g.Cur.Clicks), fmtInt(g.Prev.Clicks), pct(g.Cur.Clicks, g.Prev.Clicks)},
			KPI{"GSC 展示", fmtInt(g.Cur.Impressions), fmtInt(g.Prev.Impressions), pct(g.Cur.Impressions, g.Prev.Impressions)},
			KPI{"CTR", fmtPct(g.Cur.CTR), fmtPct(g.Prev.CTR), points(g.Cur.CTR, g.Prev.CTR)},
			KPI{"平均排名", fmtPos(g.Cur.Position), fmtPos(g.Prev.Position), posChange(g.Cur.Position, g.Prev.Position)})
	}
	if p := d.GA; p != nil {
		out = append(out, KPI{"GA4 会话", fmtInt(p.Cur), fmtInt(p.Prev), pct(p.Cur, p.Prev)})
	}
	if p := d.Bing; p != nil {
		out = append(out, KPI{"Bing 点击", fmtInt(p.Cur), fmtInt(p.Prev), pct(p.Cur, p.Prev)})
	}
	if h := d.Health; h != nil {
		k := KPI{"健康分", fmt.Sprint(h.Latest.Score), "—", "—"}
		if h.WeekAgo != nil {
			k.Prev, k.Change = fmt.Sprint(h.WeekAgo.Score), signed(float64(h.Latest.Score-h.WeekAgo.Score), 0)
		}
		out = append(out, k)
	}
	return out
}

// Markdown renders the report.
func Markdown(d Data) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", Title(d))
	fmt.Fprintf(&b, "> 生成于 %s · SiteMoment Desktop · 数据来自本机：Search Console、GA4、Bing 的同步数据和体检记录。本周 = %s 至 %s，上周 = %s 至 %s。\n\n",
		d.Generated.Format("2006-01-02 15:04"), d.From, d.To, d.PrevFrom, d.PrevTo)

	b.WriteString("## 本周概览\n\n")
	if kpis := KPIs(d); len(kpis) == 0 {
		b.WriteString("这个站点还没有同步的数据，也没有体检记录。\n\n")
	} else {
		b.WriteString("| 指标 | 本周 | 上周 | 变化 |\n|---|---|---|---|\n")
		for _, k := range kpis {
			fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", k.Label, k.Cur, k.Prev, k.Change)
		}
		b.WriteString("\n")
	}

	b.WriteString("## 搜索表现\n\n")
	if g := d.GSC; g == nil {
		b.WriteString("没有绑定 Search Console，或者数据还没有同步完。\n\n")
	} else {
		b.WriteString("<!-- chart -->\n\n")
		movers := func(title string, ms []Mover) {
			fmt.Fprintf(&b, "### %s\n\n", title)
			if len(ms) == 0 {
				b.WriteString("没有。\n\n")
				return
			}
			b.WriteString("| 查询 | 本周点击 | 上周点击 | 变化 | 本周排名 |\n|---|---|---|---|---|\n")
			for _, m := range ms {
				fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", cell(m.Query), fmtInt(m.Clicks), fmtInt(m.Prev), signed(m.Clicks-m.Prev, 0), fmtPos(m.Position))
			}
			b.WriteString("\n")
		}
		movers("点击增加最多的查询", g.Up)
		movers("点击减少最多的查询", g.Down)
		b.WriteString("### 差一点进首页的词（近 28 天排名 8–20，展示 ≥ 50）\n\n")
		if len(g.Striking) == 0 {
			b.WriteString("没有。\n\n")
		} else {
			b.WriteString("| 查询 | 展示 | 点击 | 排名 |\n|---|---|---|---|\n")
			for _, r := range g.Striking {
				fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", cell(r.Key), fmtInt(r.Impressions), fmtInt(r.Clicks), fmtPos(r.Position))
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("## 体检\n\n")
	if h := d.Health; h == nil {
		b.WriteString("还没有体检记录。\n\n")
	} else {
		c := h.Latest
		fmt.Fprintf(&b, "健康分 **%d**（P0 %d · P1 %d · P2 %d），体检于 %s，抓取 %d 页（%d 页正常）。\n\n",
			c.Score, c.P0, c.P1, c.P2, time.Unix(c.FinishedAt, 0).Format("2006-01-02 15:04"), c.Pages, c.OKPages)
		p2 := 0
		for _, is := range h.Issues {
			if is.Severity == "p2" {
				p2++
				continue
			}
			line := fmt.Sprintf("- **%s** %s（%d）", strings.ToUpper(is.Severity), is.Title, is.Count)
			if is.New {
				line += " · 新"
			}
			b.WriteString(line + "\n")
		}
		if p2 > 0 {
			fmt.Fprintf(&b, "- 另有 %d 类 P2 问题，见「体检」页。\n", p2)
		}
		if len(h.Issues) == 0 {
			b.WriteString("没有发现问题。\n")
		}
		b.WriteString("\n")
	}

	b.WriteString("## 任务\n\n")
	t := d.Tasks
	if len(t.Passed)+len(t.Failed)+len(t.Open) == 0 {
		b.WriteString("没有任务。可以让 Agent 把建议变成带验收条件的任务。\n\n")
	} else {
		list := func(title, mark string, vs []tasks.View, due bool) {
			if len(vs) == 0 {
				return
			}
			fmt.Fprintf(&b, "### %s\n\n", title)
			for _, v := range vs {
				line := fmt.Sprintf("- %s %s", mark, cell(v.Title))
				if due {
					line += "（到期 " + time.Unix(v.DueAt, 0).Format("2006-01-02") + "）"
				}
				b.WriteString(line + "\n")
				for i, l := range v.Labels {
					state, note := "待检查", ""
					if i < len(v.Outcomes) && v.Outcomes[i].State != "" {
						state, note = stateText[v.Outcomes[i].State], v.Outcomes[i].Note
					}
					fmt.Fprintf(&b, "  - %s：%s %s\n", cell(l), state, cell(note))
				}
			}
			b.WriteString("\n")
		}
		list("本周通过", "✓", t.Passed, false)
		list("本周到期未通过", "✗", t.Failed, false)
		list("进行中", "·", t.Open, true)
	}

	if next := nextSteps(d); len(next) > 0 {
		b.WriteString("## 下周建议\n\n")
		for i, s := range next {
			fmt.Fprintf(&b, "%d. %s\n", i+1, s)
		}
		b.WriteString("\n")
	}
	return b.String()
}

var stateText = map[string]string{tasks.StatePass: "达标", tasks.StateUnmet: "未达标", tasks.StatePending: "待数据"}

// nextSteps are plain follow-ups from the data: P0 issues, near-page-one
// queries and tasks due within a week.
func nextSteps(d Data) []string {
	var out []string
	if h := d.Health; h != nil {
		for _, is := range h.Issues {
			if is.Severity == "p0" {
				out = append(out, "先处理 P0：「"+is.Title+"」（"+fmt.Sprint(is.Count)+"）。")
			}
		}
	}
	if g := d.GSC; g != nil && len(g.Striking) > 0 {
		var qs []string
		for i, r := range g.Striking {
			if i == 3 {
				break
			}
			qs = append(qs, "「"+cell(r.Key)+"」（"+fmtPos(r.Position)+"）")
		}
		out = append(out, "差一点进首页："+strings.Join(qs, "、")+"，可以问 Agent 怎么把它们推进前 10。")
	}
	soon := d.Generated.AddDate(0, 0, 7).Unix()
	for _, v := range d.Tasks.Open {
		if v.DueAt <= soon {
			out = append(out, "任务「"+cell(v.Title)+"」"+time.Unix(v.DueAt, 0).Format("01-02")+" 到期，还没有达标。")
		}
	}
	return out
}

// cell keeps a value on one table row and neutralizes Markdown/HTML
// metacharacters from untrusted text (GSC queries, agent-written task
// titles and notes): "<", ">", "[", "]", "(", ")" become full-width
// lookalikes (the same trick already used for "|"), so third-party
// Markdown readers of the exported .md file cannot turn them into a link,
// raw HTML or (as a side effect) an HTML comment opener ("<!--").
func cell(s string) string {
	return strings.NewReplacer(
		"|", "｜", "\n", " ", "\r", " ",
		"<", "＜", ">", "＞", "[", "［", "]", "］", "(", "（", ")", "）",
	).Replace(s)
}

func fmtInt(v float64) string {
	s := fmt.Sprintf("%.0f", v)
	n := len(s)
	if v < 0 {
		return "-" + fmtInt(-v)
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (n-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func fmtPct(v float64) string { return fmt.Sprintf("%.1f%%", v*100) }

func fmtPos(v float64) string {
	if v <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f", v)
}

func signed(v float64, places int) string {
	s := fmt.Sprintf("%+.*f", places, v)
	if s == "+0" || s == "-0" || s == "+0.0" || s == "-0.0" {
		return "0"
	}
	return s
}

// pct is the relative change, "—" without a baseline.
func pct(cur, prev float64) string {
	if prev <= 0 {
		return "—"
	}
	return signed(math.Round((cur-prev)/prev*1000)/10, 1) + "%"
}

// points is a CTR change in percentage points.
func points(cur, prev float64) string {
	if prev <= 0 && cur <= 0 {
		return "—"
	}
	return signed((cur-prev)*100, 1) + " 个百分点"
}

// posChange: a smaller position is better, so it is shown as "↑ 1.2" (up the page).
func posChange(cur, prev float64) string {
	if cur <= 0 || prev <= 0 {
		return "—"
	}
	d := math.Round((prev-cur)*10) / 10
	switch {
	case d > 0:
		return fmt.Sprintf("↑ %.1f", d)
	case d < 0:
		return fmt.Sprintf("↓ %.1f", -d)
	}
	return "0"
}
