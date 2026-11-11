// SPDX-License-Identifier: AGPL-3.0-or-later

// Package gscsync copies Search Console data into the local database, day
// by day, newest first, re-fetching the last few days on every run.
package gscsync

import (
	"time"
	_ "time/tzdata" // real America/Los_Angeles with DST, even without an OS zoneinfo database
)

// Span is an inclusive range of days to fetch.
type Span struct{ From, Through time.Time }

func (s Span) days() int { return int(s.Through.Sub(s.From).Hours()/24) + 1 }

// Plan lists the spans to fetch, newest first: days in [from, through] never
// fetched, plus the last `tail` days fetched before refreshBefore (unix
// seconds). Adjacent days are merged up to chunkDays per span.
func Plan(done map[string]int64, from, through time.Time, tail int, refreshBefore int64, chunkDays int) []Span {
	var out []Span
	open := false
	tailStart := through.AddDate(0, 0, -(tail - 1))
	for d := through; !d.Before(from); d = d.AddDate(0, 0, -1) {
		at, fetched := done[d.Format("2006-01-02")]
		need := !fetched || (!d.Before(tailStart) && at < refreshBefore)
		if !need {
			open = false
			continue
		}
		if open && out[len(out)-1].days() < chunkDays {
			out[len(out)-1].From = d
			continue
		}
		out = append(out, Span{From: d, Through: d})
		open = true
	}
	return out
}

// QPPlan is one thing a query or page slice's sync should do for a span of
// days: either fetch it with one GSC request, or (Skip) record it as
// fetched with zero rows without a request, because the date slice already
// showed those days have no impressions.
type QPPlan struct {
	Span
	Skip bool
}

// PlanQueryPage is Plan for the query and page slices: it uses the date
// slice's ledger (dateDone) and per-day impressions (dateImpressions, as
// returned by store.DayImpressions) to skip days already known to have no
// data, and merges consecutive non-empty days into one request as long as
// the merged span covers at most maxDays days and maxImpressions total
// impressions (by the date slice's count); bigger or uncertain days are
// requested one at a time. Days the date ledger does not cover yet (for
// example because an earlier Plan run for "date" itself was capped) are
// always requested alone, never merged.
//
// A day within the newest tail days is never permanently recorded empty:
// Search Console finalizes data for the most recent days late, so a zero
// seen there might just not be final yet. Such a day is still requested
// (not skipped) as long as it is due — either because it was never fetched,
// or because its own fetch is older than refreshBefore — exactly like a
// non-empty day, so it keeps getting re-checked every run until either real
// data shows up or it ages out of the tail (at which point a zero becomes
// final and is skipped from then on).
func PlanQueryPage(done, dateDone map[string]int64, dateImpressions map[string]float64, from, through time.Time, tail int, refreshBefore int64, maxDays int, maxImpressions float64) []QPPlan {
	var out []QPPlan
	// open tracks the kind of the span currently being extended backward
	// (toward older days): "" (closed), "skip" or "fetch".
	open := ""
	openImpressions := 0.0
	tailStart := through.AddDate(0, 0, -(tail - 1))
	for d := through; !d.Before(from); d = d.AddDate(0, 0, -1) {
		key := d.Format("2006-01-02")
		at, fetched := done[key]
		withinTail := !d.Before(tailStart)
		need := !fetched || (withinTail && at < refreshBefore)
		if !need {
			open = ""
			continue
		}
		_, dateFetched := dateDone[key]
		imp, hasImpressions := dateImpressions[key]
		isEmpty := !hasImpressions || imp <= 0

		switch {
		case !dateFetched:
			// Unknown: the date slice has not covered this day yet. Fetch
			// it alone; do not let it join (or start) a merge chain.
			out = append(out, QPPlan{Span: Span{From: d, Through: d}})
			open = ""

		case isEmpty && !withinTail:
			// Outside the tail: a zero is as final as it gets. Skip it,
			// recording it fetched with 0 rows, no request.
			if open == "skip" {
				out[len(out)-1].From = d
				continue
			}
			out = append(out, QPPlan{Span: Span{From: d, Through: d}, Skip: true})
			open = "skip"

		default:
			// Either real data, or a zero still within the tail (not
			// final yet, so fetch and recheck rather than skip).
			if open == "fetch" && out[len(out)-1].days() < maxDays && openImpressions+imp <= maxImpressions {
				out[len(out)-1].From = d
				openImpressions += imp
				continue
			}
			out = append(out, QPPlan{Span: Span{From: d, Through: d}})
			openImpressions = imp
			open = "fetch"
		}
	}
	return out
}

var pacific = func() *time.Location {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		return time.FixedZone("PST", -8*3600)
	}
	return loc
}()

// Through is the newest day to sync: yesterday in Pacific time, as a UTC
// midnight.
func Through(now time.Time) time.Time {
	y, m, d := now.In(pacific).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
}
