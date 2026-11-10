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
