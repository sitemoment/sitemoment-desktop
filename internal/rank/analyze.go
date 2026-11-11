// SPDX-License-Identifier: AGPL-3.0-or-later

package rank

import (
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// Thresholds used by the checks.
const (
	titleMin, titleMax = 30, 60
	descMin, descMax   = 70, 160
	minWords           = 300
	minInternalLinks   = 3
	fastMs, slowMs     = 1500, 3000
	maxPageBytes       = 2 << 20
	maxRedirectHops    = 1
)

// checkWeights is how much each check counts in the score. The score is a
// weighted pass rate: pass earns the full weight, warn half, fail nothing,
// and the total is scaled to 0–100. Indexability and HTTPS weigh most
// because failing them hides the page or warns visitors away; cosmetic
// checks weigh least.
var checkWeights = map[string]int{
	"indexable":      5,
	"https":          4,
	"title":          3,
	"description":    3,
	"h1":             3,
	"canonical":      2,
	"viewport":       2,
	"word_count":     2,
	"image_alt":      2,
	"internal_links": 2,
	"http_redirect":  2,
	"robots_txt":     2,
	"sitemap":        2,
	"json_ld":        2,
	"response_time":  2,
	"og_tags":        2,
	"lang":           1,
	"heading_order":  1,
	"redirects":      1,
	"page_size":      1,
	"twitter_card":   1,
}

// Analyze builds the report for one fetched page. pageURL is the requested
// (normalized) URL; resp is the page response and site what Audit learned
// about the rest of the site. It does no I/O.
func Analyze(pageURL string, resp *Response, site SiteInfo) *Report {
	finalURL := resp.FinalURL
	if finalURL == "" {
		finalURL = pageURL
	}
	f := extract(finalURL, resp.Body)
	if xr := resp.Header.Get("X-Robots-Tag"); xr != "" {
		if f.Robots == "" {
			f.Robots = collapse(xr)
		} else {
			f.Robots += ", " + collapse(xr)
		}
	}
	fu, _ := url.Parse(finalURL)
	isHTTPS := fu != nil && strings.EqualFold(fu.Scheme, "https")

	r := &Report{
		URL:        pageURL,
		FinalURL:   finalURL,
		FetchedAt:  time.Now().UTC().Truncate(time.Second),
		StatusCode: resp.StatusCode,
		TimeMs:     resp.TimeMs,
		Bytes:      int64(len(resp.Body)),
		HTTPS:      isHTTPS,
		Page:       f.PageInfo,
		Site:       SiteSummary{RobotsTxt: site.RobotsTxt, Sitemap: site.Sitemap, SitemapURLs: site.SitemapURLs},
	}
	c := &checker{}

	// meta
	c.textLen("title", "Title", f.Title, titleMin, titleMax,
		"Add a <title> that names the page's topic in 30–60 characters.",
		"Rewrite the title to 30–60 characters (it is %d) so search results show it in full.",
		"The title is %d characters, a good length.")
	c.textLen("description", "Meta description", f.Description, descMin, descMax,
		`Add a <meta name="description"> summarising the page in 70–160 characters.`,
		"Rewrite the meta description to 70–160 characters (it is %d) so it is not cut off or too thin.",
		"The meta description is %d characters, a good length.")
	c.canonical(f.Canonical, fu)

	// content
	switch n := len(f.H1); {
	case n == 1:
		c.add("h1", GroupContent, StatusPass, "One H1 heading", "The page has exactly one H1.", n)
	case n == 0:
		c.add("h1", GroupContent, StatusFail, "One H1 heading", "Add one <h1> that states the page's main topic.", n)
	default:
		c.add("h1", GroupContent, StatusWarn, "One H1 heading", fmt.Sprintf("Keep a single <h1> and turn the other %d into <h2> headings.", n-1), n)
	}
	switch {
	case f.headingCount == 0:
		c.add("heading_order", GroupContent, StatusWarn, "Heading order", "Structure the page with headings, starting at <h1> and going down one level at a time.", nil)
	case len(f.headingSkips) == 0:
		c.add("heading_order", GroupContent, StatusPass, "Heading order", "Headings go down one level at a time.", nil)
	default:
		c.add("heading_order", GroupContent, StatusWarn, "Heading order",
			fmt.Sprintf("Don't skip heading levels (found %s); go down one level at a time.", strings.Join(f.headingSkips, ", ")), f.headingSkips)
	}
	if f.Words >= minWords {
		c.add("word_count", GroupContent, StatusPass, "Enough text", fmt.Sprintf("The page has %d words of visible text.", f.Words), f.Words)
	} else {
		c.add("word_count", GroupContent, StatusWarn, "Enough text", fmt.Sprintf("Add more useful text: the page has %d words and pages with at least %d usually rank better.", f.Words, minWords), f.Words)
	}
	switch m := f.ImagesMissingAlt; {
	case m == 0:
		c.add("image_alt", GroupContent, StatusPass, "Image alt text", "Every image has alt text.", m)
	case m*2 > f.Images:
		c.add("image_alt", GroupContent, StatusFail, "Image alt text", fmt.Sprintf(`Add alt text to %d of %d images (use alt="" for purely decorative ones).`, m, f.Images), m)
	default:
		c.add("image_alt", GroupContent, StatusWarn, "Image alt text", fmt.Sprintf(`Add alt text to %d of %d images (use alt="" for purely decorative ones).`, m, f.Images), m)
	}

	// links
	switch n := f.Links.Internal; {
	case n >= minInternalLinks:
		c.add("internal_links", GroupLinks, StatusPass, "Internal links", fmt.Sprintf("The page links to %d pages on the same site.", n), n)
	case n > 0:
		c.add("internal_links", GroupLinks, StatusWarn, "Internal links", fmt.Sprintf("Link to at least %d other pages on your site (found %d) so visitors and crawlers can go further.", minInternalLinks, n), n)
	default:
		c.add("internal_links", GroupLinks, StatusFail, "Internal links", fmt.Sprintf("Link to at least %d other pages on your site so visitors and crawlers can go further.", minInternalLinks), n)
	}
	if resp.Redirects <= maxRedirectHops {
		c.add("redirects", GroupLinks, StatusPass, "Redirect hops", "The URL reaches the page in at most one redirect.", resp.Redirects)
	} else {
		c.add("redirects", GroupLinks, StatusWarn, "Redirect hops", fmt.Sprintf("Link straight to the final URL; it took %d redirects to get there.", resp.Redirects), resp.Redirects)
	}

	// technical
	robots := strings.ToLower(f.Robots)
	if hasDirective(robots, "noindex") || hasDirective(robots, "none") {
		c.add("indexable", GroupTechnical, StatusFail, "Indexable", "Remove noindex from the robots meta tag or X-Robots-Tag header, or search engines will drop this page.", f.Robots)
	} else {
		c.add("indexable", GroupTechnical, StatusPass, "Indexable", "Search engines are allowed to index this page.", nil)
	}
	if f.Lang != "" {
		c.add("lang", GroupTechnical, StatusPass, "Language", fmt.Sprintf("The page declares its language (%s).", f.Lang), f.Lang)
	} else {
		c.add("lang", GroupTechnical, StatusWarn, "Language", `Add a lang attribute to <html>, for example <html lang="en">.`, nil)
	}
	if f.Viewport {
		c.add("viewport", GroupTechnical, StatusPass, "Mobile viewport", "The page sets a viewport for mobile screens.", nil)
	} else {
		c.add("viewport", GroupTechnical, StatusFail, "Mobile viewport", `Add <meta name="viewport" content="width=device-width, initial-scale=1"> so the page works on phones.`, nil)
	}
	if isHTTPS {
		c.add("https", GroupTechnical, StatusPass, "HTTPS", "The page is served over HTTPS.", nil)
	} else {
		c.add("https", GroupTechnical, StatusFail, "HTTPS", "Serve the site over HTTPS; browsers mark plain HTTP pages as not secure.", nil)
	}
	switch {
	case !isHTTPS || (site.HTTPRedirectsToHTTPS != nil && !*site.HTTPRedirectsToHTTPS):
		c.add("http_redirect", GroupTechnical, StatusFail, "HTTP redirects to HTTPS", "Redirect every http:// URL to its https:// version with a 301.", false)
	case site.HTTPRedirectsToHTTPS == nil:
		c.add("http_redirect", GroupTechnical, StatusWarn, "HTTP redirects to HTTPS", "We couldn't load the http:// version; make sure it redirects to https:// with a 301.", nil)
	default:
		c.add("http_redirect", GroupTechnical, StatusPass, "HTTP redirects to HTTPS", "The http:// version redirects to https://.", true)
	}
	switch {
	case !site.RobotsTxt:
		c.add("robots_txt", GroupTechnical, StatusWarn, "robots.txt", "Add a /robots.txt file that allows crawling and lists your sitemap.", false)
	case site.RobotsBlocksRoot:
		c.add("robots_txt", GroupTechnical, StatusFail, "robots.txt", `Remove "Disallow: /" from robots.txt; it blocks search engines from the whole site.`, true)
	default:
		c.add("robots_txt", GroupTechnical, StatusPass, "robots.txt", "robots.txt is reachable and allows crawling.", true)
	}
	if site.Sitemap {
		d := "A sitemap is reachable."
		var v any
		if site.SitemapURLs != nil {
			d = fmt.Sprintf("A sitemap is reachable and lists %d URLs.", *site.SitemapURLs)
			v = *site.SitemapURLs
		}
		c.add("sitemap", GroupTechnical, StatusPass, "Sitemap", d, v)
	} else {
		c.add("sitemap", GroupTechnical, StatusWarn, "Sitemap", "Publish /sitemap.xml (or list it in robots.txt) so search engines find all your pages.", nil)
	}
	switch {
	case f.jsonLDBroken > 0:
		c.add("json_ld", GroupTechnical, StatusFail, "Structured data", fmt.Sprintf("Fix the JSON-LD: %d of %d blocks are not valid JSON, so search engines ignore them.", f.jsonLDBroken, f.jsonLDBlocks), f.JSONLDTypes)
	case f.jsonLDBlocks == 0:
		c.add("json_ld", GroupTechnical, StatusWarn, "Structured data", `Add JSON-LD structured data (for example a WebSite or Article <script type="application/ld+json">) to qualify for rich results.`, nil)
	default:
		c.add("json_ld", GroupTechnical, StatusPass, "Structured data", fmt.Sprintf("JSON-LD is present and valid (%s).", typesOrBlocks(f.JSONLDTypes)), f.JSONLDTypes)
	}

	// social
	og := 0
	for _, v := range []string{f.OG.Title, f.OG.Description, f.OG.Image} {
		if v != "" {
			og++
		}
	}
	switch og {
	case 3:
		c.add("og_tags", GroupSocial, StatusPass, "Open Graph tags", "og:title, og:description and og:image are set for link previews.", og)
	case 0:
		c.add("og_tags", GroupSocial, StatusFail, "Open Graph tags", "Add og:title, og:description and og:image so shared links show a rich preview.", og)
	default:
		c.add("og_tags", GroupSocial, StatusWarn, "Open Graph tags", fmt.Sprintf("Add the missing %s so shared links show a full preview.", strings.Join(missingOG(f.OG), " and ")), og)
	}
	if f.Twitter.Card != "" {
		c.add("twitter_card", GroupSocial, StatusPass, "Twitter card", fmt.Sprintf("twitter:card is set (%s).", f.Twitter.Card), f.Twitter.Card)
	} else {
		c.add("twitter_card", GroupSocial, StatusWarn, "Twitter card", `Add <meta name="twitter:card" content="summary_large_image"> for large previews on X.`, nil)
	}

	// performance
	switch t := resp.TimeMs; {
	case t < fastMs:
		c.add("response_time", GroupPerformance, StatusPass, "Response time", fmt.Sprintf("The page loaded in %d ms.", t), t)
	case t < slowMs:
		c.add("response_time", GroupPerformance, StatusWarn, "Response time", fmt.Sprintf("Speed up the server or add caching: the page took %d ms (aim for under 1.5 s).", t), t)
	default:
		c.add("response_time", GroupPerformance, StatusFail, "Response time", fmt.Sprintf("Speed up the server or add caching: the page took %d ms (aim for under 1.5 s).", t), t)
	}
	if r.Bytes < maxPageBytes {
		c.add("page_size", GroupPerformance, StatusPass, "Page size", fmt.Sprintf("The HTML is %d KB.", (r.Bytes+1023)/1024), r.Bytes)
	} else {
		c.add("page_size", GroupPerformance, StatusFail, "Page size", fmt.Sprintf("Trim the HTML below 2 MB (it is %.1f MB) by removing inlined data and unused markup.", float64(r.Bytes)/(1<<20)), r.Bytes)
	}

	r.Checks = c.checks
	r.Score = score(c.checks)
	return r
}

// checker accumulates checks in report order.
type checker struct{ checks []Check }

func (c *checker) add(id, group, status, title, detail string, value any) {
	c.checks = append(c.checks, Check{ID: id, Group: group, Status: status, Title: title, Detail: detail, Value: value})
}

// textLen is the shared title/description length check.
func (c *checker) textLen(id, title, s string, lo, hi int, missing, badLen, good string) {
	n := utf8.RuneCountInString(s)
	switch {
	case n == 0:
		c.add(id, GroupMeta, StatusFail, title, missing, 0)
	case n < lo || n > hi:
		c.add(id, GroupMeta, StatusWarn, title, fmt.Sprintf(badLen, n), n)
	default:
		c.add(id, GroupMeta, StatusPass, title, fmt.Sprintf(good, n), n)
	}
}

func (c *checker) canonical(canon string, page *url.URL) {
	const title = "Canonical URL"
	if canon == "" {
		c.add("canonical", GroupMeta, StatusWarn, title, `Add <link rel="canonical"> pointing at this page's preferred URL.`, nil)
		return
	}
	cu, err := url.Parse(canon)
	if err != nil || page == nil || (cu.Scheme != "http" && cu.Scheme != "https") {
		c.add("canonical", GroupMeta, StatusFail, title, "Fix the canonical link; it is not a valid absolute URL.", canon)
		return
	}
	if hostKey(cu.Hostname()) != hostKey(page.Hostname()) {
		c.add("canonical", GroupMeta, StatusFail, title, fmt.Sprintf("Point the canonical link at this site; it names %s, which tells search engines to index that site instead.", cu.Hostname()), canon)
		return
	}
	if sameURL(cu, page) {
		c.add("canonical", GroupMeta, StatusPass, title, "The canonical link points at this page.", canon)
	} else {
		c.add("canonical", GroupMeta, StatusPass, title, "The canonical link points at another page on this site; make sure that is intended.", canon)
	}
}

// score is the weighted pass rate, 0–100.
func score(checks []Check) int {
	var got, total float64
	for _, ch := range checks {
		w := float64(checkWeights[ch.ID])
		total += w
		switch ch.Status {
		case StatusPass:
			got += w
		case StatusWarn:
			got += w / 2
		}
	}
	if total == 0 {
		return 0
	}
	return int(math.Round(100 * got / total))
}

func sameURL(a, b *url.URL) bool {
	pa, pb := a.EscapedPath(), b.EscapedPath()
	if pa == "" {
		pa = "/"
	}
	if pb == "" {
		pb = "/"
	}
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.TrimSuffix(pa, "/") == strings.TrimSuffix(pb, "/") &&
		hostKey(a.Host) == hostKey(b.Host) && a.RawQuery == b.RawQuery
}

// hasDirective reports whether a comma-separated robots value contains d,
// also in the bot-prefixed form "googlebot: noindex".
func hasDirective(robots, d string) bool {
	for _, part := range strings.Split(robots, ",") {
		part = strings.TrimSpace(part)
		if i := strings.LastIndexByte(part, ':'); i >= 0 {
			part = strings.TrimSpace(part[i+1:])
		}
		if part == d {
			return true
		}
	}
	return false
}

func missingOG(og OpenGraph) []string {
	var out []string
	if og.Title == "" {
		out = append(out, "og:title")
	}
	if og.Description == "" {
		out = append(out, "og:description")
	}
	if og.Image == "" {
		out = append(out, "og:image")
	}
	return out
}

func typesOrBlocks(types []string) string {
	if len(types) == 0 {
		return "no @type"
	}
	return strings.Join(types, ", ")
}
