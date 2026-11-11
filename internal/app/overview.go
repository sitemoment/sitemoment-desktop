// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"context"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

// Overview is everything the overview page shows for one site and period.
type Overview struct {
	Site      store.Site        `json:"site"`
	Synced    bool              `json:"synced"`
	Days      int               `json:"days"`
	From      string            `json:"from"`
	To        string            `json:"to"`
	PrevFrom  string            `json:"prevFrom"`
	PrevTo    string            `json:"prevTo"`
	Current   store.Totals      `json:"current"`
	Previous  store.Totals      `json:"previous"`
	Daily     []store.DayPoint  `json:"daily"`
	PrevDaily []store.DayPoint  `json:"prevDaily"`
	Queries   []store.QueryStat `json:"queries"`
	Tiles     []Tile            `json:"tiles"`
}

// OverviewService serves the overview page.
type OverviewService struct{ Store *store.Store }

// Get returns the last `days` days (7, 28 or 90; anything else means 28)
// ending at the newest synced day, against the period before.
func (o *OverviewService) Get(ctx context.Context, siteID int64, days int) (Overview, error) {
	if days != 7 && days != 28 && days != 90 {
		days = 28
	}
	site, err := o.Store.Site(ctx, siteID)
	if err != nil {
		return Overview{}, err
	}
	ov := Overview{Site: site, Days: days, Daily: []store.DayPoint{}, PrevDaily: []store.DayPoint{}, Queries: []store.QueryStat{}, Tiles: []Tile{}}
	if ov.Tiles, err = o.tiles(ctx, site, days); err != nil {
		return ov, err
	}
	latest, err := o.Store.LatestDay(ctx, siteID, "gsc", "date")
	if err != nil || latest == "" {
		return ov, err
	}
	to, err := time.Parse(store.DayLayout, latest)
	if err != nil {
		return ov, err
	}
	from := to.AddDate(0, 0, -(days - 1))
	prevTo := from.AddDate(0, 0, -1)
	prevFrom := prevTo.AddDate(0, 0, -(days - 1))
	f := func(t time.Time) string { return t.Format(store.DayLayout) }
	ov.Synced, ov.From, ov.To, ov.PrevFrom, ov.PrevTo = true, f(from), f(to), f(prevFrom), f(prevTo)

	if ov.Current, err = o.Store.GSCTotals(ctx, siteID, ov.From, ov.To); err != nil {
		return ov, err
	}
	if ov.Previous, err = o.Store.GSCTotals(ctx, siteID, ov.PrevFrom, ov.PrevTo); err != nil {
		return ov, err
	}
	daily, err := o.Store.GSCDaily(ctx, siteID, ov.From, ov.To)
	if err != nil {
		return ov, err
	}
	ov.Daily = fillDays(from, to, daily)
	prevDaily, err := o.Store.GSCDaily(ctx, siteID, ov.PrevFrom, ov.PrevTo)
	if err != nil {
		return ov, err
	}
	ov.PrevDaily = fillDays(prevFrom, prevTo, prevDaily)
	ov.Queries, err = o.Store.TopQueries(ctx, siteID, ov.From, ov.To, ov.PrevFrom, ov.PrevTo, 20)
	return ov, err
}

// fillDays returns exactly one DayPoint per day in [from, to], in ascending
// order, using pts where it has one and a zero DayPoint otherwise. GSC (and
// so the store, which only ever stores what GSC reported) can omit days
// with no clicks or impressions, but the overview chart compares the
// current and previous periods by index, so both series must have the same
// length and day-by-day alignment.
func fillDays(from, to time.Time, pts []store.DayPoint) []store.DayPoint {
	by := make(map[string]store.DayPoint, len(pts))
	for _, p := range pts {
		by[p.Day] = p
	}
	out := make([]store.DayPoint, 0, int(to.Sub(from).Hours()/24)+1)
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		day := d.Format(store.DayLayout)
		if p, ok := by[day]; ok {
			out = append(out, p)
		} else {
			out = append(out, store.DayPoint{Day: day})
		}
	}
	return out
}

// Tile is a GA4 or Bing KPI: one metric over the source's own newest `days`
// days, against the period before, with one value per day.
type Tile struct {
	Source   string    `json:"source"` // "ga" | "bing"
	Label    string    `json:"label"`
	From     string    `json:"from"`
	To       string    `json:"to"`
	Current  float64   `json:"current"`
	Previous float64   `json:"previous"`
	Daily    []float64 `json:"daily"`
}

type tileSource struct {
	bound                bool
	source, slice, label string
	total                func(from, to string) (float64, error)
	daily                func(from, to string) ([]store.DayValue, error)
}

// tiles builds a Tile for every bound source that has data.
func (o *OverviewService) tiles(ctx context.Context, site store.Site, days int) ([]Tile, error) {
	out := []Tile{}
	for _, s := range []tileSource{
		{site.GAProperty != "", "ga", "date", "会话",
			func(from, to string) (float64, error) {
				t, err := o.Store.GATotals(ctx, site.ID, from, to)
				return t.Sessions, err
			},
			func(from, to string) ([]store.DayValue, error) { return o.Store.GADaily(ctx, site.ID, from, to) }},
		{site.BingSite != "", "bing", "traffic", "Bing 点击",
			func(from, to string) (float64, error) {
				t, err := o.Store.BingTotals(ctx, site.ID, from, to)
				return t.Clicks, err
			},
			func(from, to string) ([]store.DayValue, error) { return o.Store.BingDaily(ctx, site.ID, from, to) }},
	} {
		if !s.bound {
			continue
		}
		latest, err := o.Store.LatestDay(ctx, site.ID, s.source, s.slice)
		if err != nil {
			return nil, err
		}
		if latest == "" {
			continue
		}
		to, err := time.Parse(store.DayLayout, latest)
		if err != nil {
			return nil, err
		}
		from := to.AddDate(0, 0, -(days - 1))
		prevTo := from.AddDate(0, 0, -1)
		prevFrom := prevTo.AddDate(0, 0, -(days - 1))
		f := func(t time.Time) string { return t.Format(store.DayLayout) }
		tile := Tile{Source: s.source, Label: s.label, From: f(from), To: f(to)}
		if tile.Current, err = s.total(f(from), f(to)); err != nil {
			return nil, err
		}
		if tile.Previous, err = s.total(f(prevFrom), f(prevTo)); err != nil {
			return nil, err
		}
		pts, err := s.daily(f(from), f(to))
		if err != nil {
			return nil, err
		}
		tile.Daily = fillValues(from, to, pts)
		out = append(out, tile)
	}
	return out, nil
}

// fillValues is one value per day in [from, to], 0 where pts has none.
func fillValues(from, to time.Time, pts []store.DayValue) []float64 {
	by := make(map[string]float64, len(pts))
	for _, p := range pts {
		by[p.Day] = p.Value
	}
	out := []float64{}
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		out = append(out, by[d.Format(store.DayLayout)])
	}
	return out
}
