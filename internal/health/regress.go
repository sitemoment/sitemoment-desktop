// SPDX-License-Identifier: AGPL-3.0-or-later
package health

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/sitemoment/sitemoment-desktop/internal/rank"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

// pageRow is what the memory keeps of a crawled page.
func pageRow(p rank.CrawledPage, now int64) store.Page {
	title := p.Title
	if r := []rune(title); len(r) > 200 {
		title = string(r[:200])
	}
	return store.Page{URL: p.URL, FinalURL: p.FinalURL, Status: p.Status, OK: p.OK() && p.Redirects == 0, Title: title,
		Canonical: p.Canonical, Noindex: p.Noindex, InSitemap: p.InSitemap, Error: p.Error, LastSeen: now}
}

// canonicalOf is where a page points search engines: its canonical, else itself.
func canonicalOf(canonical, finalURL string) string {
	c := canonical
	if c == "" {
		c = finalURL
	}
	return strings.TrimSuffix(c, "/")
}

// regressed lists the crawled pages that got worse since the previous check:
// noindex that was not there, a canonical pointing somewhere new, and the
// previously OK pages that now fail (to be traced, with unseen ones).
func regressed(prev map[string]store.Page, res *rank.CrawlResult) (noindex, canonical, failing []string) {
	for _, p := range res.Pages {
		old, ok := prev[p.URL]
		if !ok || !old.OK || p.Error != "" {
			continue
		}
		switch {
		case p.Status >= 400:
			failing = append(failing, p.URL)
		case p.OK() && p.Redirects == 0:
			if p.Noindex && !old.Noindex {
				noindex = append(noindex, p.URL)
			}
			if !p.Noindex && !old.Noindex && canonicalOf(p.Canonical, p.FinalURL) != canonicalOf(old.Canonical, old.FinalURL) {
				canonical = append(canonical, fmt.Sprintf("%s：%s → %s", p.URL, orSelf(old.Canonical), orSelf(p.Canonical)))
			}
		}
	}
	return noindex, canonical, failing
}

func orSelf(c string) string {
	if c == "" {
		return "（无，指向自己）"
	}
	return c
}

// unseenOK lists previously OK pages this crawl did not reach, at most
// limit. Pages robots.txt now disallows are left out: rechecking them would
// fetch a URL our crawler itself would skip.
func unseenOK(prev map[string]store.Page, res *rank.CrawlResult, limit int) []string {
	crawled := make(map[string]bool, len(res.Pages))
	for _, p := range res.Pages {
		crawled[p.URL] = true
	}
	var out []string
	for u, p := range prev {
		if p.OK && !crawled[u] && res.Allowed(u) {
			out = append(out, u)
		}
	}
	sort.Strings(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// lostPages traces urls (previously OK pages that fail now or were not
// reached) and returns the issue lines of those that end on an error without
// a redirect, plus memory rows for every traced URL (what the trace cannot
// see, such as the title, is kept from prev). URLs robots.txt now disallows
// are skipped: this traces with a direct fetch, outside the crawler that
// enforces robots.txt itself.
func lostPages(ctx context.Context, f *rank.Fetcher, urls []string, res *rank.CrawlResult, prev map[string]store.Page, now int64) ([]string, []store.Page) {
	allowed := urls[:0:0]
	for _, u := range urls {
		if res.Allowed(u) {
			allowed = append(allowed, u)
		}
	}
	urls = allowed
	if len(urls) == 0 {
		return nil, nil
	}
	var lines []string
	var rows []store.Page
	for _, ch := range rank.CheckURLs(ctx, f, urls, res.Pages, 2) {
		if ch.Error != "" {
			continue
		}
		row := prev[ch.URL]
		row.URL, row.FinalURL, row.Status, row.Error, row.LastSeen = ch.URL, ch.FinalURL, ch.FinalStatus, "", now
		row.OK = ch.FinalStatus < 400 && !ch.Redirected
		rows = append(rows, row)
		if ch.Lost {
			line := fmt.Sprintf("%s → %d", ch.URL, ch.FinalStatus)
			if ch.Suggest != "" {
				line += "（建议 301 到 " + ch.Suggest + "）"
			}
			lines = append(lines, line)
		}
	}
	return lines, rows
}
