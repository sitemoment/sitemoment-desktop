// SPDX-License-Identifier: AGPL-3.0-or-later
package health

import (
	"context"
	"errors"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/google"
	"github.com/sitemoment/sitemoment-desktop/internal/rank"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

// Inspector runs Search Console URL Inspection for a site's property
// (app.GSCInspector picks the site's Google account and token).
type Inspector interface {
	Inspect(ctx context.Context, site store.Site, pageURL string) (google.Inspection, error)
}

const (
	// defaultInspectPerRun and defaultInspectPerDay keep far below Google's
	// 2,000 inspections per property per day.
	defaultInspectPerRun = 50
	defaultInspectPerDay = 200
	// reinspectAfter: a page's index status is asked again after a week;
	// pages a task watches after a day.
	reinspectAfter      = 7 * 24 * time.Hour
	reinspectWatchAfter = 24 * time.Hour
	// maxInspectErrors stops a run after this many failed inspections.
	maxInspectErrors = 3
)

// suspects are the pages worth an inspection, most useful first: pages a
// task watches, then indexable sitemap pages without Search Console
// impressions in 28 days, then other indexable pages without impressions.
// Pages inspected recently are skipped.
func suspects(res *rank.CrawlResult, mem map[string]store.Page, seen map[string]bool, watch []string, now time.Time) []string {
	stale := func(u string, after time.Duration) bool {
		return mem[u].IndexCheckedAt < now.Add(-after).Unix()
	}
	var out []string
	added := map[string]bool{}
	add := func(u string) {
		if !added[u] {
			added[u] = true
			out = append(out, u)
		}
	}
	for _, u := range watch {
		if stale(u, reinspectWatchAfter) {
			add(u)
		}
	}
	for _, inSitemap := range []bool{true, false} {
		for _, p := range res.Pages {
			if p.InSitemap != inSitemap || !p.Indexable() || p.Redirects > 0 || seen[p.URL] || seen[p.FinalURL] {
				continue
			}
			if stale(p.URL, reinspectAfter) {
				add(p.URL)
			}
		}
	}
	return out
}

// inspect asks Search Console about the suspects within the day's budget
// and stores the answers. It returns how many it inspected and why it
// stopped early ("" when it did not).
func (c *Checker) inspect(ctx context.Context, site store.Site, urls []string, now time.Time) (int, string) {
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	used, err := c.Store.InspectionsSince(ctx, site.ID, day.Unix())
	if err != nil {
		return 0, err.Error()
	}
	budget := min(c.perRun(), c.perDay()-used)
	if budget <= 0 && len(urls) > 0 {
		return 0, "今天的网址检查次数已用完"
	}
	n, failures, why := 0, 0, ""
	for _, u := range urls {
		if n == budget || ctx.Err() != nil {
			break
		}
		in, err := c.Inspector.Inspect(ctx, site, u)
		if err != nil {
			if errors.Is(err, google.ErrReauth) || errors.Is(err, google.ErrRateLimited) {
				return n, inspectError(err)
			}
			if failures++; failures >= maxInspectErrors {
				return n, inspectError(err)
			}
			why = inspectError(err)
			continue
		}
		if err := c.Store.SetInspection(ctx, site.ID, u, in.Verdict, in.CoverageState, now.Unix()); err != nil {
			return n, err.Error()
		}
		n++
	}
	return n, why
}

func inspectError(err error) string {
	switch {
	case errors.Is(err, google.ErrReauth):
		return "Google 授权已失效，请在设置里重新连接"
	case errors.Is(err, google.ErrRateLimited):
		return "Search Console 网址检查限流或今天的配额已用完"
	default:
		return err.Error()
	}
}

// notIndexed lists the crawl's indexable pages that their last inspection
// (within 30 days) says are not on Google, with the reason.
func notIndexed(res *rank.CrawlResult, mem map[string]store.Page, now time.Time) []string {
	var out []string
	for _, p := range res.Pages {
		m, ok := mem[p.URL]
		if !ok || !p.Indexable() || p.Redirects > 0 || m.IndexVerdict == "" || m.IndexVerdict == "PASS" || m.IndexCheckedAt < now.AddDate(0, 0, -30).Unix() {
			continue
		}
		reason := m.IndexCoverage
		if reason == "" {
			reason = m.IndexVerdict
		}
		out = append(out, p.URL+"（"+reason+"）")
	}
	return out
}
