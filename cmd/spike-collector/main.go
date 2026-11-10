// SPDX-License-Identifier: AGPL-3.0-or-later

// Command spike-collector checks that a hidden Wails window can open Google,
// run an injected collector and post the result back to Go (plan 6, M0).
//
//	go run ./cmd/spike-collector -q "thumbnail maker,youtube thumbnail size"
package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/collect"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed assets
var assetsFS embed.FS

//go:embed google_serp.js
var serpJS string

func main() {
	queries := flag.String("q", "thumbnail maker", "comma-separated queries")
	show := flag.Bool("show", false, "show the collector window from the start")
	gap := flag.Duration("gap", 10*time.Second, "pause between queries")
	flag.Parse()
	list := strings.Split(*queries, ",")

	assets, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		log.Fatal(err)
	}

	var (
		app     *application.App
		win     *application.WebviewWindow
		mu      sync.Mutex
		run     collect.Run
		index   int
		booted  bool
		started time.Time
	)
	next := func() {
		mu.Lock()
		defer mu.Unlock()
		if index >= len(list) {
			log.Printf("done")
			app.Quit()
			return
		}
		q := strings.TrimSpace(list[index])
		index++
		run = collect.NewRun("www.google.com")
		started = time.Now()
		target := "https://www.google.com/search?q=" + url.QueryEscape(q) + "&gl=us&hl=en&num=10"
		log.Printf("query %q -> %s", q, target)
		win.SetURL(target)
	}

	app = application.New(application.Options{
		Name:   "spike-collector",
		Assets: application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
		Mac:    application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
		RawMessageHandler: func(w application.Window, msg string, o *application.OriginInfo) {
			origin, top, main := "", "", false
			if o != nil {
				origin, top, main = o.Origin, o.TopOrigin, o.IsMainFrame
			}
			log.Printf("raw: window=%s origin=%q top=%q main=%v len=%d", w.Name(), origin, top, main, len(msg))
			m, err := collect.Parse(msg)
			if err != nil {
				log.Printf("ignored: %v", err)
				return
			}
			mu.Lock()
			cur, took := run, time.Since(started)
			mu.Unlock()
			if err := collect.Verify(cur, m, origin); err != nil {
				log.Printf("rejected: %v", err)
				return
			}
			switch m.Kind {
			case "result":
				fmt.Printf("RESULT after %s: %s\n", took.Round(time.Millisecond), m.Data)
				win.Hide()
				// The run is done: clear it so any late message still
				// carrying this run id (e.g. from a second navigation
				// event on the same page) is rejected by Verify instead
				// of producing a duplicate RESULT.
				mu.Lock()
				run = collect.Run{}
				mu.Unlock()
				time.AfterFunc(*gap, next)
			case "need_human":
				log.Printf("captcha: showing the window; solve it and the page reloads")
				win.Show()
				// Unlike "result", the run is NOT done here: solving the
				// captcha navigates the same window back to the results
				// page for this same query, and onNavigated only
				// re-injects when run.ID is non-empty. Clearing it here
				// would leave the window stuck showing the captcha with
				// nothing ever re-injecting it, until the 3-minute
				// timeout. Keep run as-is so the next navigation re-runs
				// the collector for it.
			default:
				log.Printf("%s: %s", m.Kind, m.Data)
			}
		},
	})

	win = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "collector", Title: "SiteMoment 采集器", Width: 1100, Height: 800,
		URL: "/boot.html", Hidden: !*show,
	})
	win.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) {
		mu.Lock()
		first := !booted
		booted = true
		mu.Unlock()
		if first {
			log.Printf("runtime ready from boot page")
			next()
		}
	})
	onNavigated(win, func() {
		mu.Lock()
		ok, cur := booted, run
		mu.Unlock()
		if !ok || cur.ID == "" {
			return
		}
		log.Printf("navigation finished; injecting run %s", cur.ID)
		win.ExecJS(collect.Script(cur, serpJS))
	})
	time.AfterFunc(3*time.Minute, func() { log.Printf("timeout"); app.Quit() })

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
