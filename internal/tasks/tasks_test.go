// SPDX-License-Identifier: AGPL-3.0-or-later
package tasks

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/rank"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

var ctx = context.Background()

func ok(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func setup(t *testing.T, host string) (*Service, store.Site, *time.Time) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	ok(t, err)
	t.Cleanup(func() { st.Close() })
	ok(t, st.Migrate(ctx))
	site, err := st.AddSite(ctx, host, 1)
	ok(t, err)
	ok(t, st.BindGSC(ctx, site.ID, "me@example.com", "sc-domain:"+host))
	site, _ = st.Site(ctx, site.ID)
	now := time.Date(2026, 11, 10, 9, 0, 0, 0, time.Local)
	s := &Service{Store: st, Fetcher: rank.NewFetcher(rank.FetcherConfig{AllowPrivate: true}), Now: func() time.Time { return now }}
	return s, site, &now
}

func TestValidate(t *testing.T) {
	site := store.Site{Host: "example.com"}
	good, err := Validate([]Condition{
		{Kind: KindPosition, Keyword: "  Thumbnail   Maker ", Value: 10},
		{Kind: KindStatus, URL: "https://www.example.com/a"},
		{Kind: KindIndexed, URL: "https://blog.example.com/b", Keyword: "dropped"},
		{Kind: KindMetric, Metric: "clicks", Op: "gte", Value: 300},
	}, site)
	ok(t, err)
	if good[0].Keyword != "thumbnail maker" || good[0].Op != "lt" || good[0].Window != 28 || good[1].Status != 200 ||
		good[2].Keyword != "" || good[3].Scope != "site" {
		t.Fatalf("normalized = %+v", good)
	}
	if got := Describe(good[0]); got != "「thumbnail maker」近 28 天平均排名 < 10" {
		t.Fatalf("Describe = %q", got)
	}
	if got := Describe(Condition{Kind: KindMetric, Metric: "ctr", Scope: "page", URL: "https://example.com/a", Op: "gte", Value: 0.035, Window: 28}); got != "https://example.com/a近 28 天 GSC CTR ≥ 3.5%" {
		t.Fatalf("Describe ctr = %q", got)
	}
	bads := [][]Condition{
		nil,
		{{Kind: "rank"}},
		{{Kind: KindPosition, Value: 10}},
		{{Kind: KindPosition, Keyword: "x", Value: 0}},
		{{Kind: KindPosition, Keyword: "x", Value: 10, Op: "gt"}},
		{{Kind: KindStatus, URL: "https://other.com/a"}},
		{{Kind: KindStatus, URL: "/a"}},
		{{Kind: KindStatus, URL: "https://example.com/a", Status: 500}},
		{{Kind: KindMetric, Metric: "sessions", Op: "gte", Value: 1}},
		{{Kind: KindMetric, Metric: "clicks", Op: "eq", Value: 1}},
		{{Kind: KindMetric, Metric: "clicks", Op: "gte", Value: 1, Scope: "query"}},
		{{Kind: KindMetric, Metric: "clicks", Op: "gte", Value: 1, Window: 3}},
	}
	for i, conds := range bads {
		if _, err := Validate(conds, site); !errors.Is(err, ErrCondition) {
			t.Errorf("case %d: %v", i, err)
		}
	}
}

func TestPositionTaskPassesOnNewData(t *testing.T) {
	s, site, now := setup(t, "example.com")
	// Before the task: position 14 over the 28 days to 2026-11-08.
	ok(t, s.Store.ReplaceGSC(ctx, site.ID, "query", "2026-10-20", "2026-11-08", []store.GSCRow{
		{Day: "2026-10-20", Query: "thumbnail maker", Impressions: 100, Position: 14}}, 1))
	v, err := s.Propose(ctx, Proposal{SiteID: site.ID, Title: "改首页标题", Days: 28,
		Conditions: []Condition{{Kind: KindPosition, Keyword: "thumbnail maker", Value: 10}}})
	ok(t, err)
	if v.Status != StatusProposed || len(v.Outcomes) != 0 {
		t.Fatalf("proposed = %+v", v)
	}
	if done, _ := s.Evaluate(ctx, site.ID); len(done) != 0 {
		t.Fatal("evaluated a proposed task")
	}
	v, err = s.Confirm(ctx, v.ID)
	ok(t, err)
	o := v.Outcomes[0]
	if v.Status != StatusOpen || v.DueAt != now.AddDate(0, 0, 28).Unix() || o.Baseline == nil || *o.Baseline != 14 || o.State != StatePending ||
		!strings.Contains(o.Note, "2026-11-10 之后") {
		t.Fatalf("confirmed = %+v / %+v", v.Task, o)
	}
	if _, err := s.Confirm(ctx, v.ID); !errors.Is(err, ErrState) {
		t.Fatalf("confirmed twice: %v", err)
	}

	// Data after the task opened, but less than 7 full days of it once the
	// newest 2 provisional days are excluded: still pending, even though 12
	// already beats the baseline of 14.
	*now = now.AddDate(0, 0, 5)
	ok(t, s.Store.ReplaceGSC(ctx, site.ID, "query", "2026-11-10", "2026-11-12", []store.GSCRow{
		{Day: "2026-11-11", Query: "thumbnail maker", Impressions: 100, Position: 12}}, 1))
	done, err := s.Evaluate(ctx, site.ID)
	ok(t, err)
	v, _ = s.Check(ctx, v.ID)
	if len(done) != 0 || v.Status != StatusOpen || v.Outcomes[0].State != StatePending || !strings.Contains(v.Outcomes[0].Note, "不满 7 天") {
		t.Fatalf("too little data yet: %+v", v.Outcomes)
	}

	// 9 raw days in (7 usable once the newest 2 are excluded): enough data now, and it passes.
	ok(t, s.Store.ReplaceGSC(ctx, site.ID, "query", "2026-11-13", "2026-11-20", []store.GSCRow{
		{Day: "2026-11-16", Query: "thumbnail maker", Impressions: 300, Position: 7}}, 1))
	done, err = s.Evaluate(ctx, site.ID)
	ok(t, err)
	if len(done) != 1 || done[0].Status != StatusPassed || *done[0].Outcomes[0].Current != 8.3 || done[0].DoneAt != now.Unix() {
		t.Fatalf("passed = %+v", done)
	}
}

func TestTaskFailsWhenDue(t *testing.T) {
	s, site, now := setup(t, "example.com")
	v, err := s.Propose(ctx, Proposal{SiteID: site.ID, Title: "多拿点击", Days: 7, Open: true,
		Conditions: []Condition{{Kind: KindMetric, Metric: "clicks", Op: "gte", Value: 100}}})
	ok(t, err)
	if v.Status != StatusOpen || v.Outcomes[0].State != StatePending {
		t.Fatalf("open = %+v", v)
	}
	// Synced a bit past the due date (2026-11-17), so the 2 newest days excluded as
	// provisional still leave exactly the 7 days needed up to the due date.
	ok(t, s.Store.ReplaceGSC(ctx, site.ID, "date", "2026-11-10", "2026-11-19", []store.GSCRow{{Day: "2026-11-12", Clicks: 40, Impressions: 900}}, 1))
	*now = now.AddDate(0, 0, 7)
	done, err := s.Evaluate(ctx, site.ID)
	ok(t, err)
	if len(done) != 1 || done[0].Status != StatusFailed || *done[0].Outcomes[0].Current != 40 {
		t.Fatalf("done = %+v", done)
	}
	if done, _ := s.Evaluate(ctx, site.ID); len(done) != 0 {
		t.Fatal("a failed task was evaluated again")
	}
}

func TestStatusAndIndexedConditions(t *testing.T) {
	s, site, _ := setup(t, "127.0.0.1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/old":
			http.Redirect(w, r, "/new", http.StatusMovedPermanently)
		case "/new":
			w.Write([]byte("ok"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	v, err := s.Propose(ctx, Proposal{SiteID: site.ID, Title: "旧网址 301，新页面收录", Open: true, Conditions: []Condition{
		{Kind: KindStatus, URL: srv.URL + "/old", Status: 301},
		{Kind: KindStatus, URL: srv.URL + "/new"},
		{Kind: KindIndexed, URL: srv.URL + "/new"},
	}})
	ok(t, err)
	if v.Outcomes[0].State != StatePass || v.Outcomes[1].State != StatePass || v.Outcomes[2].State != StatePending || v.Status != StatusOpen {
		t.Fatalf("outcomes = %+v", v.Outcomes)
	}
	watched, err := s.Watched(ctx, site.ID)
	ok(t, err)
	if len(watched) != 1 || watched[0] != srv.URL+"/new" {
		t.Fatalf("Watched = %v", watched)
	}
	ok(t, s.Store.SetInspection(ctx, site.ID, srv.URL+"/new", "NEUTRAL", "Discovered - currently not indexed", s.now().Unix()))
	v, _ = s.Check(ctx, v.ID)
	if v.Outcomes[2].State != StateUnmet || !strings.Contains(v.Outcomes[2].Note, "Discovered") {
		t.Fatalf("not indexed yet: %+v", v.Outcomes[2])
	}
	ok(t, s.Store.SetInspection(ctx, site.ID, srv.URL+"/new", "PASS", "Submitted and indexed", s.now().Unix()))
	done, err := s.Evaluate(ctx, site.ID)
	ok(t, err)
	if len(done) != 1 || done[0].Status != StatusPassed {
		t.Fatalf("done = %+v", done)
	}
}

func TestIndexedConditionIgnoresInspectionFromBeforeTheTaskStarted(t *testing.T) {
	s, site, now := setup(t, "127.0.0.1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	defer srv.Close()
	// Already indexed before the task existed: must not satisfy page_indexed on its own.
	ok(t, s.Store.SetInspection(ctx, site.ID, srv.URL+"/new", "PASS", "Submitted and indexed", now.Add(-time.Hour).Unix()))
	v, err := s.Propose(ctx, Proposal{SiteID: site.ID, Title: "新页面收录", Open: true,
		Conditions: []Condition{{Kind: KindIndexed, URL: srv.URL + "/new"}}})
	ok(t, err)
	if v.Outcomes[0].State != StatePending {
		t.Fatalf("a pre-task inspection must not satisfy page_indexed: %+v", v.Outcomes[0])
	}
	// A fresh inspection, after the task started, does.
	ok(t, s.Store.SetInspection(ctx, site.ID, srv.URL+"/new", "PASS", "Submitted and indexed", now.Add(time.Minute).Unix()))
	done, err := s.Evaluate(ctx, site.ID)
	ok(t, err)
	if len(done) != 1 || done[0].Status != StatusPassed {
		t.Fatalf("done = %+v", done)
	}
}

func TestGSCConditionCapsWindowAtDueDate(t *testing.T) {
	s, site, now := setup(t, "example.com")
	// Days and Window are both >= gscMinDays so the due-date cap, not the
	// minimum-days gate, is what has to exclude the late data below.
	v, err := s.Propose(ctx, Proposal{SiteID: site.ID, Title: "多拿点击", Days: 9, Open: true,
		Conditions: []Condition{{Kind: KindMetric, Metric: "clicks", Op: "gte", Value: 100, Window: 10}}})
	ok(t, err)
	due := v.DueAt
	// Inside the due window: 40 clicks. After the due date, but recent enough
	// that the newest-2-days provisional buffer alone would not exclude it: another
	// 200, which must still not count towards the task.
	ok(t, s.Store.ReplaceGSC(ctx, site.ID, "date", "2026-11-10", "2026-11-25", []store.GSCRow{
		{Day: "2026-11-12", Clicks: 40, Impressions: 900},
		{Day: "2026-11-20", Clicks: 200, Impressions: 900},
	}, 1))
	*now = now.AddDate(0, 0, 20)
	done, err := s.Evaluate(ctx, site.ID)
	ok(t, err)
	if len(done) != 1 || done[0].Status != StatusFailed {
		t.Fatalf("done = %+v (due %d)", done, due)
	}
	if cur := done[0].Outcomes[0].Current; cur == nil || *cur != 40 {
		t.Fatalf("the GSC window must not extend past the due date: outcome = %+v", done[0].Outcomes[0])
	}
}

func TestStatusConditionIgnoredAfterDueDate(t *testing.T) {
	s, site, now := setup(t, "127.0.0.1")
	var gone atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if gone.Load() {
			w.WriteHeader(410)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	v, err := s.Propose(ctx, Proposal{SiteID: site.ID, Title: "旧网址返回 410", Days: 3, Open: true,
		Conditions: []Condition{{Kind: KindStatus, URL: srv.URL + "/old", Status: 410}}})
	ok(t, err)
	if v.Outcomes[0].State != StateUnmet {
		t.Fatalf("expected unmet before the fix: %+v", v.Outcomes[0])
	}
	*now = now.AddDate(0, 0, 10) // well past the 3-day due date
	gone.Store(true)             // fixed, but only now — after the deadline
	done, err := s.Evaluate(ctx, site.ID)
	ok(t, err)
	if len(done) != 1 || done[0].Status != StatusFailed {
		t.Fatalf("a url_status fix made after the due date must not retroactively pass the task: %+v", done)
	}
}

func TestIndexedConditionIgnoredAfterDueDate(t *testing.T) {
	s, site, now := setup(t, "127.0.0.1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	defer srv.Close()
	v, err := s.Propose(ctx, Proposal{SiteID: site.ID, Title: "新页面收录", Days: 3, Open: true,
		Conditions: []Condition{{Kind: KindIndexed, URL: srv.URL + "/new"}}})
	ok(t, err)
	due := v.DueAt
	*now = now.AddDate(0, 0, 10) // well past the 3-day due date
	ok(t, s.Store.SetInspection(ctx, site.ID, srv.URL+"/new", "PASS", "Submitted and indexed", now.Unix()))
	done, err := s.Evaluate(ctx, site.ID)
	ok(t, err)
	if len(done) != 1 || done[0].Status != StatusFailed {
		t.Fatalf("an inspection recorded after the due date (%d) must not retroactively pass the task: %+v", due, done)
	}
}

func TestDismiss(t *testing.T) {
	s, site, _ := setup(t, "example.com")
	v, err := s.Propose(ctx, Proposal{SiteID: site.ID, Title: "x", Conditions: []Condition{{Kind: KindIndexed, URL: "https://example.com/"}}})
	ok(t, err)
	ok(t, s.Dismiss(ctx, v.ID))
	if err := s.Dismiss(ctx, v.ID); !errors.Is(err, ErrState) {
		t.Fatalf("dismissed twice: %v", err)
	}
	if _, err := s.Confirm(ctx, v.ID); !errors.Is(err, ErrState) {
		t.Fatalf("confirmed a dismissed task: %v", err)
	}
	if _, err := s.Propose(ctx, Proposal{SiteID: site.ID, Title: " ", Conditions: []Condition{{Kind: KindIndexed, URL: "https://example.com/"}}}); !errors.Is(err, ErrCondition) {
		t.Fatalf("empty title: %v", err)
	}
}
