// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/google"
	"github.com/sitemoment/sitemoment-desktop/internal/health"
	"github.com/sitemoment/sitemoment-desktop/internal/rank"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
	"github.com/sitemoment/sitemoment-desktop/internal/tasks"
)

// events records emitted events.
type events struct {
	mu   sync.Mutex
	list []string
}

func (e *events) emit(name string, data any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if n, ok := data.(store.Notice); ok {
		name += ":" + n.Kind
	}
	e.list = append(e.list, name)
}

func (e *events) count(name string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, x := range e.list {
		if x == name {
			n++
		}
	}
	return n
}

// opsFor serves a two-page site and returns Ops whose clock the test moves.
func opsFor(t *testing.T) (*Ops, *store.Store, *events, *time.Time, *httptest.Server) {
	t.Helper()
	st := openStore(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, `<html><head><title>Home page of the example site</title></head><body><h1>Home</h1><a href="/a">a</a></body></html>`)
		case "/a":
			fmt.Fprint(w, `<html><head><title>Page A of the example site</title></head><body><h1>A</h1></body></html>`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	now := time.Date(2026, 11, 10, 9, 0, 0, 0, time.Local)
	clock := func() time.Time { return now }
	ev := &events{}
	o := &Ops{Store: st, Emit: ev.emit, Now: clock,
		Checker: &health.Checker{Store: st, Fetcher: rank.NewFetcher(rank.FetcherConfig{AllowPrivate: true}), Now: clock,
			Start: func(string) string { return srv.URL + "/" }},
		Tasks: &tasks.Service{Store: st, Now: clock}}
	return o, st, ev, &now, srv
}

func TestLastSlot(t *testing.T) {
	loc := time.Local
	for _, c := range []struct{ now, want time.Time }{
		{time.Date(2026, 11, 10, 9, 0, 0, 0, loc), time.Date(2026, 11, 10, 8, 0, 0, 0, loc)},
		{time.Date(2026, 11, 10, 8, 0, 0, 0, loc), time.Date(2026, 11, 10, 8, 0, 0, 0, loc)},
		{time.Date(2026, 11, 10, 7, 59, 0, 0, loc), time.Date(2026, 11, 9, 8, 0, 0, 0, loc)},
	} {
		if got := lastSlot(c.now); !got.Equal(c.want) {
			t.Errorf("lastSlot(%v) = %v, want %v", c.now, got, c.want)
		}
	}
}

func TestRunDueChecksDailyAndReportsWeekly(t *testing.T) {
	o, st, ev, now, _ := opsFor(t)
	a, _ := st.AddSite(ctx, "a.example.com", 1)
	b, _ := st.AddSite(ctx, "b.example.com", 1)

	o.RunDue(ctx)
	for _, id := range []int64{a.ID, b.ID} {
		if c, err := st.LatestCrawl(ctx, id); err != nil || c.Origin != "schedule" || c.Score != 92 { // no sitemap: P1
			t.Fatalf("site %d: %+v, %v", id, c, err)
		}
		if r, _ := st.Reports(ctx, id); len(r) != 1 || r[0].Origin != "weekly" {
			t.Fatalf("site %d reports: %+v", id, r)
		}
	}
	if ev.count("notice:new:report_ready") != 2 || ev.count("health:status") != 4 {
		t.Fatalf("events = %v", ev.list)
	}
	// Later the same day: nothing is due.
	*now = now.Add(5 * time.Hour)
	o.RunDue(ctx)
	if list, _ := st.Crawls(ctx, a.ID, 10); len(list) != 1 {
		t.Fatalf("checked again the same day: %d", len(list))
	}
	// Next morning: checked again, but no new weekly report yet.
	*now = time.Date(2026, 11, 11, 8, 30, 0, 0, time.Local)
	o.RunDue(ctx)
	if list, _ := st.Crawls(ctx, a.ID, 10); len(list) != 2 {
		t.Fatalf("not checked the next day: %d", len(list))
	}
	if r, _ := st.Reports(ctx, a.ID); len(r) != 1 {
		t.Fatalf("weekly report after a day: %d", len(r))
	}
	// A week after the first report (at 08:05 the next Tuesday).
	*now = time.Date(2026, 11, 17, 8, 5, 0, 0, time.Local)
	o.RunDue(ctx)
	if r, _ := st.Reports(ctx, a.ID); len(r) != 2 {
		t.Fatalf("no weekly report a week later: %d", len(r))
	}
}

func TestCheckSiteFailureAndBusy(t *testing.T) {
	o, st, ev, _, srv := opsFor(t)
	site, _ := st.AddSite(ctx, "example.com", 1)
	o.running = map[int64]bool{site.ID: true}
	if err := o.CheckSite(ctx, site.ID, "manual"); !errors.Is(err, ErrChecking) {
		t.Fatalf("busy: %v", err)
	}
	h := &HealthService{Ops: o}
	if err := h.Run(ctx, site.ID); !errors.Is(err, ErrChecking) {
		t.Fatalf("Run while busy: %v", err)
	}
	o.running = nil
	srv.Close()
	if err := o.CheckSite(ctx, site.ID, "manual"); !errors.Is(err, health.ErrUnreachable) {
		t.Fatalf("down site: %v", err)
	}
	if ev.count("notice:new:health_failed") != 1 {
		t.Fatalf("events = %v", ev.list)
	}
	v, err := h.Get(ctx, site.ID)
	if err != nil || v.Latest != nil || v.Last == nil || v.Last.Status != "failed" || len(v.History) != 1 {
		t.Fatalf("Get = %+v, %v", v, err)
	}
	if r, _ := st.Reports(ctx, site.ID); len(r) != 0 {
		t.Fatal("a manual check made a weekly report")
	}
}

func TestProposeConfirmAndAutoAccept(t *testing.T) {
	o, st, ev, _, _ := opsFor(t)
	site, _ := st.AddSite(ctx, "example.com", 1)
	ts := &TaskService{Ops: o}
	p := tasks.Proposal{SiteID: site.ID, Title: "修复首页 404", Conditions: []tasks.Condition{{Kind: tasks.KindIndexed, URL: "https://example.com/"}}}
	v, err := o.Propose(ctx, p)
	if err != nil || v.Status != tasks.StatusProposed || ev.count("notice:new:task_proposed") != 1 {
		t.Fatalf("Propose = %+v, %v, %v", v, err, ev.list)
	}
	v, err = ts.Confirm(ctx, v.ID)
	if err != nil || v.Status != tasks.StatusOpen {
		t.Fatalf("Confirm = %+v, %v", v, err)
	}
	if _, err := ts.Confirm(ctx, v.ID); err == nil || !strings.Contains(err.Error(), "状态已经变了") {
		t.Fatalf("second Confirm: %v", err)
	}
	if err := ts.SetAutoAccept(ctx, true); err != nil {
		t.Fatal(err)
	}
	if on, _ := ts.AutoAccept(ctx); !on {
		t.Fatal("auto accept not saved")
	}
	v, err = o.Propose(ctx, p)
	if err != nil || v.Status != tasks.StatusOpen || ev.count("notice:new:task_opened") != 1 {
		t.Fatalf("auto Propose = %+v, %v", v, err)
	}
	list, err := ts.List(ctx, site.ID)
	if err != nil || len(list) != 2 || list[0].Labels[0] != "https://example.com/ 已被谷歌收录" {
		t.Fatalf("List = %+v, %v", list, err)
	}
	nl, err := (&NoticeService{Store: st}).List(ctx)
	if err != nil || nl.Unread != 2 || !strings.HasPrefix(nl.Items[0].Ref, "task:") {
		t.Fatalf("notices = %+v, %v", nl, err)
	}
}

func TestReportExport(t *testing.T) {
	o, st, _, _, _ := opsFor(t)
	site, _ := st.AddSite(ctx, "example.com", 1)
	dir := t.TempDir()
	var asked string
	rs := &ReportService{Ops: o, Save: func(name string) (string, error) {
		asked = name
		if strings.HasSuffix(name, ".md") {
			return "", nil // the user cancelled
		}
		return filepath.Join(dir, name), nil
	}}
	r, err := rs.Generate(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	path, err := rs.Export(ctx, r.ID, "html")
	if err != nil {
		t.Fatal(err)
	}
	if asked != "example.com-周报-2026-11-09.html" {
		t.Fatalf("file name = %q", asked)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.HasPrefix(string(data), "<!doctype html>") {
		t.Fatalf("exported %q: %v", data[:min(len(data), 40)], err)
	}
	if path, err := rs.Export(ctx, r.ID, "md"); path != "" || err != nil {
		t.Fatalf("cancelled export = %q, %v", path, err)
	}
	if list, _ := rs.List(ctx, site.ID); len(list) != 1 || list[0].Origin != "manual" {
		t.Fatalf("List = %+v", list)
	}
}

func TestGSCInspectorNeedsBinding(t *testing.T) {
	g := GSCInspector{GSC: &google.GSC{}, Tokens: tokenFunc(func(context.Context, string) (string, error) { return "tok", nil })}
	if _, err := g.Inspect(ctx, store.Site{Host: "example.com"}, "https://example.com/"); err == nil {
		t.Fatal("inspected a site without Search Console")
	}
}
