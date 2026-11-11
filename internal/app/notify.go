// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/collect"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

// Note is one notification.
type Note struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Kind  string `json:"kind"` // "human" (someone has to act), "notice", "update", "info"
}

// NoteFor picks the app events worth an OS notification while the main
// window is in the background: a collector window waiting for someone to
// solve a CAPTCHA (it gives up after 2 minutes), and the M4 notices about
// health checks and tasks. Each already has its place inside the app (the
// collector bar, the notices list), so these go to the OS only (Notifier.OS).
func NoteFor(name string, data any) (Note, bool) {
	switch name {
	case "collect:run":
		if r, ok := data.(collect.RunView); ok && r.Status == store.RunNeedHuman {
			return Note{Title: "采集需要人工验证", Body: fmt.Sprintf("采集器 %s 遇到了验证页面，请在弹出的窗口里完成验证（2 分钟内）。", r.Collector), Kind: "human"}, true
		}
	case "notice:new":
		if n, ok := data.(store.Notice); ok {
			switch n.Kind {
			// A task's title and body are tasks.Describe output
			// ("「keyword」近 28 天平均排名 …", a URL and its status, …): real
			// SEO data that must not sit on a lock screen or notification
			// center. The OS gets a generic note; the in-app notice (the
			// notices list) still has the full text.
			case "task_passed":
				return Note{Title: "任务有结果了", Body: "打开 SiteMoment 查看详情", Kind: "notice"}, true
			case "task_failed":
				return Note{Title: "任务未达成", Body: "打开 SiteMoment 查看详情", Kind: "notice"}, true
			// A failed check's body is the crawl error, which can quote a
			// URL; the title (just the site's host) is fine as is.
			case "health_failed":
				return Note{Title: n.Title, Body: "这次体检没有完成，打开查看原因", Kind: "notice"}, true
			// health_issues' body is only a count of P0 problems: no
			// keywords, URLs or page content, so the full text is fine.
			case "health_issues":
				return Note{Title: n.Title, Body: n.Body, Kind: "notice"}, true
			}
		}
	}
	return Note{}, false
}

// NativeNotifier sends OS notifications (Notification Center, Windows toasts).
type NativeNotifier interface {
	// Available reports whether OS notifications work in this run, or why not.
	Available() (bool, string)
	// Send shows one, asking the user for permission the first time.
	Send(title, body string) error
}

// Settings is the settings table (store.Store).
type Settings interface {
	Setting(ctx context.Context, key string) (string, error)
	SetSetting(ctx context.Context, key, value string) error
}

const keyNativeNotes = "notify.native" // "off" turns OS notifications off

// coalesceWindow and coalesceLimit bound how many OS notifications a burst
// can produce: more than coalesceLimit within coalesceWindow collapse into
// one generic count, so e.g. several sites finishing their health check at
// once do not fire a notification each.
const (
	coalesceWindow = 60 * time.Second
	coalesceLimit  = 2
)

// Notifier shows notifications: Notify puts one in the app (the "notify"
// event the toasts listen to) and sends it through the OS as well; OS sends
// it through the OS only. The OS gets it while the main window is not in
// front and OS notifications are on; a burst of more than coalesceLimit
// within coalesceWindow collapses into one generic count (coalesced).
type Notifier struct {
	Settings Settings
	Emit     func(name string, data any) // to the main window
	Native   NativeNotifier              // nil: in-app only
	Focused  func() bool                 // whether the main window is in front

	now func() time.Time // time.Now; overridden in tests

	mu       sync.Mutex
	winStart time.Time
	winCount int
}

// Notify shows note in the app and, in the background, through the OS.
func (n *Notifier) Notify(ctx context.Context, note Note) {
	if n.Emit != nil {
		n.Emit("notify", note)
	}
	n.OS(ctx, note)
}

// OS sends note through the OS when the main window is in the background
// and OS notifications are available and on.
func (n *Notifier) OS(ctx context.Context, note Note) {
	if n.Native == nil || (n.Focused != nil && n.Focused()) || !n.nativeOn(ctx) {
		return
	}
	if ok, _ := n.Native.Available(); !ok {
		return
	}
	note = n.coalesced(note)
	go func() {
		if err := n.Native.Send(note.Title, note.Body); err != nil {
			log.Printf("app: notification: %v", err)
		}
	}()
}

func (n *Notifier) clock() time.Time {
	if n.now != nil {
		return n.now()
	}
	return time.Now()
}

// coalesced returns note as is for the first coalesceLimit OS notifications
// in a rolling coalesceWindow, and a generic count after that: a burst must
// read as "N new messages", not as N separate notifications with their real
// (possibly sensitive) titles and bodies.
func (n *Notifier) coalesced(note Note) Note {
	n.mu.Lock()
	defer n.mu.Unlock()
	now := n.clock()
	if n.winStart.IsZero() || now.Sub(n.winStart) >= coalesceWindow {
		n.winStart, n.winCount = now, 0
	}
	n.winCount++
	if n.winCount > coalesceLimit {
		return Note{Title: fmt.Sprintf("有 %d 条新消息", n.winCount), Body: "打开 SiteMoment 查看详情", Kind: "info"}
	}
	return note
}

func (n *Notifier) nativeOn(ctx context.Context) bool {
	if n.Settings == nil {
		return true
	}
	v, _ := n.Settings.Setting(ctx, keyNativeNotes)
	return v != "off"
}

// NotifySettings is Settings → 通知.
type NotifySettings struct {
	Native    bool   `json:"native"`    // the user's switch
	Available bool   `json:"available"` // OS notifications work in this run
	Reason    string `json:"reason,omitempty"`
}

// NotifyService backs Settings → 通知.
type NotifyService struct {
	Notifier *Notifier
}

// Get returns the notification settings.
func (s *NotifyService) Get(ctx context.Context) NotifySettings {
	out := NotifySettings{Native: s.Notifier.nativeOn(ctx), Reason: "这个版本不支持系统通知"}
	if s.Notifier.Native != nil {
		out.Available, out.Reason = s.Notifier.Native.Available()
	}
	return out
}

// SetNative turns OS notifications on or off.
func (s *NotifyService) SetNative(ctx context.Context, on bool) (NotifySettings, error) {
	v := ""
	if !on {
		v = "off"
	}
	if err := s.Notifier.Settings.SetSetting(ctx, keyNativeNotes, v); err != nil {
		return NotifySettings{}, err
	}
	return s.Get(ctx), nil
}

// Test shows a test notification in the app and through the OS (asking
// for permission if needed), and reports why the OS one failed.
func (s *NotifyService) Test(ctx context.Context) error {
	note := Note{Title: "SiteMoment 通知测试", Body: "体检发现新问题、任务有结果、采集需要人工验证、发现新版本时，会这样提醒你。", Kind: "info"}
	if s.Notifier.Emit != nil {
		s.Notifier.Emit("notify", note)
	}
	if s.Notifier.Native == nil {
		return fmt.Errorf("这个版本不支持系统通知")
	}
	if ok, why := s.Notifier.Native.Available(); !ok {
		return fmt.Errorf("%s", why)
	}
	return s.Notifier.Native.Send(note.Title, note.Body)
}
