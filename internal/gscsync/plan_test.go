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

func TestThrough(t *testing.T) {
	// 05:00 UTC on Nov 10 is still Nov 9 in California, so the newest day is Nov 8.
	if got := Through(time.Date(2026, 11, 10, 5, 0, 0, 0, time.UTC)); !got.Equal(day("2026-11-08")) {
		t.Fatalf("Through = %v", got)
	}
	if got := Through(time.Date(2026, 11, 10, 20, 0, 0, 0, time.UTC)); !got.Equal(day("2026-11-09")) {
		t.Fatalf("Through = %v", got)
	}
}
