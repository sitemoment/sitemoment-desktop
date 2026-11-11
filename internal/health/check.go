// SPDX-License-Identifier: AGPL-3.0-or-later
package health

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/rank"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

const (
	defaultMaxPages = 300
	defaultRecheck  = 60
	// keepCrawls is how many checks a site keeps; pages unseen for
	// forgetPagesAfter are forgotten.
	keepCrawls       = 60
	forgetPagesAfter = 90 * 24 * time.Hour
)

// Checker runs health checks. Safe for concurrent use on different sites;
// the caller (app.HealthService) runs one check per site at a time.
type Checker struct {
	Store   *store.Store
	Fetcher *rank.Fetcher
	// Inspector is nil when URL Inspection is not available.
	Inspector Inspector
	Now       func() time.Time
	// MaxPages caps the crawl (default 300); Recheck caps the remembered
	// pages the crawl missed that are traced again (default 60).
	MaxPages, Recheck int
	// InspectPerRun and InspectPerDay cap URL inspections per site (default 50 and 200).
	InspectPerRun, InspectPerDay int
	// Start is a site's start URL (default https://host/; tests serve plain http).
	Start func(host string) string
}

// Summary is stored as JSON with each check.
type Summary struct {
	Host        string `json:"host"`
	Pages       int    `json:"pages"`
	OK          int    `json:"ok"`
	SitemapURLs int    `json:"sitemapUrls"`
	Truncated   bool   `json:"truncated"`
	// Disallowed counts URLs robots.txt keeps SiteMomentBot away from.
	Disallowed int        `json:"disallowed"`
	Rechecked  int        `json:"rechecked"`
	GSC        *GSCWindow `json:"gsc,omitempty"`
	Inspected  int        `json:"inspected"`
	// InspectNote says why URL Inspection did not run or stopped early.
	InspectNote string `json:"inspectNote,omitempty"`
}

func (c *Checker) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Checker) perRun() int { return orDefault(c.InspectPerRun, defaultInspectPerRun) }
func (c *Checker) perDay() int { return orDefault(c.InspectPerDay, defaultInspectPerDay) }

func orDefault(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}

// ErrUnreachable means the crawl got no page at all.
var ErrUnreachable = errors.New("health: site unreachable")

// Check runs one health check of site and stores it; origin is "schedule"
// or "manual", watch the URLs whose index status tasks wait for. A failed
// check is stored too (status failed) and its error returned.
func (c *Checker) Check(ctx context.Context, site store.Site, origin string, watch []string) (store.Crawl, error) {
	now := c.now()
	id, err := c.Store.StartCrawl(ctx, site.ID, origin, now.Unix())
	if err != nil {
		return store.Crawl{}, err
	}
	// The outcome is stored even when ctx was cancelled mid-check.
	done := context.WithoutCancel(ctx)
	prev, prevErr := c.Store.LatestCrawl(ctx, site.ID)
	sum, issues, err := c.run(ctx, site, now, watch)
	if err != nil {
		msg := err.Error()
		if r := []rune(msg); len(r) > 300 {
			msg = string(r[:300])
		}
		if ferr := c.Store.FailCrawl(done, id, msg, c.now().Unix()); ferr != nil {
			return store.Crawl{}, ferr
		}
		cr, _ := c.Store.Crawl(done, id)
		return cr, err
	}
	// New: not reported by the previous finished check (everything is new on the first one).
	old := map[string]bool{}
	if prevErr == nil {
		if prevIssues, err := c.Store.CrawlIssues(ctx, prev.ID); err == nil {
			for _, is := range prevIssues {
				old[is.Code] = true
			}
		}
	}
	for i := range issues {
		issues[i].New = !old[issues[i].Code]
	}
	p0, p1, p2, score := Grade(issues)
	raw, _ := json.Marshal(sum)
	cr := store.Crawl{ID: id, FinishedAt: c.now().Unix(), Pages: sum.Pages, OKPages: sum.OK, Score: score, P0: p0, P1: p1, P2: p2, Summary: string(raw)}
	if err := c.Store.FinishCrawl(done, cr, issues); err != nil {
		return store.Crawl{}, err
	}
	if err := c.Store.PruneCrawls(done, site.ID, keepCrawls); err != nil {
		return store.Crawl{}, err
	}
	if err := c.Store.PrunePages(done, site.ID, now.Add(-forgetPagesAfter).Unix()); err != nil {
		return store.Crawl{}, err
	}
	return c.Store.Crawl(done, id)
}

func (c *Checker) run(ctx context.Context, site store.Site, now time.Time, watch []string) (Summary, []store.Issue, error) {
	start := "https://" + site.Host + "/"
	if c.Start != nil {
		start = c.Start(site.Host)
	}
	res, err := rank.Crawl(ctx, c.Fetcher, start, rank.CrawlOptions{MaxPages: orDefault(c.MaxPages, defaultMaxPages), Concurrency: 2, Budget: 5 * time.Minute})
	if err != nil {
		return Summary{}, nil, err
	}
	sum := Summary{Host: site.Host, Pages: len(res.Pages), SitemapURLs: len(res.SitemapURLs), Truncated: res.Truncated, Disallowed: res.Disallowed}
	for _, p := range res.Pages {
		if p.OK() {
			sum.OK++
		}
	}
	if !reachable(res) {
		if res.RobotsBlocksRoot || res.RobotsStatus >= 500 {
			return sum, fromRank(rank.SiteIssues(res)), nil // the block itself is the finding
		}
		switch {
		case res.RobotsError != "":
			return sum, nil, fmt.Errorf("网站打不开（robots.txt：%s）：%w", res.RobotsError, ErrUnreachable)
		case res.Disallowed > 0:
			return sum, nil, fmt.Errorf("robots.txt 不允许 SiteMomentBot 抓取首页，体检无法进行")
		case len(res.Pages) > 0 && res.Pages[0].Error != "":
			return sum, nil, fmt.Errorf("网站打不开（%s）：%w", res.Pages[0].Error, ErrUnreachable)
		default:
			return sum, nil, ErrUnreachable
		}
	}
	issues := fromRank(rank.SiteIssues(res))

	// Regressions against the memory of earlier checks.
	mem, err := c.Store.SitePages(ctx, site.ID)
	if err != nil {
		return sum, nil, err
	}
	ts := now.Unix()
	rows := make([]store.Page, 0, len(res.Pages))
	for _, p := range res.Pages {
		rows = append(rows, pageRow(p, ts))
	}
	noindex, canonical, failing := regressed(mem, res)
	unseen := unseenOK(mem, res, orDefault(c.Recheck, defaultRecheck))
	sum.Rechecked = len(unseen)
	lost, traced := lostPages(ctx, c.Fetcher, append(failing, unseen...), res, mem, ts)
	if err := c.Store.SavePages(ctx, site.ID, append(rows, traced...)); err != nil {
		return sum, nil, err
	}
	if len(lost) > 0 {
		issues = append(issues, newIssue(CodeLost, rank.SeverityP0, lost))
	}
	if len(noindex) > 0 {
		issues = append(issues, newIssue(CodeNewNoindex, rank.SeverityP0, noindex))
	}
	if len(canonical) > 0 {
		issues = append(issues, newIssue(CodeCanonical, rank.SeverityP1, canonical))
	}

	// Search Console: drops, then URL Inspection of pages without impressions.
	if site.GSCProperty != "" {
		w, drops, err := gscDrops(ctx, c.Store, site.ID)
		if err != nil {
			return sum, nil, err
		}
		sum.GSC = w
		issues = append(issues, drops...)
		switch {
		case c.Inspector == nil:
			sum.InspectNote = "没有连接 Google，不能做网址检查"
		case w == nil:
			sum.InspectNote = "Search Console 数据还没有同步完，这次不做网址检查"
		default:
			to, _ := time.Parse(store.DayLayout, w.To)
			seen, err := c.Store.ImpressionPages(ctx, site.ID, to.AddDate(0, 0, -(impressionDays-1)).Format(store.DayLayout), w.To)
			if err != nil {
				return sum, nil, err
			}
			sum.Inspected, sum.InspectNote = c.inspect(ctx, site, suspects(res, mem, seen, watch, now), now)
			if mem, err = c.Store.SitePages(ctx, site.ID); err != nil {
				return sum, nil, err
			}
			if ni := notIndexed(res, mem, now); len(ni) > 0 {
				issues = append(issues, newIssue(CodeNotIndexed, rank.SeverityP1, ni))
			}
		}
	}
	order := map[string]int{rank.SeverityP0: 0, rank.SeverityP1: 1, rank.SeverityP2: 2}
	sort.SliceStable(issues, func(i, j int) bool { return order[issues[i].Severity] < order[issues[j].Severity] })
	return sum, issues, nil
}

// reachable: at least one page answered.
func reachable(res *rank.CrawlResult) bool {
	for _, p := range res.Pages {
		if p.Error == "" {
			return true
		}
	}
	return false
}
