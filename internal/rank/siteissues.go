// SPDX-License-Identifier: AGPL-3.0-or-later

package rank

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"unicode"
)

// Issue severities: p0 loses traffic now, p1 holds rankings back, p2 is housekeeping.
const (
	SeverityP0 = "p0"
	SeverityP1 = "p1"
	SeverityP2 = "p2"
)

// Issue is one site-level finding from a crawl.
type Issue struct {
	ID       string   `json:"id"`
	Severity string   `json:"severity"`
	Title    string   `json:"title"`
	Detail   string   `json:"detail"`
	Count    int      `json:"count"`
	URLs     []string `json:"urls"`
	// Groups lists URL groups for issues about sets of pages (duplicates, cannibalization).
	Groups []IssueGroup `json:"groups,omitempty"`
}

// IssueGroup is a set of pages sharing one value.
type IssueGroup struct {
	Value string   `json:"value"`
	URLs  []string `json:"urls"`
	// Total is the group size before URLs was capped.
	Total int `json:"total"`
}

const (
	issueURLCap      = 50
	issueGroupCap    = 30
	issueGroupURLCap = 20
	// minPhraseWords is the shortest shared leading phrase reported as cannibalization,
	// of which at least minContentWords must be neither connectors nor generic verbs.
	minPhraseWords  = 3
	minContentWords = 2
)

// SiteIssues finds site-level problems in a crawl, most severe first.
func SiteIssues(r *CrawlResult) []Issue {
	var out []Issue
	add := func(id, sev, title, detail string, urls []string, groups []IssueGroup) {
		n := len(urls)
		for _, g := range groups {
			n += len(g.URLs)
		}
		if n == 0 && id != "no_sitemap" && id != "robots_blocks_site" && id != "robots_server_error" {
			return
		}
		count := len(urls)
		if len(groups) > 0 {
			count = len(groups)
		}
		out = append(out, Issue{ID: id, Severity: sev, Title: title, Detail: detail, Count: count, URLs: capList(urls, issueURLCap), Groups: capGroups(groups)})
	}

	if r.RobotsStatus >= 500 {
		add("robots_server_error", SeverityP0, "robots.txt answers with a server error",
			fmt.Sprintf("robots.txt returned HTTP %d. Google treats that as \"crawl nothing\" and stops crawling the site until it answers 200 (or 404) again.", r.RobotsStatus), nil, nil)
	}
	if r.RobotsBlocksRoot {
		add("robots_blocks_site", SeverityP0, "robots.txt blocks the whole site",
			"robots.txt disallows / for all crawlers; search engines cannot crawl any page.", nil, nil)
	}
	if len(r.Sitemaps) == 0 {
		add("no_sitemap", SeverityP1, "No sitemap found",
			"Neither robots.txt nor /sitemap.xml points to a readable sitemap; submit one so new pages are found quickly.", nil, nil)
	}

	crawled := map[string]bool{}
	for _, p := range r.Pages {
		if u, err := url.Parse(p.URL); err == nil {
			crawled[strings.Trim(u.Path, "/")] = true
		}
	}
	var sitemapBroken, sitemapRedirect, sitemapNoindex, sitemapCanonical, brokenLinks, notInSitemap, missingTitle, failed []string
	for _, p := range r.Pages {
		switch {
		case p.Error != "":
			failed = append(failed, p.URL)
		case p.InSitemap && p.Status >= 400:
			sitemapBroken = append(sitemapBroken, p.URL)
		case p.LinkedFrom != "" && p.Status >= 400:
			brokenLinks = append(brokenLinks, fmt.Sprintf("%s (linked from %s)", p.URL, p.LinkedFrom))
		}
		if p.Error != "" || p.Status >= 400 {
			continue
		}
		if p.InSitemap && p.Redirects > 0 {
			sitemapRedirect = append(sitemapRedirect, p.URL+" → "+p.FinalURL)
		}
		if !p.OK() {
			continue
		}
		if p.InSitemap && p.Noindex {
			sitemapNoindex = append(sitemapNoindex, p.URL)
		}
		if p.InSitemap && !p.Noindex && p.Canonical != "" && !sameURLString(p.Canonical, p.FinalURL) {
			sitemapCanonical = append(sitemapCanonical, p.URL+" → "+p.Canonical)
		}
		if !p.InSitemap && p.Redirects == 0 && len(r.Sitemaps) > 0 && p.Indexable() && p.Depth > 0 && !isPageN(p.URL, crawled) {
			notInSitemap = append(notInSitemap, p.URL)
		}
		if p.Title == "" {
			missingTitle = append(missingTitle, p.URL)
		}
	}

	add("sitemap_broken", SeverityP0, "Sitemap lists pages that return errors",
		"These sitemap URLs answer 4xx/5xx. Restore them, 301-redirect them to the closest live page, or remove them from the sitemap.", sitemapBroken, nil)
	add("broken_internal_links", SeverityP1, "Internal links point to error pages",
		"Fix or remove these links; each one wastes crawl budget and sends visitors to a dead end.", brokenLinks, nil)
	add("sitemap_redirect", SeverityP1, "Sitemap lists redirecting URLs",
		"List the final URL in the sitemap instead of one that redirects.", sitemapRedirect, nil)
	add("sitemap_noindex", SeverityP1, "Sitemap lists noindex pages",
		"Either remove noindex or drop these pages from the sitemap; the two signals contradict each other.", sitemapNoindex, nil)
	add("sitemap_canonical_elsewhere", SeverityP2, "Sitemap pages canonicalize to other URLs",
		"A sitemap should list canonical URLs only; list the canonical target or fix the canonical tag.", sitemapCanonical, nil)
	add("missing_title", SeverityP1, "Pages without a title", "Give every indexable page a unique <title>.", missingTitle, nil)

	idx := indexablePages(r.Pages)
	add("duplicate_title", SeverityP1, "Pages share the same title",
		"Each page needs its own title; identical titles make search engines pick one page and drop the others.", nil, dropVariants(duplicates(idx, func(p CrawledPage) []string { return []string{p.Title} })))
	add("duplicate_h1", SeverityP2, "Pages share the same H1",
		"Make each page's H1 describe what is unique about it.", nil, dropVariants(duplicates(idx, func(p CrawledPage) []string { return p.H1 })))
	clashes, overlaps := splitBySection(dropVariants(cannibalization(idx, r.Host)))
	add("cannibalization", SeverityP1, "Pages compete for the same phrase",
		"These pages open their title or H1 with the same phrase, so they target the same query and split its ranking. Pick one page for the phrase and rewrite the others around a different angle.", nil, clashes)
	add("overlapping_posts", SeverityP2, "Articles overlap on the same phrase",
		"These articles lead with the same phrase. Check they answer different questions; merge near-duplicates into one stronger page.", nil, overlaps)
	add("not_in_sitemap", SeverityP2, "Crawlable pages missing from the sitemap",
		"These indexable pages are linked on the site but not listed in any sitemap: "+sectionSummary(notInSitemap)+". Add them, or noindex them if they should not rank.", notInSitemap, nil)
	add("fetch_failed", SeverityP2, "Pages that could not be fetched",
		"These URLs timed out or failed (DNS, TLS, size); check them by hand.", failed, nil)

	rank := map[string]int{SeverityP0: 0, SeverityP1: 1, SeverityP2: 2}
	sort.SliceStable(out, func(i, j int) bool { return rank[out[i].Severity] < rank[out[j].Severity] })
	return out
}

func indexablePages(pages []CrawledPage) []CrawledPage {
	var out []CrawledPage
	seen := map[string]bool{}
	for _, p := range pages {
		if p.Indexable() && !seen[p.FinalURL] {
			seen[p.FinalURL] = true
			out = append(out, p)
		}
	}
	return out
}

// duplicates groups pages by every value of key (case- and space-insensitive).
func duplicates(pages []CrawledPage, key func(CrawledPage) []string) []IssueGroup {
	by := map[string][]string{}
	label := map[string]string{}
	for _, p := range pages {
		for _, v := range key(p) {
			k := strings.ToLower(collapse(v))
			if k == "" {
				continue
			}
			if !containsString(by[k], p.FinalURL) {
				by[k] = append(by[k], p.FinalURL)
				label[k] = v
			}
		}
	}
	var out []IssueGroup
	for k, urls := range by {
		if len(urls) > 1 {
			out = append(out, IssueGroup{Value: label[k], URLs: urls})
		}
	}
	sortGroups(out)
	return out
}

// connectors never end a shared phrase: "character ai vs" is a pattern, not a target.
var connectors = map[string]bool{
	"a": true, "an": true, "the": true, "and": true, "or": true, "vs": true, "versus": true, "for": true, "to": true,
	"of": true, "in": true, "on": true, "with": true, "that": true, "who": true, "your": true, "you": true, "how": true,
	"is": true, "are": true, "best": true, "top": true, "new": true, "free": true, "-": true,
}

// cannibalization groups pages whose title or H1, with the site's brand words
// removed, starts with the same phrase of at least minPhraseWords words
// (trailing connector words dropped).
func cannibalization(pages []CrawledPage, host string) []IssueGroup {
	brand := brandWords(pages, host)
	type entry struct {
		url   string
		words []string
	}
	var entries []entry
	for _, p := range pages {
		texts := append([]string{p.Title}, p.H1...)
		for _, t := range texts {
			if w := stripBrand(words(t), brand); len(w) >= minPhraseWords {
				entries = append(entries, entry{p.FinalURL, w})
			}
		}
	}
	by := map[string][]string{}
	for i := range entries {
		for j := i + 1; j < len(entries); j++ {
			a, b := entries[i], entries[j]
			// Identical texts are already reported as duplicate titles / H1s.
			if a.url == b.url || slicesEqual(a.words, b.words) {
				continue
			}
			phrase := sharedLead(a.words, b.words)
			if len(phrase) < minPhraseWords || contentWords(phrase) < minContentWords {
				continue
			}
			k := strings.Join(phrase, " ")
			for _, u := range []string{a.url, b.url} {
				if !containsString(by[k], u) {
					by[k] = append(by[k], u)
				}
			}
		}
	}
	// Keep the longest phrase per page set: "ai character chat" and "ai character" name the same clash.
	var out []IssueGroup
	for k, urls := range by {
		sort.Strings(urls)
		out = append(out, IssueGroup{Value: k, URLs: urls})
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i].Value) > len(out[j].Value) })
	var kept []IssueGroup
	for _, g := range out {
		dup := false
		for _, k := range kept {
			if strings.HasPrefix(k.Value, g.Value) && sameSet(k.URLs, g.URLs) {
				dup = true
				break
			}
		}
		if !dup {
			kept = append(kept, g)
		}
	}
	sortGroups(kept)
	return kept
}

// genericWords open how-to series ("how to use …", "how to write …") without naming a topic.
var genericWords = map[string]bool{
	"use": true, "using": true, "write": true, "make": true, "create": true, "get": true, "choose": true, "pick": true,
	"find": true, "build": true, "start": true, "add": true, "set": true, "turn": true, "try": true, "learn": true,
	"guide": true, "tips": true, "ways": true, "why": true, "what": true, "when": true, "do": true, "does": true,
}

func contentWords(ws []string) int {
	n := 0
	for _, w := range ws {
		if !connectors[w] && !genericWords[w] {
			n++
		}
	}
	return n
}

// articleSections hold posts; overlap inside one of them is milder than two key pages clashing.
var articleSections = map[string]bool{
	"blog": true, "posts": true, "post": true, "articles": true, "article": true, "news": true, "guides": true, "guide": true,
	"tag": true, "tags": true, "category": true, "categories": true, "docs": true, "help": true, "learn": true, "resources": true,
}

// splitBySection separates groups whose pages all sit in one article section.
func splitBySection(groups []IssueGroup) (clashes, overlaps []IssueGroup) {
	for _, g := range groups {
		section, same := "", true
		for i, raw := range g.URLs {
			seg := ""
			if u, err := url.Parse(raw); err == nil {
				seg = strings.SplitN(strings.Trim(strings.ToLower(u.Path), "/"), "/", 2)[0]
			}
			if i == 0 {
				section = seg
			} else if seg != section {
				same = false
			}
		}
		if same && articleSections[section] {
			overlaps = append(overlaps, g)
		} else {
			clashes = append(clashes, g)
		}
	}
	return clashes, overlaps
}

// sharedLead is the common leading words of a and b without trailing connectors.
func sharedLead(a, b []string) []string {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	for n > 0 && connectors[a[n-1]] {
		n--
	}
	return a[:n]
}

// brandWords are the site name: the host's labels (heartie.net → heartie) plus
// any word that opens or closes at least half the titles ("… — Heartie").
// Plain frequency would also catch topic words like "ai" on an AI site.
func brandWords(pages []CrawledPage, host string) map[string]bool {
	out := map[string]bool{}
	labels := strings.Split(strings.TrimPrefix(host, "www."), ".")
	if len(labels) > 1 {
		for _, w := range words(strings.Join(labels[:len(labels)-1], " ")) {
			out[w] = true
		}
	}
	if len(pages) < 3 {
		return out
	}
	count := map[string]int{}
	for _, p := range pages {
		ws := words(p.Title)
		if len(ws) == 0 {
			continue
		}
		count[ws[0]]++
		if last := ws[len(ws)-1]; last != ws[0] {
			count[last]++
		}
	}
	for w, n := range count {
		if 2*n >= len(pages) && !connectors[w] {
			out[w] = true
		}
	}
	return out
}

func stripBrand(ws []string, brand map[string]bool) []string {
	var out []string
	for _, w := range ws {
		if !brand[w] {
			out = append(out, w)
		}
	}
	return out
}

// words lower-cases s and splits it on anything that is not a letter, digit or apostrophe.
func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'')
	})
}

// sectionSummary names the top path sections of urls, e.g. "/role/ (24), /tag/ (3)".
func sectionSummary(urls []string) string {
	count := map[string]int{}
	for _, raw := range urls {
		u, err := url.Parse(raw)
		if err != nil {
			continue
		}
		seg := strings.SplitN(strings.Trim(u.Path, "/"), "/", 2)[0]
		key := "/"
		if seg != "" {
			key = "/" + seg + "/"
		}
		count[key]++
	}
	keys := make([]string, 0, len(count))
	for k := range count {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if count[keys[i]] != count[keys[j]] {
			return count[keys[i]] > count[keys[j]]
		}
		return keys[i] < keys[j]
	})
	parts := []string{}
	for i, k := range keys {
		if i == 5 {
			break
		}
		parts = append(parts, fmt.Sprintf("%s (%d)", k, count[k]))
	}
	return strings.Join(parts, ", ")
}

func sortGroups(gs []IssueGroup) {
	sort.Slice(gs, func(i, j int) bool {
		if len(gs[i].URLs) != len(gs[j].URLs) {
			return len(gs[i].URLs) > len(gs[j].URLs)
		}
		return gs[i].Value < gs[j].Value
	})
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, x := range a {
		if !containsString(b, x) {
			return false
		}
	}
	return true
}

func capList(l []string, n int) []string {
	if len(l) > n {
		return l[:n]
	}
	if l == nil {
		return []string{}
	}
	return l
}

func capGroups(g []IssueGroup) []IssueGroup {
	if len(g) > issueGroupCap {
		g = g[:issueGroupCap]
	}
	for i := range g {
		g[i].Total = len(g[i].URLs)
		g[i].URLs = capList(g[i].URLs, issueGroupURLCap)
	}
	return g
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// dropVariants removes groups whose pages are one page in several languages
// (/amy, /es/amy, /zh-hant/amy) or one paginated list (/blog, /blog/2, /blog/3):
// sharing a title or H1 is expected there.
func dropVariants(groups []IssueGroup) []IssueGroup {
	var out []IssueGroup
	for _, g := range groups {
		base, same := "", true
		for i, raw := range g.URLs {
			k := variantKey(raw)
			if i == 0 {
				base = k
			} else if k != base {
				same = false
				break
			}
		}
		if !same {
			out = append(out, g)
		}
	}
	return out
}

// variantKey is a URL's path without a leading locale segment or a trailing page number.
func variantKey(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	segs := strings.Split(strings.Trim(strings.ToLower(u.Path), "/"), "/")
	if len(segs) > 0 && isLocale(segs[0]) {
		segs = segs[1:]
	}
	for len(segs) > 0 && isDigits(segs[len(segs)-1]) {
		segs = segs[:len(segs)-1]
	}
	if n := len(segs); n >= 1 && segs[n-1] == "page" {
		segs = segs[:n-1]
	}
	return strings.Join(segs, "/")
}

// isLocale matches language path prefixes: es, pt-br, zh-hant, zh_cn.
func isLocale(seg string) bool {
	lang, region, hasRegion := strings.Cut(strings.ReplaceAll(seg, "_", "-"), "-")
	if len(lang) != 2 || !isLetters(lang) {
		return false
	}
	return !hasRegion || (len(region) >= 2 && len(region) <= 4 && isLetters(region))
}

func isLetters(s string) bool {
	for _, r := range s {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return s != ""
}

// isPageN reports whether raw is page N of a crawled list (/blog/2 next to /blog);
// list pages beyond the first rarely belong in a sitemap.
func isPageN(raw string, crawled map[string]bool) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	n := len(segs)
	if n < 2 || !isDigits(segs[n-1]) || segs[n-1] == "" {
		return false
	}
	base := segs[:n-1]
	if base[len(base)-1] == "page" {
		base = base[:len(base)-1]
	}
	return crawled[strings.Join(base, "/")]
}
