// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"errors"
	"testing"
)

func TestTasks(t *testing.T) {
	st := open(t)
	a, err := st.AddSite(ctx, "a.example.com", 1)
	ok(t, err)
	b, err := st.AddSite(ctx, "b.example.com", 1)
	ok(t, err)
	sess, err := st.CreateAgentSession(ctx, a.ID, "confirm", 1)
	ok(t, err)
	t1, err := st.AddTask(ctx, Task{SiteID: a.ID, SessionID: sess.ID, Title: "改标题", Status: "proposed", Conditions: `[{"kind":"url_status"}]`, Days: 28, CreatedAt: 10})
	ok(t, err)
	if t1.ID == 0 || t1.SessionID != sess.ID || t1.Status != "proposed" || t1.Results != "" {
		t.Fatalf("AddTask = %+v", t1)
	}
	_, err = st.AddTask(ctx, Task{SiteID: b.ID, Title: "x", Status: "open", Conditions: "[]", Days: 7, CreatedAt: 11, StartedAt: 11, DueAt: 11 + 7*86400})
	ok(t, err)

	if all, _ := st.Tasks(ctx, 0); len(all) != 2 {
		t.Fatalf("all tasks = %+v", all)
	}
	if open, _ := st.Tasks(ctx, a.ID, "open", "passed"); len(open) != 0 {
		t.Fatalf("site a open = %+v", open)
	}
	t1.Status, t1.StartedAt, t1.DueAt = "open", 20, 20+28*86400
	if done, err := st.UpdateTask(ctx, t1, "proposed"); err != nil || !done {
		t.Fatalf("UpdateTask = %v, %v", done, err)
	}
	if done, _ := st.UpdateTask(ctx, t1, "proposed"); done {
		t.Fatal("updated a task that is no longer proposed")
	}
	got, err := st.Task(ctx, t1.ID)
	ok(t, err)
	if got.Status != "open" || got.DueAt != 20+28*86400 {
		t.Fatalf("Task = %+v", got)
	}
	ok(t, st.DeleteAgentSession(ctx, sess.ID))
	if got, _ := st.Task(ctx, t1.ID); got.SessionID != 0 {
		t.Fatalf("session id survived its session: %+v", got)
	}
	ok(t, st.DeleteTask(ctx, t1.ID))
	if _, err := st.Task(ctx, t1.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted task: %v", err)
	}
}

func TestNotices(t *testing.T) {
	st := open(t)
	site, err := st.AddSite(ctx, "example.com", 1)
	ok(t, err)
	for i := 0; i < keepNotices+5; i++ {
		_, err := st.AddNotice(ctx, Notice{SiteID: site.ID, Kind: "task_passed", Title: "任务已验收", Ref: "task:1", CreatedAt: int64(i)})
		ok(t, err)
	}
	n, err := st.AddNotice(ctx, Notice{Kind: "report_ready", Title: "周报已生成", CreatedAt: 999})
	ok(t, err)
	list, unread, err := st.Notices(ctx, 10)
	ok(t, err)
	if unread != keepNotices || len(list) != 10 || list[0].ID != n.ID || list[0].SiteID != 0 || list[1].Ref != "task:1" {
		t.Fatalf("Notices = %d unread, %+v", unread, list[:2])
	}
	ok(t, st.MarkNoticesRead(ctx, n.ID))
	if _, unread, _ := st.Notices(ctx, 1); unread != keepNotices-1 {
		t.Fatalf("unread after one = %d", unread)
	}
	ok(t, st.MarkNoticesRead(ctx, 0))
	if _, unread, _ := st.Notices(ctx, 1); unread != 0 {
		t.Fatalf("unread after all = %d", unread)
	}
}

func TestGSCKeyStat(t *testing.T) {
	st := open(t)
	site, err := st.AddSite(ctx, "example.com", 1)
	ok(t, err)
	ok(t, st.ReplaceGSC(ctx, site.ID, "query", "2026-11-01", "2026-11-02", []GSCRow{
		{Day: "2026-11-01", Query: "thumbnail maker", Clicks: 2, Impressions: 100, Position: 12},
		{Day: "2026-11-02", Query: "thumbnail maker", Clicks: 6, Impressions: 300, Position: 8},
		{Day: "2026-11-02", Query: "thumbnail maker free", Clicks: 50, Impressions: 500, Position: 3},
	}, 1))
	r, err := st.GSCKeyStat(ctx, site.ID, "query", "thumbnail maker", "2026-11-01", "2026-11-30")
	ok(t, err)
	if r.Clicks != 8 || r.Impressions != 400 || r.Position != 9 || r.CTR != 0.02 {
		t.Fatalf("GSCKeyStat = %+v", r)
	}
	if _, err := st.GSCKeyStat(ctx, site.ID, "country", "us", "2026-11-01", "2026-11-30"); !errors.Is(err, ErrBadQuery) {
		t.Fatalf("bad slice: %v", err)
	}
}
