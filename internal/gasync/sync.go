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

// RecentDays and BackfillBatch mirror gscsync's: every call keeps the
// newest RecentDays fully up to date for "landing" and "channel", and
// takes at most BackfillBatch additional older-history requests shared
// between the two (not BackfillBatch each), newest-first across both — a
// fresh 16-month property otherwise makes about 93 requests (mostly
// "landing", chunked every 7 days) in its very first run. "date" stays
// unbatched (90-day chunks are already cheap). Exported for the same
// reason as gscsync's: internal/app's progress table needs RecentDays to
// tell a real coverage gap from the ordinary day-by-day lag of a caught-up
// site.
const (
	RecentDays    = 28
	BackfillBatch = 20
)

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
	// Limiter paces requests per Google account, across every site and
	// slice sharing it (the same Limiter instance as gscsync.Syncer, since
	// both draw on the account's Google API quota); nil means no extra
	// pacing. Applied per HTTP request (google.GAReport's OnPage), not once
	// per logical Report, so a report that paginates is still paced
	// correctly.
	Limiter  gscsync.Limiter
	Progress func(gscsync.Progress)
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

// olderCandidate pairs an older-history span with the slice it belongs to,
// so "landing" and "channel" can share one backfill budget (BackfillBatch)
// instead of getting one each.
type olderCandidate struct {
	sl   Slice
	span gscsync.Span
}

// mergeNewestFirst merges two already newest-first candidate lists into
// one (a standard merge-sort merge step), so capping the combined list at
// BackfillBatch takes from whichever slice has the newest unfetched
// history first.
func mergeNewestFirst(a, b []olderCandidate) []olderCandidate {
	out := make([]olderCandidate, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		if !a[i].span.Through.Before(b[j].span.Through) {
			out = append(out, a[i])
			i++
		} else {
			out = append(out, b[j])
			j++
		}
	}
	out = append(out, a[i:]...)
	out = append(out, b[j:]...)
	return out
}

// SyncSite fetches what gscsync.Plan says is missing or stale, up to
// MaxRequests, keeping each slice's recent window complete and the rest of
// "landing"/"channel"'s history to at most BackfillBatch extra requests
// shared between the two (see RecentDays, BackfillBatch).
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
	tail := or(s.Tail, 3)

	recentFrom := through.AddDate(0, 0, -(RecentDays - 1))
	if recentFrom.Before(from) {
		recentFrom = from
	}

	token, err := s.Tokens.AccessToken(ctx, site.GAAccount)
	if err != nil {
		return res, err
	}

	// "date": cheap enough (90-day chunks) to always fetch in full.
	dateSlice := Slices[0]
	done, err := s.Store.SyncDays(ctx, site.ID, "ga", dateSlice.Name)
	if err != nil {
		return res, err
	}
	spans := gscsync.Plan(done, from, through, tail, refreshBefore, dateSlice.ChunkDays)
	if err := s.runSpans(ctx, site, token, dateSlice, spans, limit, now, &res); err != nil {
		return res, err
	}

	// "landing" and "channel": each slice's recent window is always
	// synced in full; older history shares one BackfillBatch budget
	// between the two, newest-first.
	type slState struct {
		sl         Slice
		fetchSpans []gscsync.Span
		older      []olderCandidate
	}
	var states []*slState
	for _, sl := range Slices[1:] {
		done, err := s.Store.SyncDays(ctx, site.ID, "ga", sl.Name)
		if err != nil {
			return res, err
		}
		st := &slState{sl: sl}
		st.fetchSpans = gscsync.Plan(done, recentFrom, through, tail, refreshBefore, sl.ChunkDays)
		if recentFrom.After(from) {
			olderThrough := recentFrom.AddDate(0, 0, -1)
			for _, sp := range gscsync.Plan(done, from, olderThrough, 0, 0, sl.ChunkDays) {
				st.older = append(st.older, olderCandidate{sl: sl, span: sp})
			}
		}
		states = append(states, st)
	}

	var merged []olderCandidate
	for _, st := range states {
		merged = mergeNewestFirst(merged, st.older)
	}
	if len(merged) > BackfillBatch {
		res.Remaining += len(merged) - BackfillBatch
		merged = merged[:BackfillBatch]
	}
	for _, st := range states {
		for _, c := range merged {
			if c.sl.Name == st.sl.Name {
				st.fetchSpans = append(st.fetchSpans, c.span)
			}
		}
	}

	for _, st := range states {
		if err := s.runSpans(ctx, site, token, st.sl, st.fetchSpans, limit, now, &res); err != nil {
			return res, err
		}
	}
	return res, nil
}

// runSpans issues one GA4 request per span (sequentially: GA4's report rate
// is already modest enough that gscsync's bounded parallelism is not
// needed here), stopping at the first error. Spans beyond what
// res.Requests has room for under limit are added to res.Remaining and
// left for a later call. Progress's Total is the number of requests this
// call will actually make (after that cap), not the slice's whole plan.
func (s *Syncer) runSpans(ctx context.Context, site store.Site, token string, sl Slice, spans []gscsync.Span, limit int, now time.Time, res *gscsync.Result) error {
	if len(spans) == 0 {
		return nil
	}
	room := limit - res.Requests
	if room < 0 {
		room = 0
	}
	todo := spans
	if len(todo) > room {
		res.Remaining += len(todo) - room
		todo = todo[:room]
	}
	if len(todo) == 0 {
		return nil
	}

	for i, sp := range todo {
		start, end := sp.From.Format(store.DayLayout), sp.Through.Format(store.DayLayout)
		r := google.GAReport{Start: start, End: end, Dimensions: sl.Dimensions, Metrics: Metrics}
		if s.Limiter != nil {
			r.OnPage = func(c context.Context) (func(), error) { return s.Limiter.Acquire(c, site.GAAccount) }
		}
		rows, err := s.GA.Report(ctx, token, site.GAProperty, r)
		res.Requests++
		if err != nil && !errors.Is(err, google.ErrTruncated) {
			return fmt.Errorf("GA4 %s %s..%s: %w", sl.Name, start, end, err)
		}
		if err := s.Store.ReplaceGA(ctx, site.ID, sl.Name, start, end, toRows(sl, rows), now.Unix()); err != nil {
			return err
		}
		if s.Progress != nil {
			s.Progress(gscsync.Progress{SiteID: site.ID, Slice: "ga:" + sl.Name, Day: start, Done: i + 1, Total: len(todo)})
		}
	}
	return nil
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
