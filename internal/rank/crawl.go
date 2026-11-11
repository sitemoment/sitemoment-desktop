// SPDX-License-Identifier: AGPL-3.0-or-later

package rank

import (
	"context"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

// CrawlOptions bounds a site crawl. Zero fields take the defaults.
type CrawlOptions struct {
	// MaxPages is the number of URLs fetched (default 200, at most 1000).
	MaxPages int
	// MaxDepth is link hops from the start page (default 5).
	MaxDepth int
	// Concurrency is parallel fetches (default 3, at most 8).
	Concurrency int
	// Delay is the pause each worker takes after a fetch (default 150 ms).
	Delay time.Duration
	// Budget is the whole crawl's time limit (default 3 minutes).
	Budget time.Duration
	// SitemapLimit caps the sitemap URLs read for membership checks (default 5000).
	SitemapLimit int
}

func (o CrawlOptions) withDefaults() CrawlOptions {
	if o.MaxPages <= 0 {
		o.MaxPages = 200
	}
	o.MaxPages = min(o.MaxPages, 1000)
	if o.MaxDepth <= 0 {
		o.MaxDepth = 5
	}
	if o.Concurrency <= 0 {
		o.Concurrency = 3
	}
	o.Concurrency = min(o.Concurrency, 8)
	if o.Delay == 0 {
		o.Delay = 150 * time.Millisecond
	}
	if o.Budget <= 0 {
		o.Budget = 3 * time.Minute
	}
	if o.SitemapLimit <= 0 {
		o.SitemapLimit = 5000
	}
	return o
}

// CrawledPage is what one URL of the site returned.
type CrawledPage struct {
	URL      string `json:"url"`
	FinalURL string `json:"finalUrl"`
	Status   int    `json:"status"`
	// Redirects is the number of hops to FinalURL.
	Redirects   int      `json:"redirects"`
	HTML        bool     `json:"html"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	H1          []string `json:"h1"`
	Canonical   string   `json:"canonical"`
	// Noindex comes from the robots meta tag or the X-Robots-Tag header.
	Noindex   bool `json:"noindex"`
	Words     int  `json:"words"`
	Depth     int  `json:"depth"`
	InSitemap bool `json:"inSitemap"`
	// LinkedFrom is the first crawled page found linking here ("" when only the sitemap lists it).
	LinkedFrom string `json:"linkedFrom,omitempty"`
	// Error is a fetch failure reason (dns, timeout, too_large, …).
	Error string `json:"error,omitempty"`

	links []string
}

// OK reports whether the page answered 2xx with HTML.
func (p CrawledPage) OK() bool { return p.Error == "" && p.Status >= 200 && p.Status < 300 && p.HTML }

// Indexable is OK, not noindex, and canonical to itself (or without canonical).
func (p CrawledPage) Indexable() bool {
	return p.OK() && !p.Noindex && (p.Canonical == "" || sameURLString(p.Canonical, p.FinalURL))
}

func sameURLString(a, b string) bool {
	ua, err1 := url.Parse(a)
	ub, err2 := url.Parse(b)
	return err1 == nil && err2 == nil && sameURL(ua, ub)
}

// CrawlResult is a crawl of one site.
type CrawlResult struct {
	Start     string `json:"start"`
	Host      string `json:"host"`
	RobotsTxt bool   `json:"robotsTxt"`
	// RobotsBlocksRoot: the "*" group disallows the whole site.
	RobotsBlocksRoot bool `json:"robotsBlocksRoot"`
	// RobotsStatus is robots.txt's HTTP status (0 when the request failed:
	// RobotsError says why). A 5xx or a failed request means no URL is
	// crawled (RFC 9309 §2.3.1.4); a 4xx means everything may be.
	RobotsStatus int    `json:"robotsStatus"`
	RobotsError  string `json:"robotsError,omitempty"`
	// Sitemaps are the sitemap URLs that were read successfully.
	Sitemaps    []string      `json:"sitemaps"`
	SitemapURLs []string      `json:"sitemapUrls"`
	Pages       []CrawledPage `json:"pages"`
	// Truncated: MaxPages, MaxDepth or the time budget stopped the crawl with URLs left.
	Truncated bool `json:"truncated"`
	// Disallowed counts the URLs robots.txt keeps our crawler away from (not fetched).
	Disallowed int `json:"disallowed"`

	// robots is this crawl's parsed robots.txt rules (nil: none found); agent
	// is the product token they were resolved for. Allowed uses both.
	robots *robotsInfo
	agent  string
}

// Allowed reports whether this crawl's user agent may fetch rawURL under the
// robots.txt it read (true when it has none, or the request failed). Callers
// that re-fetch remembered URLs outside the crawl (health rechecks, lost-page
// tracing) must check this first so they stay inside robots.txt too.
func (r *CrawlResult) Allowed(rawURL string) bool {
	if r.robots == nil {
		return true
	}
	return r.robots.allowed(r.agent, rawURL)
}

// skipExt are link targets that are never HTML pages.
var skipExt = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true, ".svg": true, ".ico": true, ".avif": true,
	".css": true, ".js": true, ".mjs": true, ".json": true, ".xml": true, ".txt": true, ".pdf": true, ".zip": true,
	".mp4": true, ".webm": true, ".mp3": true, ".woff": true, ".woff2": true, ".ttf": true, ".dmg": true, ".exe": true,
}

// Crawl fetches up to MaxPages same-site URLs breadth-first, starting from
// start and the URLs of the site's sitemaps (robots.txt Sitemap: lines on the
// same host, else /sitemap.xml). URLs robots.txt disallows for the fetcher's
// user agent are skipped and counted. Only a bad start URL is an error;
// failed fetches are recorded on their page.
func Crawl(ctx context.Context, f *Fetcher, start string, opt CrawlOptions) (*CrawlResult, error) {
	opt = opt.withDefaults()
	startURL, err := normalizeURL(start, f.anyPort)
	if err != nil {
		return nil, &FetchError{Reason: "bad_url", Err: err}
	}
	origin, err := originOf(startURL)
	if err != nil {
		return nil, &FetchError{Reason: "bad_url", Err: err}
	}
	ctx, cancel := context.WithTimeout(ctx, opt.Budget)
	defer cancel()

	res := &CrawlResult{Start: startURL, Host: hostKey(origin.Hostname()), Sitemaps: []string{}, SitemapURLs: []string{}, Pages: []CrawledPage{}, agent: f.agentToken()}
	c := &crawler{f: f, opt: opt, res: res, seen: map[string]int{}, disallowed: map[string]bool{}}
	c.readSitemaps(ctx, origin)
	inSitemap := map[string]bool{}
	for _, u := range res.SitemapURLs {
		inSitemap[u] = true
	}

	frontier := []queued{{url: startURL}}
	for _, u := range res.SitemapURLs {
		frontier = append(frontier, queued{url: u, depth: 1})
	}
	for depth := 0; len(frontier) > 0 && !res.Truncated; depth++ {
		var batch, later, dups []queued
		for _, q := range frontier {
			if q.depth > depth {
				later = append(later, q)
				continue
			}
			if _, dup := c.seen[q.url]; dup || !c.crawlable(q.url) {
				dups = append(dups, q)
				continue
			}
			if !res.Allowed(q.url) {
				if !c.disallowed[q.url] {
					c.disallowed[q.url] = true
					res.Disallowed++
				}
				continue
			}
			if len(res.Pages)+len(batch) >= opt.MaxPages || depth > opt.MaxDepth || ctx.Err() != nil {
				res.Truncated = true
				break
			}
			c.seen[q.url] = len(res.Pages) + len(batch)
			batch = append(batch, q)
		}
		pages := c.fetchAll(ctx, batch, depth)
		next := later
		for i, p := range pages {
			p.InSitemap = inSitemap[p.URL]
			p.LinkedFrom = batch[i].from
			res.Pages = append(res.Pages, p)
			for _, l := range p.links {
				next = append(next, queued{url: l, depth: depth + 1, from: p.URL})
			}
		}
		for _, q := range dups {
			c.markLinked(q)
		}
		frontier = next
	}
	return res, nil
}

type queued struct {
	url   string
	depth int
	from  string
}

type crawler struct {
	f          *Fetcher
	opt        CrawlOptions
	res        *CrawlResult
	seen       map[string]int // url → index in res.Pages
	disallowed map[string]bool
}

// markLinked records the first linking page on an already crawled URL.
func (c *crawler) markLinked(q queued) {
	if i, ok := c.seen[q.url]; ok && q.from != "" && i < len(c.res.Pages) && c.res.Pages[i].LinkedFrom == "" {
		c.res.Pages[i].LinkedFrom = q.from
	}
}

func (c *crawler) crawlable(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || hostKey(u.Hostname()) != c.res.Host {
		return false
	}
	p := strings.ToLower(u.Path)
	// /cdn-cgi/ is Cloudflare's own machinery (email obfuscation, challenges), never site content.
	return !skipExt[path.Ext(p)] && !strings.HasPrefix(p, "/cdn-cgi/")
}

func (c *crawler) readSitemaps(ctx context.Context, origin *url.URL) {
	var candidates []string
	r, err := c.f.Get(ctx, origin.String()+"/robots.txt")
	if err != nil {
		c.res.RobotsError = "fetch_failed"
		if fe, ok := err.(*FetchError); ok {
			c.res.RobotsError = fe.Reason
		}
	} else {
		c.res.RobotsStatus = r.StatusCode
	}
	if c.res.RobotsError != "" || c.res.RobotsStatus >= 500 {
		// RFC 9309 §2.3.1.4: an unreachable robots.txt means "crawl nothing".
		c.res.robots = &robotsInfo{groups: []robotsGroup{{agents: []string{"*"}, rules: []robotsRule{{pattern: "/"}}}}}
	}
	if err == nil && r.StatusCode >= 200 && r.StatusCode < 300 {
		c.res.RobotsTxt = true
		ri := parseRobots(r.Body)
		c.res.robots = &ri
		c.res.RobotsBlocksRoot = ri.blocksRoot
		for _, s := range ri.sitemaps {
			if u, err := url.Parse(s); err == nil && hostKey(u.Hostname()) == hostKey(origin.Hostname()) {
				candidates = append(candidates, s)
			}
		}
	}
	if len(candidates) == 0 {
		candidates = []string{origin.String() + "/sitemap.xml"}
	}
	seen := map[string]bool{}
	for _, sm := range candidates {
		left := c.opt.SitemapLimit - len(c.res.SitemapURLs)
		if left <= 0 || ctx.Err() != nil {
			break
		}
		urls, err := ReadSitemap(ctx, c.f, sm, left)
		if err != nil {
			continue
		}
		c.res.Sitemaps = append(c.res.Sitemaps, sm)
		for _, u := range urls {
			if !seen[u] {
				seen[u] = true
				c.res.SitemapURLs = append(c.res.SitemapURLs, u)
			}
		}
	}
}

// fetchAll fetches batch with opt.Concurrency workers, keeping batch order.
func (c *crawler) fetchAll(ctx context.Context, batch []queued, depth int) []CrawledPage {
	out := make([]CrawledPage, len(batch))
	sem := make(chan struct{}, c.opt.Concurrency)
	var wg sync.WaitGroup
	for i, q := range batch {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, q queued) {
			defer wg.Done()
			defer func() { <-sem }()
			out[i] = c.fetchPage(ctx, q.url, depth)
			if c.opt.Delay > 0 {
				select {
				case <-ctx.Done():
				case <-time.After(c.opt.Delay):
				}
			}
		}(i, q)
	}
	wg.Wait()
	return out
}

func (c *crawler) fetchPage(ctx context.Context, u string, depth int) CrawledPage {
	p := CrawledPage{URL: u, Depth: depth, H1: []string{}}
	resp, err := c.f.Get(ctx, u)
	if err != nil {
		p.Error = "fetch_failed"
		if fe, ok := err.(*FetchError); ok {
			p.Error = fe.Reason
		}
		return p
	}
	p.FinalURL, p.Status, p.Redirects = resp.FinalURL, resp.StatusCode, resp.Redirects
	p.HTML = isHTML(resp)
	if !p.HTML {
		return p
	}
	fu, err := url.Parse(resp.FinalURL)
	if err != nil || hostKey(fu.Hostname()) != c.res.Host {
		return p // redirected off-site: nothing to learn
	}
	fa := extract(resp.FinalURL, resp.Body)
	p.Title, p.Description, p.Canonical, p.Words = fa.Title, fa.Description, fa.Canonical, fa.Words
	p.H1 = fa.H1
	robots := strings.ToLower(fa.Robots + ", " + resp.Header.Get("X-Robots-Tag"))
	p.Noindex = hasDirective(robots, "noindex") || hasDirective(robots, "none")
	if resp.StatusCode < 400 {
		p.links = fa.internalLinks
		sort.Strings(p.links)
	}
	return p
}
