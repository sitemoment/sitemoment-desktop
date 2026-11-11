// SPDX-License-Identifier: AGPL-3.0-or-later

// Package webview is the Wails side of collectors: hidden windows for
// collect.Browser, their navigation events, and the guards that keep the
// pages shown in those windows away from the app. It is the only internal
// package that imports Wails.
package webview

import (
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/collect"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// BootPath is the local page a collector window starts on. Wails runs
// ExecJS only after a window reports "runtime ready", which external pages
// never do, so this page reports it once before the window navigates away.
const BootPath = "/collector-boot.html"

const bootHTML = `<!doctype html>
<meta charset="utf-8">
<title>SiteMoment 采集器</title>
<p>采集器准备中…</p>
<script>
  var msg = "wails:runtime:ready";
  if (window.webkit && window.webkit.messageHandlers && window.webkit.messageHandlers.external) {
    window.webkit.messageHandlers.external.postMessage(msg);
  } else if (window.chrome && window.chrome.webview) {
    window.chrome.webview.postMessage(msg);
  }
</script>
`

// windowPrefix starts every collector window's name (collect.Browser names
// them collector-1, collector-2, …).
const windowPrefix = "collector-"

// windowHeader is set by Wails on every request a webview makes to the
// app's asset server, naming the window (a page cannot set or change it).
const windowHeader = "x-wails-window-name"

// Middleware serves the boot page and answers 403 to every other request
// from a collector window: a page loaded there must not reach the app's
// assets or /wails/runtime, through which the bound Go services are called.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == BootPath {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.Write([]byte(bootHTML))
			return
		}
		if strings.HasPrefix(r.Header.Get(windowHeader), windowPrefix) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Windows makes collector windows in App for Browser.
type Windows struct {
	App     *application.App
	Browser *collect.Browser
	// ReadyTimeout bounds waiting for a new window's boot page (default 20s).
	ReadyTimeout time.Duration
}

// New creates a hidden collector window and waits until it can run scripts.
func (ws *Windows) New(name string) (collect.Window, error) {
	ready := make(chan struct{})
	var once sync.Once
	w := ws.App.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: name, Title: "SiteMoment 采集器", Width: 1100, Height: 800, URL: BootPath, Hidden: true,
	})
	w.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) { once.Do(func() { close(ready) }) })
	onNavigated(w, func() { ws.Browser.Navigated(name) })
	// Closing a shown window (e.g. the user gives up on a check) hides it
	// and ends its run instead of destroying a pooled window.
	w.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		e.Cancel()
		w.Hide()
		ws.Browser.Closed(name)
	})
	timeout := ws.ReadyTimeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	select {
	case <-ready:
		return window{w}, nil
	case <-time.After(timeout):
		return nil, errors.New("webview: the collector window did not start")
	}
}

// RawMessage is the app's RawMessageHandler: messages from collector
// windows' main frames go to the Browser, everything else is ignored.
// Whether a message is from the main frame (as opposed to an iframe the
// collector's page embeds) is checked by isMainFrame, which differs by
// platform: see mainframe_darwin.go and mainframe_fallback.go.
func (ws *Windows) RawMessage(w application.Window, msg string, o *application.OriginInfo) {
	name := w.Name()
	if !strings.HasPrefix(name, windowPrefix) {
		return
	}
	origin := ""
	if o != nil {
		if !isMainFrame(o) {
			return
		}
		origin = o.Origin
	}
	ws.Browser.Handle(name, msg, origin)
}

type window struct{ w *application.WebviewWindow }

func (x window) Load(url string)  { x.w.SetURL(url) }
func (x window) Inject(js string) { x.w.ExecJS(js) }
func (x window) Show()            { x.w.Show(); x.w.Focus() }
func (x window) Hide()            { x.w.Hide() }
