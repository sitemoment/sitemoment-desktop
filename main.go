// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"context"
	"embed"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
	_ "time/tzdata" // Windows may lack a zoneinfo database; gscsync needs America/Los_Angeles

	"github.com/sitemoment/sitemoment-desktop/collectors"
	"github.com/sitemoment/sitemoment-desktop/internal/agent"
	"github.com/sitemoment/sitemoment-desktop/internal/app"
	"github.com/sitemoment/sitemoment-desktop/internal/bing"
	"github.com/sitemoment/sitemoment-desktop/internal/bingsync"
	"github.com/sitemoment/sitemoment-desktop/internal/cloud"
	"github.com/sitemoment/sitemoment-desktop/internal/collect"
	"github.com/sitemoment/sitemoment-desktop/internal/gasync"
	"github.com/sitemoment/sitemoment-desktop/internal/google"
	"github.com/sitemoment/sitemoment-desktop/internal/gscsync"
	"github.com/sitemoment/sitemoment-desktop/internal/paths"
	"github.com/sitemoment/sitemoment-desktop/internal/rank"
	"github.com/sitemoment/sitemoment-desktop/internal/secret"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
	"github.com/sitemoment/sitemoment-desktop/internal/webdata"
	"github.com/sitemoment/sitemoment-desktop/internal/webview"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:frontend/dist
var assets embed.FS

// Set at release build time with -ldflags "-X main.version=... -X main.googleClientID=...".
var (
	version            = "dev"
	cloudBase          = "https://sitemoment.com/api"
	googleClientID     = ""
	googleClientSecret = "" // a desktop client's secret is not confidential (Google's own guidance)
)

// runCachePrune is how far back finished collector runs are kept: the
// table also serves as the collectors' cache (longest TTL today is
// google-serp's 7 days), so anything older than that is safe to drop.
const runCacheRetention = 30 * 24 * time.Hour

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// googleSerpCacheHours shortens the cache for an empty google-serp result
// to 1 hour instead of the manifest's normal 7 days: an empty page is far
// more likely to be a transient blip (throttling, a check deferred) than a
// search that truly has zero organic results.
func googleSerpCacheHours(data json.RawMessage) int {
	var v struct {
		Organic []json.RawMessage `json:"organic"`
	}
	if json.Unmarshal(data, &v) == nil && len(v.Organic) == 0 {
		return 1
	}
	return 0
}

func main() {
	dir, err := paths.DataDir()
	if err != nil {
		log.Fatal(err)
	}
	dbPath := filepath.Join(dir, "sitemoment.db")
	st, err := store.Open(dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := st.Migrate(ctx); err != nil {
		log.Fatal(err)
	}
	// A turn or a collector run cannot outlive the process that ran it.
	if _, err := st.ResetRunningAgentSessions(ctx); err != nil {
		log.Fatal(err)
	}
	now := time.Now()
	if err := st.CancelUnfinishedCollectorRuns(ctx, now.Unix()); err != nil {
		log.Fatal(err)
	}
	if err := st.PruneCollectorRuns(ctx, now.Add(-runCacheRetention).Unix()); err != nil {
		log.Fatal(err)
	}
	if _, err := st.PruneWebCache(ctx, now.Unix()); err != nil {
		log.Fatal(err)
	}

	secrets := secret.Keyring{Service: "SiteMoment"}
	httpc := &http.Client{Timeout: 60 * time.Second}
	streamHTTP := &http.Client{} // model answers stream for minutes; each call has its own context deadline
	cl := &cloud.Client{Base: env("SITEMOMENT_API", cloudBase), HTTP: httpc, Secrets: secrets}
	oauth := &google.OAuth{
		ClientID:     env("SITEMOMENT_GOOGLE_CLIENT_ID", googleClientID),
		ClientSecret: env("SITEMOMENT_GOOGLE_CLIENT_SECRET", googleClientSecret),
		HTTP:         httpc,
	}
	gsc := &google.GSC{HTTP: httpc}
	ga := &google.GA{HTTP: httpc}
	bingc := &bing.Client{HTTP: httpc}
	tokens := &app.GoogleTokens{OAuth: oauth, Secrets: secrets}
	bingKeys := &app.BingKeys{Secrets: secrets}

	var wapp *application.App
	open := func(u string) error { return wapp.Browser.OpenURL(u) }
	// Events go to the main window only: Event.Emit runs JS in every window,
	// and a collector window shows a third-party page that could read them.
	var mainWin atomic.Pointer[application.WebviewWindow]
	emit := func(name string, data any) {
		if w := mainWin.Load(); w != nil {
			w.DispatchWailsEvent(&application.CustomEvent{Name: name, Data: data})
		}
	}
	progress := func(p gscsync.Progress) { emit("sync:progress", p) }
	syncSvc := &app.SyncService{
		Sources: []app.Source{
			app.GSCSource{Syncer: &gscsync.Syncer{Store: st, Tokens: tokens, GSC: gsc, Progress: progress}},
			app.GASource{Syncer: &gasync.Syncer{Store: st, Tokens: tokens, GA: ga, Progress: progress}},
			app.BingSource{Syncer: &bingsync.Syncer{Store: st, Keys: bingKeys, Bing: bingc}},
		},
		Store: st, Emit: emit, Ctx: ctx,
	}

	models := &app.Models{Store: st, Secrets: secrets, Cloud: cl, HTTP: streamHTTP}
	browser := &collect.Browser{}
	catalog := &collect.Catalog{Builtin: collectors.FS, Dir: filepath.Join(dir, "collectors"), Store: st, Reserved: agent.ReservedToolNames()}
	if err := catalog.Reload(ctx); err != nil {
		log.Fatal(err)
	}
	runner := &collect.Runner{Store: st, Catalog: catalog, Browser: browser, Emit: emit,
		Post:       map[string]collect.PostFunc{"google-serp": (&collect.GoogleResolver{}).Post},
		CacheHours: map[string]func(data json.RawMessage) int{"google-serp": googleSerpCacheHours},
	}
	windows := &webview.Windows{Browser: browser}
	browser.New = windows.New
	ag := agent.New(st, models.Current, &agent.Deps{
		Fetcher:    rank.NewFetcher(rank.DefaultFetcherConfig()),
		PSI:        webdata.NewPSI(webdata.DefaultPSIURL, ""),
		Wayback:    webdata.NewWayback(webdata.DefaultWaybackURL),
		Suggest:    webdata.NewSuggest(webdata.DefaultSuggestURL),
		Collectors: &app.CollectorTools{Catalog: catalog, Runner: runner},
		Cloud:      cl,
	}, emit)

	wapp = application.New(application.Options{
		Name: "SiteMoment",
		Services: []application.Service{
			application.NewService(&app.AccountService{Cloud: cl, Open: open}),
			application.NewService(&app.GoogleService{OAuth: oauth, GSC: gsc, GA: ga, Tokens: tokens, Store: st, Secrets: secrets, Open: open}),
			application.NewService(&app.BingService{Bing: bingc, Store: st, Secrets: secrets}),
			application.NewService(&app.SiteService{Store: st, Sync: syncSvc.SyncNow}),
			application.NewService(syncSvc),
			application.NewService(&app.OverviewService{Store: st}),
			application.NewService(&app.KeywordService{Store: st, Cloud: cl, Bing: bingc, BingKeys: bingKeys}),
			application.NewService(&app.InfoService{Version: version, DataDir: dir, DBPath: dbPath}),
			application.NewService(&app.ModelService{Models: models}),
			application.NewService(&app.AgentService{Agent: ag, Store: st}),
			application.NewService(&app.CollectorService{Catalog: catalog, Runner: runner, Browser: browser}),
		},
		// The middleware serves collector windows their boot page and keeps
		// them away from everything else the asset server offers.
		Assets:            application.AssetOptions{Handler: application.AssetFileServerFS(assets), Middleware: webview.Middleware},
		RawMessageHandler: windows.RawMessage,
		Mac:               application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
	})
	windows.App = wapp
	win := wapp.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:  "main", // must not start with "collector-" (see webview.Middleware)
		Title: "SiteMoment", Width: 1440, Height: 900, URL: "/",
		BackgroundColour: application.NewRGB(11, 14, 20),
		Mac:              application.MacWindow{TitleBar: application.MacTitleBarHiddenInset, InvisibleTitleBarHeight: 34},
	})
	mainWin.Store(win)
	// Hidden collector windows would keep the app running after the main
	// window closes.
	win.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) { wapp.Quit() })
	go app.RunSyncLoop(ctx, syncSvc, 6*time.Hour)

	if err := wapp.Run(); err != nil {
		log.Fatal(err)
	}
}
