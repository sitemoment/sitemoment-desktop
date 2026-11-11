// SPDX-License-Identifier: AGPL-3.0-or-later
package gscsync

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/google"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

// TokenSource returns a Google access token for a connected account.
type TokenSource interface {
	AccessToken(ctx context.Context, account string) (string, error)
}

// Querier runs Search Analytics queries (google.GSC).
type Querier interface {
	Query(ctx context.Context, token, property string, q google.Query) ([]google.Row, error)
}

// Limiter additionally paces requests per key (a Google account), on top of
// MaxRequests and the GSC client's own 429 backoff; *ratelimit.Limiter
// satisfies this. Acquire blocks until a slot is free, or ctx ends; release
// must be called exactly once when the request is done. It is applied per
// HTTP request (google.Query's OnPage), not once per logical Query, so a
// query that paginates is still paced correctly.
type Limiter interface {
	Acquire(ctx context.Context, key string) (release func(), err error)
}

// Slice is one dimension set kept locally.
type Slice struct {
	Name       string
	Dimensions []string
	ChunkDays  int
}

// Slices are the dimension sets synced in M1 (web search only).
var Slices = []Slice{
	{Name: "date", Dimensions: []string{"date"}, ChunkDays: 90},
	{Name: "query", Dimensions: []string{"date", "query"}, ChunkDays: 1},
	{Name: "page", Dimensions: []string{"date", "page"}, ChunkDays: 1},
}

// ErrNotBound means the site has no Search Console property yet.
var ErrNotBound = errors.New("gscsync: site has no Search Console property")

// Progress is reported after every request.
type Progress struct {
	SiteID int64  `json:"siteId"`
	Slice  string `json:"slice"`
	Day    string `json:"day"`
	Done   int    `json:"done"`
	Total  int    `json:"total"`
}

// Result says how much a run did and how many spans are left.
type Result struct {
	Requests  int `json:"requests"`
	Remaining int `json:"remaining"`
}

// RecentDays and BackfillBatch bound the first Search Console backfill so
// one SyncSite call never blocks on hundreds of requests: every call keeps
// the newest RecentDays fully up to date (so the overview works right
// away), and takes at most BackfillBatch additional older-history
// requests — shared between the query and page slices, not BackfillBatch
// each — newest-first across both. The rest of the history
// (res.Remaining) catches up over later calls (see app.RunBackfillLoop).
// The date slice's 90-day chunks are cheap enough (about 6 requests for 16
// months) to always fetch in full, unbatched. These are constants, not
// settings, by design: one place to tune them. Exported so internal/app's
// progress table can tell a real coverage gap (older than RecentDays) from
// the ordinary day-by-day lag of a site that is fully caught up.
const (
	RecentDays    = 28
	BackfillBatch = 20
)

// Syncer copies one site's Search Console data into the store.
type Syncer struct {
	Store  *store.Store
	Tokens TokenSource
	GSC    Querier
	Now    func() time.Time // default time.Now
	Months int              // history; default 16
	// Tail is how many of the newest days are re-fetched when stale;
	// default 5. Kept at least a few days because Search Console finalizes
	// a day's data late: PlanQueryPage never permanently records a zero
	// within the tail, only once a day ages out of it.
	Tail         int
	RefreshAfter time.Duration // tail days older than this are re-fetched; default 6h
	MaxRequests  int           // per SyncSite call; default 200
	// Parallel is how many GSC requests run at once per SyncSite call;
	// default 4. Store writes are always serialized.
	Parallel int
	// MergeMaxDays and MergeMaxImpressions bound how many consecutive
	// query/page days (by the date slice's impressions) are merged into one
	// request; defaults 7 and 5000.
	MergeMaxDays        int
	MergeMaxImpressions float64
	// Limiter additionally paces requests per Google account, across every
	// site and slice sharing it; nil means no extra pacing.
	Limiter  Limiter
	Progress func(Progress)
}

func or[T comparable](v, def T) T {
	var zero T
	if v == zero {
		return def
	}
	return v
}

// olderCandidate pairs a query/page older-history span with the slice it
// belongs to, so the two slices can share one backfill budget (see
// BackfillBatch) instead of getting one each.
type olderCandidate struct {
	sl   Slice
	span Span
}

// mergeNewestFirst merges two already newest-first candidate lists into
// one newest-first list (a standard merge-sort merge step), so capping the
// combined list at BackfillBatch takes from whichever slice has the newest
// unfetched history first, not from one slice until it is exhausted.
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

// SyncSite fetches what Plan (or PlanQueryPage) says is missing or stale,
// up to MaxRequests, keeping the recent window complete and the rest of the
// history to at most BackfillBatch extra requests shared between the query
// and page slices (see RecentDays, BackfillBatch).
func (s *Syncer) SyncSite(ctx context.Context, site store.Site) (Result, error) {
	var res Result
	if site.GSCProperty == "" || site.GoogleAccount == "" {
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
	tail := or(s.Tail, 5)
	mergeDays := or(s.MergeMaxDays, 7)
	mergeImpressions := or(s.MergeMaxImpressions, 5000)

	recentFrom := through.AddDate(0, 0, -(RecentDays - 1))
	if recentFrom.Before(from) {
		recentFrom = from
	}

	token, err := s.Tokens.AccessToken(ctx, site.GoogleAccount)
	if err != nil {
		return res, err
	}

	// "date": cheap enough (90-day chunks) to always fetch in full.
	done, err := s.Store.SyncDays(ctx, site.ID, "gsc", "date")
	if err != nil {
		return res, err
	}
	dateSlice := Slices[0]
	spans := Plan(done, from, through, tail, refreshBefore, dateSlice.ChunkDays)
	if err := s.runSpans(ctx, site, token, dateSlice, spans, limit, now, &res); err != nil {
		return res, err
	}
	if err := ctx.Err(); err != nil {
		return res, err
	}

	// "query" and "page": each slice's own recent window (RecentDays) is
	// always synced in full; older history shares one BackfillBatch budget
	// between the two, newest-first.
	dateDone, err := s.Store.SyncDays(ctx, site.ID, "gsc", "date")
	if err != nil {
		return res, err
	}
	dateImpressions, err := s.Store.DayImpressions(ctx, site.ID, from.Format(store.DayLayout), through.Format(store.DayLayout))
	if err != nil {
		return res, err
	}

	type qpState struct {
		sl         Slice
		skipSpans  []Span
		fetchSpans []Span
		older      []olderCandidate
	}
	var qp []*qpState
	for _, sl := range Slices[1:] {
		done, err := s.Store.SyncDays(ctx, site.ID, "gsc", sl.Name)
		if err != nil {
			return res, err
		}
		st := &qpState{sl: sl}
		for _, p := range PlanQueryPage(done, dateDone, dateImpressions, recentFrom, through, tail, refreshBefore, mergeDays, mergeImpressions) {
			if p.Skip {
				st.skipSpans = append(st.skipSpans, p.Span)
			} else {
				st.fetchSpans = append(st.fetchSpans, p.Span)
			}
		}
		if recentFrom.After(from) {
			olderThrough := recentFrom.AddDate(0, 0, -1)
			for _, p := range PlanQueryPage(done, dateDone, dateImpressions, from, olderThrough, 0, 0, mergeDays, mergeImpressions) {
				if p.Skip {
					st.skipSpans = append(st.skipSpans, p.Span)
				} else {
					st.older = append(st.older, olderCandidate{sl: sl, span: p.Span})
				}
			}
		}
		qp = append(qp, st)
	}

	var merged []olderCandidate
	for _, st := range qp {
		merged = mergeNewestFirst(merged, st.older)
	}
	if len(merged) > BackfillBatch {
		res.Remaining += len(merged) - BackfillBatch
		merged = merged[:BackfillBatch]
	}
	for _, st := range qp {
		for _, c := range merged {
			if c.sl.Name == st.sl.Name {
				st.fetchSpans = append(st.fetchSpans, c.span)
			}
		}
	}

	for _, st := range qp {
		for _, sp := range st.skipSpans {
			start, end := sp.From.Format(store.DayLayout), sp.Through.Format(store.DayLayout)
			if err := s.Store.ReplaceGSC(ctx, site.ID, st.sl.Name, start, end, nil, now.Unix()); err != nil {
				return res, err
			}
		}
		if err := s.runSpans(ctx, site, token, st.sl, st.fetchSpans, limit, now, &res); err != nil {
			return res, err
		}
	}
	return res, nil
}

// runSpans issues one GSC request per span, up to Parallel at once
// (default 4), stopping at the first error. Store writes are serialized
// (one at a time) even though requests run concurrently. Spans beyond what
// res.Requests has room for under limit are added to res.Remaining and left
// for a later call. Progress's Total is the number of requests this call
// will actually make (after that cap), not the slice's whole plan.
func (s *Syncer) runSpans(ctx context.Context, site store.Site, token string, sl Slice, spans []Span, limit int, now time.Time, res *Result) error {
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

	workers := or(s.Parallel, 4)
	if workers > len(todo) {
		workers = len(todo)
	}

	reqCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		firstErr error
		done     int
		sent     int // spans actually handed to a worker, for the ctx-already-done case
	)
	jobs := make(chan int)
	go func() {
		defer close(jobs)
		for i := range todo {
			select {
			case jobs <- i:
				mu.Lock()
				sent++
				mu.Unlock()
			case <-reqCtx.Done():
				return
			}
		}
	}()

	fail := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
			cancel()
		}
		mu.Unlock()
	}

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				sp := todo[i]
				start, end := sp.From.Format(store.DayLayout), sp.Through.Format(store.DayLayout)

				q := google.Query{Start: start, End: end, Dimensions: sl.Dimensions}
				if s.Limiter != nil {
					q.OnPage = func(c context.Context) (func(), error) { return s.Limiter.Acquire(c, site.GoogleAccount) }
				}
				rows, err := s.GSC.Query(reqCtx, token, site.GSCProperty, q)
				if err != nil && !errors.Is(err, google.ErrTruncated) {
					fail(fmt.Errorf("search console %s %s..%s: %w", sl.Name, start, end, err))
					return
				}

				mu.Lock()
				werr := s.Store.ReplaceGSC(ctx, site.ID, sl.Name, start, end, toRows(sl, rows), now.Unix())
				res.Requests++
				done++
				if werr != nil {
					if firstErr == nil {
						firstErr = werr
						cancel()
					}
					mu.Unlock()
					continue
				}
				doneSnapshot, totalSnapshot := done, len(todo)
				mu.Unlock()
				if s.Progress != nil {
					s.Progress(Progress{SiteID: site.ID, Slice: sl.Name, Day: start, Done: doneSnapshot, Total: totalSnapshot})
				}
			}
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	// The caller's ctx may already have been done before this call even
	// started (for example, SyncSite's own ctx was canceled between
	// slices): nothing ran, but that must not look like a clean, empty
	// success — the caller needs to know to stop, not move on to the next
	// slice.
	if sent == 0 && ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}

func toRows(sl Slice, rows []google.Row) []store.GSCRow {
	out := make([]store.GSCRow, 0, len(rows))
	for _, r := range rows {
		if len(r.Keys) != len(sl.Dimensions) {
			continue
		}
		row := store.GSCRow{Day: r.Keys[0], Clicks: r.Clicks, Impressions: r.Impressions, Position: r.Position}
		switch sl.Name {
		case "query":
			row.Query = r.Keys[1]
		case "page":
			row.Page = r.Keys[1]
		}
		out = append(out, row)
	}
	return out
}
