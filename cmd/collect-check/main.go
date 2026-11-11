// SPDX-License-Identifier: AGPL-3.0-or-later

// Command collect-check runs one built-in collector through the real
// hidden-window browser and prints its JSON, for manual verification. It
// keeps its own database in the data directory (set SITEMOMENT_DATA_DIR).
// If the page asks for a human check it shows the window and waits up to
// two minutes for a person; it never solves checks itself.
//
//	SITEMOMENT_DATA_DIR=/tmp/smd-check go run ./cmd/collect-check -c google-serp -q "thumbnail maker"
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/sitemoment/sitemoment-desktop/collectors"
	"github.com/sitemoment/sitemoment-desktop/internal/collect"
	"github.com/sitemoment/sitemoment-desktop/internal/paths"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
	"github.com/sitemoment/sitemoment-desktop/internal/webview"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

func main() {
	id := flag.String("c", "google-serp", "collector id")
	q := flag.String("q", "thumbnail maker", "the q input")
	flag.Parse()

	dir, err := paths.DataDir()
	if err != nil {
		log.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, "collect-check.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(context.Background()); err != nil {
		log.Fatal(err)
	}
	// Keep collector_runs (also the cache and the daily-count source)
	// from growing forever; the real app (main.go) should do the same.
	if err := st.PruneCollectorRuns(context.Background(), time.Now().AddDate(0, 0, -30).Unix()); err != nil {
		log.Fatal(err)
	}
	cat := &collect.Catalog{Builtin: collectors.FS, Store: st}
	if err := cat.Reload(context.Background()); err != nil {
		log.Fatal(err)
	}
	browser := &collect.Browser{}
	runner := &collect.Runner{
		Store: st, Catalog: cat, Browser: browser,
		Post: map[string]collect.PostFunc{"google-serp": (&collect.GoogleResolver{}).Post},
		// An empty google-serp result (Google's check page never
		// resolved into real results within the collector's wait) is
		// cheap to mistrust: cache it for 1 hour, not the manifest's
		// full week.
		CacheHours: map[string]func(json.RawMessage) int{"google-serp": func(data json.RawMessage) int {
			var s collect.SERP
			json.Unmarshal(data, &s)
			if len(s.Organic) == 0 {
				return 1
			}
			return 0
		}},
		Emit: func(name string, data any) {
			if r, ok := data.(collect.RunView); ok && r.Status == store.RunNeedHuman {
				log.Printf("run %d needs a human check: the window is shown; complete it there (this tool never does)", r.ID)
			}
		},
	}
	ws := &webview.Windows{Browser: browser}
	browser.New = ws.New
	app := application.New(application.Options{
		Name:              "collect-check",
		Assets:            application.AssetOptions{Handler: http.NotFoundHandler(), Middleware: webview.Middleware},
		RawMessageHandler: ws.RawMessage,
	})
	ws.App = app
	code := 0
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		go func() {
			defer app.Quit()
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
			defer cancel()
			start := time.Now()
			res, err := runner.Run(ctx, *id, map[string]any{"q": *q})
			if err != nil {
				fmt.Fprintln(os.Stderr, "ERROR:", collect.Describe(err))
				code = 1
				return
			}
			var pretty any
			json.Unmarshal(res.Data, &pretty)
			out, _ := json.MarshalIndent(map[string]any{"cached": res.Cached, "seconds": time.Since(start).Seconds(), "data": pretty}, "", "  ")
			fmt.Println(string(out))
		}()
	})
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
	os.Exit(code)
}
