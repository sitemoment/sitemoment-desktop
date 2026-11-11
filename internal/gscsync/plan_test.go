// SPDX-License-Identifier: AGPL-3.0-or-later
package gscsync

import (
	"reflect"
	"testing"
	"time"
)

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func spans(pairs ...string) []Span {
	var out []Span
	for i := 0; i < len(pairs); i += 2 {
		out = append(out, Span{From: day(pairs[i]), Through: day(pairs[i+1])})
	}
	return out
}

func doneAll(at int64, days ...string) map[string]int64 {
	m := map[string]int64{}
	for _, d := range days {
		m[d] = at
	}
	return m
}

func TestPlan(t *testing.T) {
	from, through := day("2026-01-01"), day("2026-01-05")
	all := []string{"2026-01-01", "2026-01-02", "2026-01-03", "2026-01-04", "2026-01-05"}
	cases := []struct {
		name  string
		done  map[string]int64
		chunk int
		want  []Span
	}{
		{"nothing fetched, newest first in chunks", map[string]int64{}, 2,
			spans("2026-01-04", "2026-01-05", "2026-01-02", "2026-01-03", "2026-01-01", "2026-01-01")},
		{"all fresh", doneAll(100, all...), 90, nil},
		{"tail stale", doneAll(10, all...), 90, spans("2026-01-03", "2026-01-05")},
		{"one gap", func() map[string]int64 { m := doneAll(100, all...); delete(m, "2026-01-02"); return m }(), 90,
			spans("2026-01-02", "2026-01-02")},
	}
	for _, c := range cases {
		got := Plan(c.done, from, through, 3, 50, c.chunk)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// impMap builds a dateImpressions-shaped map from day->impressions pairs,
// dropping zero entries (store.DayImpressions never returns a zero row:
// days with no gsc_facts row are simply absent).
func impMap(pairs map[string]float64) map[string]float64 {
	out := map[string]float64{}
	for d, imp := range pairs {
		if imp > 0 {
			out[d] = imp
		}
	}
	return out
}

// TestPlanQueryPageSkipsEmptyDays: a 30-day window where the date slice
// shows impressions on only 3 (non-adjacent) days must make exactly 3
// query/page requests; every other day is recorded as fetched with 0 rows
// without a request.
func TestPlanQueryPageSkipsEmptyDays(t *testing.T) {
	from, through := day("2026-01-01"), day("2026-01-30")
	dateDone := map[string]int64{}
	imp := map[string]float64{}
	for d := from; !d.After(through); d = d.AddDate(0, 0, 1) {
		dateDone[d.Format("2006-01-02")] = 100
	}
	for _, d := range []string{"2026-01-05", "2026-01-15", "2026-01-25"} {
		imp[d] = 10
	}
	plan := PlanQueryPage(map[string]int64{}, dateDone, impMap(imp), from, through, 3, 50, 7, 5000)

	var fetches, skips int
	for _, p := range plan {
		if p.Skip {
			skips++
		} else {
			fetches++
		}
	}
	if fetches != 3 {
		t.Fatalf("fetches = %d, want 3", fetches)
	}
	// Every day is accounted for (27 empty days grouped into skip spans).
	var coveredDays int
	for _, p := range plan {
		coveredDays += p.days()
	}
	if coveredDays != 30 {
		t.Fatalf("covered %d days, want 30", coveredDays)
	}
	if skips == 0 {
		t.Fatal("no skip spans recorded")
	}
}

// TestPlanQueryPageMergesSmallAdjacentDays: consecutive low-impression days
// merge into one request, up to maxDays days.
func TestPlanQueryPageMergesSmallAdjacentDays(t *testing.T) {
	from, through := day("2026-01-01"), day("2026-01-10") // 10 days
	dateDone := map[string]int64{}
	imp := map[string]float64{}
	for d := from; !d.After(through); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		dateDone[key] = 100
		imp[key] = 10 // tiny, well under the 5000 cap
	}
	plan := PlanQueryPage(map[string]int64{}, dateDone, impMap(imp), from, through, 3, 50, 7, 5000)

	var fetches []QPPlan
	for _, p := range plan {
		if !p.Skip {
			fetches = append(fetches, p)
		}
	}
	// 10 days, merged up to 7 at a time, newest first: Jan 4..10 (7d), then
	// Jan 1..3 (3d).
	if len(fetches) != 2 {
		t.Fatalf("fetch spans = %d, want 2: %+v", len(fetches), fetches)
	}
	if got := fetches[0].days(); got != 7 {
		t.Fatalf("first (newest) span = %d days, want 7", got)
	}
	if got := fetches[1].days(); got != 3 {
		t.Fatalf("second span = %d days, want 3", got)
	}
}

// TestPlanQueryPageDoesNotMergeAcrossBigImpressions: a day whose own
// impressions already exceed maxImpressions is fetched alone and does not
// pull a neighboring small day into its request.
func TestPlanQueryPageDoesNotMergeAcrossBigImpressions(t *testing.T) {
	from, through := day("2026-01-01"), day("2026-01-03")
	dateDone := doneAll(100, "2026-01-01", "2026-01-02", "2026-01-03")
	imp := impMap(map[string]float64{"2026-01-01": 10, "2026-01-02": 6000, "2026-01-03": 10})
	plan := PlanQueryPage(map[string]int64{}, dateDone, imp, from, through, 3, 50, 7, 5000)

	var fetches []QPPlan
	for _, p := range plan {
		if !p.Skip {
			fetches = append(fetches, p)
		}
	}
	if len(fetches) != 3 {
		t.Fatalf("fetch spans = %d, want 3 (each day alone): %+v", len(fetches), fetches)
	}
	for _, f := range fetches {
		if f.days() != 1 {
			t.Fatalf("span %+v covers %d days, want 1", f, f.days())
		}
	}
}

// TestPlanQueryPageUncoveredDayForcesSingleFetch: a day the date ledger has
// not fetched yet is always requested alone, even next to small days.
func TestPlanQueryPageUncoveredDayForcesSingleFetch(t *testing.T) {
	from, through := day("2026-01-01"), day("2026-01-03")
	dateDone := doneAll(100, "2026-01-01", "2026-01-03") // Jan 2 missing
	imp := impMap(map[string]float64{"2026-01-01": 10, "2026-01-03": 10})
	plan := PlanQueryPage(map[string]int64{}, dateDone, imp, from, through, 3, 50, 7, 5000)

	var fetches []QPPlan
	for _, p := range plan {
		if !p.Skip {
			fetches = append(fetches, p)
		}
	}
	if len(fetches) != 3 {
		t.Fatalf("fetch spans = %d, want 3: %+v", len(fetches), fetches)
	}
	for _, f := range fetches {
		if f.days() != 1 {
			t.Fatalf("span %+v covers %d days, want 1 (no merging through the uncovered day)", f, f.days())
		}
	}
}

func TestThrough(t *testing.T) {
	// 05:00 UTC on Nov 10 is still Nov 9 in California, so the newest day is Nov 8.
	if got := Through(time.Date(2026, 11, 10, 5, 0, 0, 0, time.UTC)); !got.Equal(day("2026-11-08")) {
		t.Fatalf("Through = %v", got)
	}
	if got := Through(time.Date(2026, 11, 10, 20, 0, 0, 0, time.UTC)); !got.Equal(day("2026-11-09")) {
		t.Fatalf("Through = %v", got)
	}
}
