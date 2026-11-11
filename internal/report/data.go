// SPDX-License-Identifier: AGPL-3.0-or-later

// Package report builds a site's weekly report from local data only (Search
// Console, GA4 and Bing facts, health checks, tasks) as Markdown and as a
// self-contained HTML page with inline SVG charts.
package report

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/store"
	"github.com/sitemoment/sitemoment-desktop/internal/tasks"
)

// Pair is a metric this week and the week before.
type Pair struct {
	Cur  float64 `json:"cur"`
	Prev float64 `json:"prev"`
}

// Mover is a query whose clicks changed between the two weeks.
type Mover struct {
	Query    string
	Clicks   float64 // this week
	Prev     float64 // the week before
	Position float64 // this week (0: no impressions)
}

// GSCPart is the Search Console section.
type GSCPart struct {
	Cur, Prev store.Totals
	// Daily is the 28 days ending with the report's last day.
	Daily    []store.DayPoint
	Up, Down []Mover
	// Striking are queries ranking 8–20 over 28 days with at least 50 impressions.
	Striking []store.GSCStat
}

// HealthPart is the latest health check and the one about a week before.
type HealthPart struct {
	Latest  store.Crawl
	WeekAgo *store.Crawl
	Issues  []store.Issue
}

// TaskPart lists tasks closed this week and those still open.
type TaskPart struct {
	Passed, Failed, Open []tasks.View
}

// Data is everything one report shows.
type Data struct {
	Site                       store.Site
	From, To, PrevFrom, PrevTo string
	Generated                  time.Time
	GSC                        *GSCPart
	GA, Bing                   *Pair
	Health                     *HealthPart
	Tasks                      TaskPart
}

func day(t time.Time) string { return t.Format(store.DayLayout) }

// Gather reads the week ending at the newest synced Search Console day
// (yesterday without Search Console data) and the week before it.
func Gather(ctx context.Context, st *store.Store, site store.Site, now time.Time) (Data, error) {
	d := Data{Site: site, Generated: now}
	latest, err := st.LatestDay(ctx, site.ID, "gsc", "date")
	if err != nil {
		return d, err
	}
	to := now.AddDate(0, 0, -1)
	if latest != "" {
		to, _ = time.Parse(store.DayLayout, latest)
	}
	d.To, d.From = day(to), day(to.AddDate(0, 0, -6))
	d.PrevTo, d.PrevFrom = day(to.AddDate(0, 0, -7)), day(to.AddDate(0, 0, -13))

	if latest != "" {
		if d.GSC, err = gatherGSC(ctx, st, site.ID, d, to); err != nil {
			return d, err
		}
	}
	if site.GAProperty != "" {
		sum := func(from, to string) (float64, error) {
			rows, err := st.GAQuery(ctx, store.FactQuery{SiteID: site.ID, Dim: "date", From: from, To: to, Limit: 500})
			total := 0.0
			for _, r := range rows {
				total += r.Sessions
			}
			return total, err
		}
		p := &Pair{}
		if p.Cur, err = sum(d.From, d.To); err != nil {
			return d, err
		}
		if p.Prev, err = sum(d.PrevFrom, d.PrevTo); err != nil {
			return d, err
		}
		d.GA = p
	}
	if site.BingSite != "" {
		sum := func(from, to string) (float64, error) {
			rows, err := st.BingQuery(ctx, store.FactQuery{SiteID: site.ID, Dim: "date", From: from, To: to, Limit: 500})
			total := 0.0
			for _, r := range rows {
				total += r.Clicks
			}
			return total, err
		}
		p := &Pair{}
		if p.Cur, err = sum(d.From, d.To); err != nil {
			return d, err
		}
		if p.Prev, err = sum(d.PrevFrom, d.PrevTo); err != nil {
			return d, err
		}
		d.Bing = p
	}

	if c, err := st.LatestCrawl(ctx, site.ID); err == nil {
		h := &HealthPart{Latest: c}
		if old, err := st.CrawlBefore(ctx, site.ID, c.StartedAt-6*86400); err == nil {
			h.WeekAgo = &old
		} else if !errors.Is(err, store.ErrNotFound) {
			return d, err
		}
		if h.Issues, err = st.CrawlIssues(ctx, c.ID); err != nil {
			return d, err
		}
		d.Health = h
	} else if !errors.Is(err, store.ErrNotFound) {
		return d, err
	}

	all, err := st.Tasks(ctx, site.ID, tasks.StatusOpen, tasks.StatusPassed, tasks.StatusFailed)
	if err != nil {
		return d, err
	}
	weekAgo := now.AddDate(0, 0, -7).Unix()
	for _, t := range all {
		v := tasks.ViewOf(t)
		switch {
		case t.Status == tasks.StatusOpen:
			d.Tasks.Open = append(d.Tasks.Open, v)
		case t.DoneAt < weekAgo:
		case t.Status == tasks.StatusPassed:
			d.Tasks.Passed = append(d.Tasks.Passed, v)
		default:
			d.Tasks.Failed = append(d.Tasks.Failed, v)
		}
	}
	return d, nil
}

func gatherGSC(ctx context.Context, st *store.Store, siteID int64, d Data, to time.Time) (*GSCPart, error) {
	g := &GSCPart{}
	var err error
	if g.Cur, err = st.GSCTotals(ctx, siteID, d.From, d.To); err != nil {
		return nil, err
	}
	if g.Prev, err = st.GSCTotals(ctx, siteID, d.PrevFrom, d.PrevTo); err != nil {
		return nil, err
	}
	if g.Daily, err = st.GSCDaily(ctx, siteID, day(to.AddDate(0, 0, -27)), d.To); err != nil {
		return nil, err
	}
	week := func(from, to string) (map[string]store.GSCStat, error) {
		rows, err := st.GSCQuery(ctx, store.FactQuery{SiteID: siteID, Dim: "query", From: from, To: to, Limit: 500})
		m := map[string]store.GSCStat{}
		for _, r := range rows {
			m[r.Key] = r
		}
		return m, err
	}
	cur, err := week(d.From, d.To)
	if err != nil {
		return nil, err
	}
	prev, err := week(d.PrevFrom, d.PrevTo)
	if err != nil {
		return nil, err
	}
	var movers []Mover
	for q, r := range cur {
		movers = append(movers, Mover{Query: q, Clicks: r.Clicks, Prev: prev[q].Clicks, Position: r.Position})
	}
	for q, p := range prev {
		if _, ok := cur[q]; !ok {
			movers = append(movers, Mover{Query: q, Prev: p.Clicks})
		}
	}
	delta := func(m Mover) float64 { return m.Clicks - m.Prev }
	sort.Slice(movers, func(i, j int) bool {
		if delta(movers[i]) != delta(movers[j]) {
			return delta(movers[i]) > delta(movers[j])
		}
		return movers[i].Query < movers[j].Query
	})
	for _, m := range movers {
		if delta(m) > 0 && len(g.Up) < 5 {
			g.Up = append(g.Up, m)
		}
	}
	for i := len(movers) - 1; i >= 0 && len(g.Down) < 5; i-- {
		if delta(movers[i]) < 0 {
			g.Down = append(g.Down, movers[i])
		}
	}
	rows, err := st.GSCQuery(ctx, store.FactQuery{SiteID: siteID, Dim: "query", From: day(to.AddDate(0, 0, -27)), To: d.To, OrderBy: "impressions", Limit: 500})
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if r.Position >= 8 && r.Position <= 20 && r.Impressions >= 50 && len(g.Striking) < 8 {
			g.Striking = append(g.Striking, r)
		}
	}
	return g, nil
}
