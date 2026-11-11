// SPDX-License-Identifier: AGPL-3.0-or-later

// Package gasync copies GA4 reports into the local database, day by day,
// newest first, re-fetching the last few days on every run (GA keeps
// processing data for 24-48 hours).
package gasync

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/google"
	"github.com/sitemoment/sitemoment-desktop/internal/gscsync"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

// Reporter runs GA4 reports (google.GA).
type Reporter interface {
	Report(ctx context.Context, token, property string, r google.GAReport) ([]google.GARow, error)
}

// Metrics are fetched for every slice, in this order.
var Metrics = []string{"sessions", "totalUsers", "engagedSessions", "keyEvents"}

// Slice is one dimension set kept locally; the first dimension is date.
type Slice struct {
	Name       string
	Dimensions []string
	ChunkDays  int
}

// Slices are the GA4 reports synced in M2.
var Slices = []Slice{
	{Name: "date", Dimensions: []string{"date"}, ChunkDays: 90},
	{Name: "landing", Dimensions: []string{"date", "landingPage"}, ChunkDays: 7},
	{Name: "channel", Dimensions: []string{"date", "sessionDefaultChannelGroup"}, ChunkDays: 30},
}

// ErrNotBound means the site has no GA4 property yet.
var ErrNotBound = errors.New("gasync: site has no GA4 property")

// Syncer copies one site's GA4 data into the store.
type Syncer struct {
	Store        *store.Store
	Tokens       gscsync.TokenSource
	GA           Reporter
	Now          func() time.Time // default time.Now
	Months       int              // history; default 16
	Tail         int              // newest days re-fetched; default 3
	RefreshAfter time.Duration    // tail days older than this are re-fetched; default 6h
	MaxRequests  int              // per SyncSite call; default 200
	Progress     func(gscsync.Progress)
}

func or[T comparable](v, def T) T {
	var zero T
	if v == zero {
		return def
	}
	return v
}

// Through is the newest day to sync: yesterday in now's time zone, as a UTC
// midnight. GA4 days follow the property's time zone, which the app does
// not know; the re-fetched tail covers the difference.
func Through(now time.Time) time.Time {
	y, m, d := now.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
}

// SyncSite fetches what gscsync.Plan says is missing or stale, up to MaxRequests.
func (s *Syncer) SyncSite(ctx context.Context, site store.Site) (gscsync.Result, error) {
	var res gscsync.Result
	if site.GAProperty == "" || site.GAAccount == "" {
		return res, ErrNotBound
	}
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	through := Through(now)
	from := through.AddDate(0, -or(s.Months, 16), 0)
	refreshBefore := now.Add(-or(s.RefreshAfter, 6*time.Hour)).Unix()
	limit := or(s.MaxRequests, 200)

	token, err := s.Tokens.AccessToken(ctx, site.GAAccount)
	if err != nil {
		return res, err
	}
	for _, sl := range Slices {
		done, err := s.Store.SyncDays(ctx, site.ID, "ga", sl.Name)
		if err != nil {
			return res, err
		}
		spans := gscsync.Plan(done, from, through, or(s.Tail, 3), refreshBefore, sl.ChunkDays)
		for i, sp := range spans {
			if res.Requests >= limit {
				res.Remaining += len(spans) - i
				break
			}
			start, end := sp.From.Format(store.DayLayout), sp.Through.Format(store.DayLayout)
			rows, err := s.GA.Report(ctx, token, site.GAProperty, google.GAReport{Start: start, End: end, Dimensions: sl.Dimensions, Metrics: Metrics})
			res.Requests++
			if err != nil && !errors.Is(err, google.ErrTruncated) {
				return res, fmt.Errorf("GA4 %s %s..%s: %w", sl.Name, start, end, err)
			}
			if err := s.Store.ReplaceGA(ctx, site.ID, sl.Name, start, end, toRows(sl, rows), now.Unix()); err != nil {
				return res, err
			}
			if s.Progress != nil {
				s.Progress(gscsync.Progress{SiteID: site.ID, Slice: "ga:" + sl.Name, Day: start, Done: i + 1, Total: len(spans)})
			}
		}
	}
	return res, nil
}

func toRows(sl Slice, rows []google.GARow) []store.GARow {
	out := make([]store.GARow, 0, len(rows))
	for _, r := range rows {
		if len(r.Dims) != len(sl.Dimensions) || len(r.Metrics) != len(Metrics) {
			continue
		}
		day, err := time.Parse("20060102", r.Dims[0])
		if err != nil {
			continue
		}
		row := store.GARow{Day: day.Format(store.DayLayout), Sessions: r.Metrics[0], Users: r.Metrics[1], EngagedSessions: r.Metrics[2], KeyEvents: r.Metrics[3]}
		if len(r.Dims) > 1 {
			row.Dim = r.Dims[1]
		}
		out = append(out, row)
	}
	return out
}
