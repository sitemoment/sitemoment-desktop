// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/collect"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

type fakeNative struct {
	mu   sync.Mutex
	ok   bool
	why  string
	err  error
	sent []string
}

func (f *fakeNative) Available() (bool, string) { return f.ok, f.why }

func (f *fakeNative) Send(title, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, title)
	return f.err
}

func (f *fakeNative) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

func (f *fakeNative) titles() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sent...)
}

func TestNoteForCollectorWaitingForAHuman(t *testing.T) {
	if n, ok := NoteFor("collect:run", collect.RunView{Collector: "google-serp", Status: store.RunNeedHuman}); !ok || n.Kind != "human" || n.Title == "" {
		t.Fatalf("need_human = %+v %v", n, ok)
	}
	if n, ok := NoteFor("notice:new", store.Notice{Kind: "health_issues", Title: "example.com 体检发现新的 P0 问题", Body: "2 个"}); !ok || n.Title != "example.com 体检发现新的 P0 问题" || n.Body != "2 个" {
		t.Fatalf("notice = %+v %v", n, ok)
	}
	for _, kind := range []string{"health_failed", "task_passed", "task_failed"} {
		if _, ok := NoteFor("notice:new", store.Notice{Kind: kind, Title: "x"}); !ok {
			t.Errorf("notice %s made no note", kind)
		}
	}
	for _, c := range []struct {
		name string
		data any
	}{
		{"collect:run", collect.RunView{Status: store.RunRunning}},
		{"collect:run", collect.RunView{Status: store.RunOK}},
		{"notice:new", store.Notice{Kind: "report_ready"}},
		{"notice:new", store.Notice{Kind: "task_proposed"}},
		{"sync:progress", nil},
		{"notify", Note{}},
	} {
		if _, ok := NoteFor(c.name, c.data); ok {
			t.Errorf("%s %+v made a note", c.name, c.data)
		}
	}
}

func TestNotifierInAppAlwaysOSOnlyInTheBackground(t *testing.T) {
	st := openStore(t)
	var events []Note
	focused := true
	nat := &fakeNative{ok: true}
	n := &Notifier{Settings: st, Emit: func(name string, data any) {
		if name == "notify" {
			events = append(events, data.(Note))
		}
	}, Native: nat, Focused: func() bool { return focused }}
	waitSent := func(want int) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for nat.count() != want && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if got := nat.count(); got != want {
			t.Fatalf("OS notifications = %d, want %d", got, want)
		}
	}
	n.Notify(ctx, Note{Title: "a"})
	waitSent(0)
	focused = false
	n.Notify(ctx, Note{Title: "b"})
	waitSent(1)
	n.OS(ctx, Note{Title: "os only"})
	waitSent(2)
	if len(events) != 2 {
		t.Fatalf("OS must not show the note in the app: %d in-app", len(events))
	}
	svc := &NotifyService{Notifier: n}
	if s, err := svc.SetNative(ctx, false); err != nil || s.Native || !s.Available {
		t.Fatalf("SetNative(false) = %+v %v", s, err)
	}
	n.Notify(ctx, Note{Title: "c"})
	waitSent(2)
	if len(events) != 3 {
		t.Fatalf("in-app = %d, want 3", len(events))
	}
	if s, _ := svc.SetNative(ctx, true); !s.Native {
		t.Fatalf("SetNative(true) = %+v", s)
	}
	nat.ok, nat.why = false, "系统通知不可用"
	n.Notify(ctx, Note{Title: "d"})
	waitSent(2)
	if s := svc.Get(ctx); s.Available || s.Reason != "系统通知不可用" {
		t.Fatalf("Get = %+v", s)
	}
	if err := svc.Test(ctx); err == nil {
		t.Fatal("Test must report an unavailable OS notifier")
	}
	nat.ok, nat.err = true, errors.New("denied")
	if err := svc.Test(ctx); err == nil || err.Error() != "denied" {
		t.Fatalf("Test = %v", err)
	}
	if (&NotifyService{Notifier: &Notifier{Settings: st}}).Get(ctx).Available {
		t.Fatal("no native notifier must not be available")
	}
}

// TestNoteForKeepsSEODataOutOfOSNotifications: a task's title and labels
// are tasks.Describe output ("「thumbnail maker」近 28 天平均排名 9.2 < 10",
// a URL and its status, ...) and a failed health check's body is the crawl
// error (which can quote a URL); none of that belongs on the lock screen.
// The in-app notice (store.Notice, the notices list) still has the real
// text; only the OS-bound Note is generic.
func TestNoteForKeepsSEODataOutOfOSNotifications(t *testing.T) {
	n, ok := NoteFor("notice:new", store.Notice{
		Kind: "task_passed", Title: "任务已验收：「thumbnail maker」近 28 天平均排名 < 10",
		Body: "「thumbnail maker」近 28 天平均排名 9.2 < 10", Ref: "task:1",
	})
	if !ok || n.Title != "任务有结果了" || n.Body != "打开 SiteMoment 查看详情" {
		t.Fatalf("task_passed = %+v %v", n, ok)
	}
	n, ok = NoteFor("notice:new", store.Notice{
		Kind: "task_failed", Title: "任务到期未达标：/blog/old-post 301 跳转",
		Body: "https://example.com/blog/old-post 301 跳转", Ref: "task:2",
	})
	if !ok || n.Title != "任务未达成" || n.Body != "打开 SiteMoment 查看详情" {
		t.Fatalf("task_failed = %+v %v", n, ok)
	}
	n, ok = NoteFor("notice:new", store.Notice{
		Kind: "health_failed", Title: "example.com 体检失败",
		Body: "fetch https://example.com/sitemap.xml: timeout", Ref: "crawl:3",
	})
	if !ok || n.Title != "example.com 体检失败" || n.Body != "这次体检没有完成，打开查看原因" {
		t.Fatalf("health_failed = %+v %v", n, ok)
	}
	// health_issues' body is just a count (no keywords, URLs or page
	// content), so it keeps the full text, unlike the two above.
	if n, ok := NoteFor("notice:new", store.Notice{Kind: "health_issues", Title: "example.com 体检发现新的 P0 问题", Body: "2 个"}); !ok || n.Title != "example.com 体检发现新的 P0 问题" || n.Body != "2 个" {
		t.Fatalf("health_issues = %+v %v", n, ok)
	}
}

// TestNotifierCoalescesBurstsWithinAWindow: several sites finishing their
// health check at once (or a stretch of task results) must not fire one OS
// notification each; after the first coalesceLimit, a generic count takes
// over until the 60s window rolls over.
func TestNotifierCoalescesBurstsWithinAWindow(t *testing.T) {
	nat := &fakeNative{ok: true}
	n := &Notifier{Native: nat, Focused: func() bool { return false }}
	now := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	n.now = func() time.Time { return now }
	waitSent := func(want int) []string {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for nat.count() != want && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if got := nat.count(); got != want {
			t.Fatalf("sent = %d, want %d: %v", got, want, nat.titles())
		}
		return nat.titles()
	}
	// Each OS() hands off to its own goroutine (Notifier.OS), so the test
	// waits for one send to land before the next: that only orders the
	// assertions, it does not change the coalescing (still driven by the
	// fake clock, not by how many sends have completed).
	n.OS(ctx, Note{Title: "a"})
	sent := waitSent(1)
	n.OS(ctx, Note{Title: "b"})
	sent = waitSent(2)
	if sent[0] != "a" || sent[1] != "b" {
		t.Fatalf("the first two keep their own titles: %v", sent)
	}
	n.OS(ctx, Note{Title: "c"}) // 3rd within the window: coalesced
	sent = waitSent(3)
	if sent[2] != "有 3 条新消息" {
		t.Fatalf("the 3rd must be a generic count, not the real title: %v", sent)
	}
	n.OS(ctx, Note{Title: "d"}) // 4th: still coalesced, running count
	sent = waitSent(4)
	if sent[3] != "有 4 条新消息" {
		t.Fatalf("the 4th must be a generic count too: %v", sent)
	}
	now = now.Add(61 * time.Second)
	n.OS(ctx, Note{Title: "e"})
	sent = waitSent(5)
	if sent[4] != "e" {
		t.Fatalf("a new window must not coalesce: %v", sent)
	}
}
