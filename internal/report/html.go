// SPDX-License-Identifier: AGPL-3.0-or-later
package report

import (
	"fmt"
	"html"
	"regexp"
	"strings"

	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

// chartMark is where the HTML version puts the clicks chart.
const chartMark = "<!-- chart -->"

// HTML renders a report as one self-contained page: no scripts, no external
// resources (fonts, styles and charts are inline), so it opens anywhere and
// the app can show it in a sandboxed frame.
func HTML(d Data, md string) string {
	body := mdToHTML(md)
	if parts := strings.SplitN(md, chartMark, 2); len(parts) == 2 && d.GSC != nil && len(d.GSC.Daily) > 0 {
		body = mdToHTML(parts[0]) + clicksChart(d.GSC.Daily, d.From) + mdToHTML(parts[1])
	}
	var cards strings.Builder
	for _, k := range KPIs(d) {
		fmt.Fprintf(&cards, `<div class="card"><div class="k">%s</div><div class="v">%s</div><div class="d">上周 %s · %s</div></div>`,
			html.EscapeString(k.Label), html.EscapeString(k.Cur), html.EscapeString(k.Prev), html.EscapeString(k.Change))
	}
	// The cards (under the title and its note) repeat the overview table,
	// which stays for the Markdown version and screen readers.
	body = strings.Replace(body, "</blockquote>", `</blockquote><div class="cards">`+cards.String()+`</div>`, 1)
	return `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">` +
		`<title>` + html.EscapeString(Title(d)) + `</title><style>` + docCSS + `</style></head><body><div class="wrap">` +
		body + `<p class="foot">SiteMoment Desktop · 本地生成</p></div></body></html>`
}

const docCSS = `:root{--bg:#fcfcfb;--fg:#1d2330;--mut:#5f6878;--line:#e3e5ea;--grid:#eceef2;--acc:#5b45c2;--series:#1399b0;--band:#f0f1f4}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--fg);font:15px/1.7 -apple-system,BlinkMacSystemFont,"PingFang SC","Microsoft YaHei",sans-serif}
.wrap{max-width:920px;margin:0 auto;padding:36px 24px 72px}
h1{font-size:26px;margin:0 0 6px}
h2{font-size:19px;margin:40px 0 12px;padding-bottom:6px;border-bottom:2px solid var(--line);color:var(--acc)}
h3{font-size:15px;margin:22px 0 8px}
blockquote{color:var(--mut);border-left:3px solid var(--line);margin:8px 0 16px;padding:0 12px;font-size:13px}
table{border-collapse:collapse;width:100%;margin:10px 0;font-size:13px;font-variant-numeric:tabular-nums}
th,td{border:1px solid var(--line);padding:6px 9px;text-align:left;vertical-align:top}
th{background:#f5f6f8;font-weight:600}
code{background:#f0f1f4;padding:1px 5px;border-radius:4px;font-size:12px}
ul,ol{padding-left:22px}
.cards{display:grid;grid-template-columns:repeat(auto-fill,minmax(150px,1fr));gap:10px;margin:18px 0 6px}
.card{background:#fff;border:1px solid var(--line);border-radius:10px;padding:12px 14px}
.card .k{font-size:12px;color:var(--mut)}
.card .v{font-size:24px;font-weight:650;line-height:1.3;font-variant-numeric:tabular-nums}
.card .d{font-size:12px;color:var(--mut);font-variant-numeric:tabular-nums}
figure{margin:12px 0 18px}
figcaption{font-size:13px;color:var(--mut);margin-bottom:4px}
svg text{font:11px -apple-system,BlinkMacSystemFont,sans-serif;fill:var(--mut)}
svg .end{fill:var(--fg);font-weight:600}
details{font-size:13px;color:var(--mut)}
.foot{margin-top:48px;color:var(--mut);font-size:12px}
@media print{.wrap{padding:0}h2{break-after:avoid}table,figure{break-inside:avoid}}`

// clicksChart is a 28-day line of daily clicks with the report week shaded,
// a native tooltip per day and a table view underneath.
func clicksChart(days []store.DayPoint, weekFrom string) string {
	const w, h = 860.0, 220.0
	const l, r, t, b = 48.0, 56.0, 12.0, 26.0
	n := len(days)
	hi := 0.0
	for _, p := range days {
		hi = max(hi, p.Clicks)
	}
	top, step := niceMax(hi)
	x := func(i int) float64 {
		if n == 1 {
			return l
		}
		return l + float64(i)/float64(n-1)*(w-l-r)
	}
	y := func(v float64) float64 { return t + (1-v/top)*(h-t-b) }
	var s strings.Builder
	fmt.Fprintf(&s, `<figure><figcaption>每日 GSC 点击（近 28 天，灰底是本周）</figcaption><svg viewBox="0 0 %.0f %.0f" width="100%%" role="img" aria-label="每日 GSC 点击，近 28 天">`, w, h)
	// The report week as a band behind everything.
	for i, p := range days {
		if p.Day >= weekFrom {
			x0 := x(i)
			if i > 0 {
				x0 = (x(i-1) + x(i)) / 2
			}
			fmt.Fprintf(&s, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" fill="var(--band)"/>`, x0, t, w-r-x0, h-t-b)
			break
		}
	}
	for v := 0.0; v <= top+step/2; v += step {
		fmt.Fprintf(&s, `<line x1="%.0f" x2="%.0f" y1="%.1f" y2="%.1f" stroke="var(--grid)" stroke-width="1"/><text x="%.0f" y="%.1f" text-anchor="end">%s</text>`,
			l, w-r, y(v), y(v), l-8, y(v)+4, fmtInt(v))
	}
	for _, i := range uniqueInts(0, (n-1)/3, 2*(n-1)/3, n-1) {
		fmt.Fprintf(&s, `<text x="%.1f" y="%.0f" text-anchor="middle">%s</text>`, x(i), h-8, dayLabel(days[i].Day))
	}
	var area, line strings.Builder
	fmt.Fprintf(&area, "M%.1f,%.1f", x(0), y(0))
	for i, p := range days {
		cmd := "L"
		if i == 0 {
			cmd = "M"
		}
		fmt.Fprintf(&line, "%s%.1f,%.1f", cmd, x(i), y(p.Clicks))
		fmt.Fprintf(&area, "L%.1f,%.1f", x(i), y(p.Clicks))
	}
	fmt.Fprintf(&area, "L%.1f,%.1fZ", x(n-1), y(0))
	fmt.Fprintf(&s, `<path d="%s" fill="var(--series)" fill-opacity="0.1"/>`, area.String())
	fmt.Fprintf(&s, `<path d="%s" fill="none" stroke="var(--series)" stroke-width="2" stroke-linejoin="round" stroke-linecap="round"/>`, line.String())
	last := days[n-1]
	fmt.Fprintf(&s, `<circle cx="%.1f" cy="%.1f" r="4" fill="var(--series)" stroke="var(--bg)" stroke-width="2"/><text class="end" x="%.1f" y="%.1f">%s</text>`,
		x(n-1), y(last.Clicks), x(n-1)+8, y(last.Clicks)+4, fmtInt(last.Clicks))
	// One hover target per day, wider than the line.
	slot := (w - l - r) / float64(max(n-1, 1))
	for i, p := range days {
		fmt.Fprintf(&s, `<rect x="%.1f" y="%.0f" width="%.1f" height="%.0f" fill="transparent"><title>%s：%s 次点击，%s 次展示</title></rect>`,
			x(i)-slot/2, t, slot, h-t-b, html.EscapeString(p.Day), fmtInt(p.Clicks), fmtInt(p.Impressions))
	}
	s.WriteString(`</svg></figure><details><summary>按天的数据</summary><table><thead><tr><th>日期</th><th>点击</th><th>展示</th></tr></thead><tbody>`)
	for _, p := range days {
		fmt.Fprintf(&s, "<tr><td>%s</td><td>%s</td><td>%s</td></tr>", html.EscapeString(p.Day), fmtInt(p.Clicks), fmtInt(p.Impressions))
	}
	s.WriteString("</tbody></table></details>")
	return s.String()
}

// niceMax rounds hi up to a clean axis top and returns it with a tick step (4 steps).
func niceMax(hi float64) (float64, float64) {
	if hi <= 0 {
		return 4, 1
	}
	raw := hi / 4
	mag := 1.0
	for mag*10 <= raw {
		mag *= 10
	}
	for mag > raw {
		mag /= 10
	}
	for _, m := range []float64{1, 2, 2.5, 5, 10} {
		if s := m * mag; s >= raw {
			if s < 1 {
				s = 1
			}
			return s * 4, s
		}
	}
	return hi, hi / 4
}

// dayLabel is the axis label for a "YYYY-MM-DD" day ("MM-DD"), escaped for
// the SVG/XML it is written into. It never panics on a shorter string.
func dayLabel(d string) string {
	if len(d) < 5 {
		return html.EscapeString(d)
	}
	return html.EscapeString(d[5:])
}

func uniqueInts(xs ...int) []int {
	var out []int
	seen := map[int]bool{}
	for _, v := range xs {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

var (
	reH    = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	reLI   = regexp.MustCompile(`^(\s*)[-*]\s+(.*)$`)
	reNLI  = regexp.MustCompile(`^\s*\d+[.、]\s+(.*)$`)
	reBold = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reCode = regexp.MustCompile("`([^`]+)`")
	reSep  = regexp.MustCompile(`^\|[\s:|-]+\|$`)
)

// mdToHTML converts the small Markdown subset reports use (headings,
// paragraphs, bullet and numbered lists with one nested level, tables, bold,
// code, quotes), escaping all text. Links are not turned into anchors. Only
// the literal chart marker is removed here; any other "<!-- -->" in the
// text (e.g. from a GSC query) is just text and gets escaped like anything
// else, not silently stripped by a generic comment-removing regex.
// Ported from craftsail-growth internal/pkg/mdhtml.
func mdToHTML(md string) string {
	md = strings.ReplaceAll(md, chartMark, "")
	lines := strings.Split(md, "\n")
	var out []string
	var open []string // list tags open, innermost last
	inline := func(s string) string {
		s = html.EscapeString(s)
		s = reBold.ReplaceAllString(s, "<strong>$1</strong>")
		return reCode.ReplaceAllString(s, "<code>$1</code>")
	}
	closeTo := func(depth int) {
		for len(open) > depth {
			out = append(out, "</"+open[len(open)-1]+">")
			open = open[:len(open)-1]
		}
	}
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trim := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "|") && i+1 < len(lines) && reSep.MatchString(strings.TrimSpace(lines[i+1])):
			closeTo(0)
			var b strings.Builder
			b.WriteString("<table><thead><tr>")
			for _, c := range splitRow(line) {
				b.WriteString("<th>" + inline(c) + "</th>")
			}
			b.WriteString("</tr></thead><tbody>")
			for i += 2; i < len(lines) && strings.HasPrefix(lines[i], "|"); i++ {
				b.WriteString("<tr>")
				for _, c := range splitRow(lines[i]) {
					b.WriteString("<td>" + inline(c) + "</td>")
				}
				b.WriteString("</tr>")
			}
			i--
			b.WriteString("</tbody></table>")
			out = append(out, b.String())
		case reH.MatchString(line):
			closeTo(0)
			m := reH.FindStringSubmatch(line)
			n := len(m[1])
			out = append(out, fmt.Sprintf("<h%d>%s</h%d>", n, inline(m[2]), n))
		case strings.HasPrefix(trim, ">"):
			closeTo(0)
			out = append(out, "<blockquote>"+inline(strings.TrimSpace(strings.TrimPrefix(trim, ">")))+"</blockquote>")
		case reLI.MatchString(line):
			m := reLI.FindStringSubmatch(line)
			depth := 1
			if len(m[1]) >= 2 {
				depth = 2
			}
			if depth > len(open)+1 {
				depth = len(open) + 1
			}
			closeTo(depth)
			if len(open) < depth {
				out = append(out, "<ul>")
				open = append(open, "ul")
			}
			out = append(out, "<li>"+inline(m[2])+"</li>")
		case reNLI.MatchString(line):
			if len(open) != 1 || open[0] != "ol" {
				closeTo(0)
				out = append(out, "<ol>")
				open = append(open, "ol")
			}
			out = append(out, "<li>"+inline(reNLI.FindStringSubmatch(line)[1])+"</li>")
		case trim == "":
			closeTo(0)
		default:
			closeTo(0)
			out = append(out, "<p>"+inline(trim)+"</p>")
		}
	}
	closeTo(0)
	return strings.Join(out, "\n")
}

func splitRow(line string) []string {
	parts := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}
