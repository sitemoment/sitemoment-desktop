// SPDX-License-Identifier: AGPL-3.0-or-later

// Package health is the daily health check of a site: a crawl (robots.txt
// respected) graded with the SEO rules of internal/rank, regressions against
// what earlier checks remembered (pages that used to work, new noindex,
// changed canonicals), Search Console drops over 28 days, and URL
// Inspection of pages that look unindexed. Results go to the crawls, issues
// and pages tables.
package health

import (
	"fmt"
	"strings"

	"github.com/sitemoment/sitemoment-desktop/internal/rank"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

// Issue codes the health check adds to rank.SiteIssues.
const (
	CodeLost          = "lost_pages"
	CodeNewNoindex    = "new_noindex"
	CodeCanonical     = "canonical_changed"
	CodeClicksDrop    = "gsc_clicks_drop"
	CodeImprDrop      = "gsc_impressions_drop"
	CodeNotIndexed    = "not_indexed"
	CodeRobotsBlocked = "robots_blocks_site"
)

// text is an issue's Chinese title and advice.
type text struct{ title, detail string }

var texts = map[string]text{
	"robots_server_error":         {"robots.txt 返回服务器错误", "robots.txt 返回 5xx 时，谷歌按「整站都不能抓」处理，会暂停抓取，直到它重新返回 200（或 404）。先修好 robots.txt。"},
	"robots_blocks_site":          {"robots.txt 屏蔽了整站", "robots.txt 对所有爬虫禁止了 /，搜索引擎抓不到任何页面。除非是故意的，立刻删掉这条 Disallow。"},
	"no_sitemap":                  {"没有找到 sitemap", "robots.txt 和 /sitemap.xml 都没有可读的 sitemap。提交一个 sitemap，新页面才能更快被发现。"},
	"sitemap_broken":              {"sitemap 里有报错的页面", "这些 sitemap 网址返回 4xx/5xx。恢复页面、301 到最接近的正常页面，或者从 sitemap 里删掉。"},
	"broken_internal_links":       {"站内链接指向错误页", "修好或删掉这些链接：每一个都浪费抓取预算，也把访客带进死胡同。"},
	"sitemap_redirect":            {"sitemap 里有跳转的网址", "sitemap 里要写跳转后的最终网址。"},
	"sitemap_noindex":             {"sitemap 里有 noindex 页面", "要么去掉 noindex，要么把页面移出 sitemap；两个信号互相矛盾。"},
	"sitemap_canonical_elsewhere": {"sitemap 页面的 canonical 指向别处", "sitemap 只放规范网址：改成列出 canonical 指向的网址，或者修正 canonical。"},
	"missing_title":               {"页面缺少标题", "每个要排名的页面都需要独一无二的 <title>。"},
	"duplicate_title":             {"多个页面标题相同", "每个页面要有自己的标题；标题相同时搜索引擎只会挑一个页面，其余的被丢掉。"},
	"duplicate_h1":                {"多个页面 H1 相同", "让每个页面的 H1 写出它独有的内容。"},
	"cannibalization":             {"页面自相竞争（抢同一个词）", "这些页面的标题或 H1 以同一个短语开头，在抢同一个查询。选一个页面主打这个短语，其余的换角度重写。"},
	"overlapping_posts":           {"文章主题重叠", "这些文章以同一个短语开头。确认它们回答的是不同的问题；几乎重复的合并成一篇更强的。"},
	"not_in_sitemap":              {"能抓到但没进 sitemap 的页面", "这些可索引的页面站内有链接，但不在任何 sitemap 里。加进去；不想排名的就加 noindex。"},
	"fetch_failed":                {"抓取失败的页面", "这些网址超时或失败（DNS、TLS、太大），请手工检查。"},
	CodeLost:                      {"以前正常、现在报错的页面", "这些网址之前的体检返回正常，现在报错且没有跳转。恢复页面，或者 301 到最接近的正常页面（括号里是建议的目标）。"},
	CodeNewNoindex:                {"新出现的 noindex", "这些页面上次体检还能被索引，现在带了 noindex（meta robots 或 X-Robots-Tag），会从谷歌里消失。不是故意的就立刻去掉。"},
	CodeCanonical:                 {"canonical 变了", "这些页面的 canonical 和上次体检不一样。确认是有意的；指错了会让谷歌改收录别的网址。"},
	CodeClicksDrop:                {"谷歌点击明显下降", "先用 GSC 数据看哪些查询和页面掉得最多，再对照这段时间的改动（改版、改标题、删页面、换网址）。"},
	CodeImprDrop:                  {"谷歌展示明显下降", "展示下降通常是排名或收录出了问题：看掉得最多的查询排名变了多少，以及「谷歌没有收录的页面」。"},
	CodeNotIndexed:                {"谷歌没有收录的页面", "Search Console 网址检查说这些可索引的页面不在谷歌上（括号里是原因）。先确认内容有独特价值、站内有链接，再在 Search Console 请求编入索引。"},
}

// Text is the Chinese title and advice of an issue code (fallbacks when unknown).
func Text(code, title, detail string) (string, string) {
	if t, ok := texts[code]; ok {
		return t.title, t.detail
	}
	return title, detail
}

// fromRank converts rank's site issues; groups become one line per group.
func fromRank(in []rank.Issue) []store.Issue {
	out := make([]store.Issue, 0, len(in))
	for _, is := range in {
		title, detail := Text(is.ID, is.Title, is.Detail)
		if is.ID == "not_in_sitemap" {
			detail = is.Detail // names the sections ("/role/ (24)"), English is fine there
		}
		urls := append([]string{}, is.URLs...)
		for _, g := range is.Groups {
			line := g.Value + "：" + strings.Join(g.URLs, "、")
			if g.Total > len(g.URLs) {
				line += fmt.Sprintf(" 等 %d 个", g.Total)
			}
			urls = append(urls, line)
		}
		// Site-wide findings (no sitemap, robots.txt blocks the site) count as one.
		out = append(out, store.Issue{Code: is.ID, Severity: is.Severity, Title: title, Detail: detail, Count: max(is.Count, 1), URLs: urls})
	}
	return out
}

func newIssue(code, severity string, urls []string) store.Issue {
	title, detail := Text(code, code, "")
	return store.Issue{Code: code, Severity: severity, Title: title, Detail: detail, Count: len(urls), URLs: capURLs(urls)}
}

func capURLs(urls []string) []string {
	if len(urls) > 50 {
		return urls[:50]
	}
	if urls == nil {
		return []string{}
	}
	return urls
}

// Grade counts issues by severity and scores the site: 100, minus 25 per
// P0, 8 per P1 and 2 per P2 issue type (not per URL), at least 0.
func Grade(issues []store.Issue) (p0, p1, p2, score int) {
	for _, is := range issues {
		switch is.Severity {
		case rank.SeverityP0:
			p0++
		case rank.SeverityP1:
			p1++
		default:
			p2++
		}
	}
	return p0, p1, p2, max(0, 100-25*p0-8*p1-2*p2)
}
