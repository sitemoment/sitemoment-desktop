// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"context"
	"embed"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
	_ "time/tzdata" // Windows may lack a zoneinfo database; gscsync needs America/Los_Angeles

	"github.com/sitemoment/sitemoment-desktop/internal/app"
	"github.com/sitemoment/sitemoment-desktop/internal/cloud"
	"github.com/sitemoment/sitemoment-desktop/internal/google"
	"github.com/sitemoment/sitemoment-desktop/internal/gscsync"
	"github.com/sitemoment/sitemoment-desktop/internal/paths"
	"github.com/sitemoment/sitemoment-desktop/internal/secret"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
	"github.com/wailsapp/wails/v3/pkg/application"
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

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
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

	secrets := secret.Keyring{Service: "SiteMoment"}
	httpc := &http.Client{Timeout: 60 * time.Second}
	cl := &cloud.Client{Base: env("SITEMOMENT_API", cloudBase), HTTP: httpc, Secrets: secrets}
	oauth := &google.OAuth{
		ClientID:     env("SITEMOMENT_GOOGLE_CLIENT_ID", googleClientID),
		ClientSecret: env("SITEMOMENT_GOOGLE_CLIENT_SECRET", googleClientSecret),
		HTTP:         httpc,
	}
	gsc := &google.GSC{HTTP: httpc}
	tokens := &app.GoogleTokens{OAuth: oauth, Secrets: secrets}

	var wapp *application.App
	open := func(u string) error { return wapp.Browser.OpenURL(u) }
	emit := func(name string, data any) {
		if wapp != nil {
			wapp.Event.Emit(name, data)
		}
	}
	syncer := &gscsync.Syncer{Store: st, Tokens: tokens, GSC: gsc, Progress: func(p gscsync.Progress) { emit("sync:progress", p) }}
	syncSvc := &app.SyncService{Syncer: syncer, Store: st, Emit: emit}

	wapp = application.New(application.Options{
		Name: "SiteMoment",
		Services: []application.Service{
			application.NewService(&app.AccountService{Cloud: cl, Open: open}),
			application.NewService(&app.GoogleService{OAuth: oauth, GSC: gsc, Tokens: tokens, Store: st, Secrets: secrets, Open: open}),
			application.NewService(&app.SiteService{Store: st, Sync: syncSvc.SyncNow}),
			application.NewService(syncSvc),
			application.NewService(&app.OverviewService{Store: st}),
			application.NewService(&app.InfoService{Version: version, DataDir: dir, DBPath: dbPath}),
		},
		Assets: application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
		Mac:    application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
	})
	wapp.Window.NewWithOptions(application.WebviewWindowOptions{
		Title: "SiteMoment", Width: 1440, Height: 900, URL: "/",
		BackgroundColour: application.NewRGB(11, 14, 20),
		Mac:              application.MacWindow{TitleBar: application.MacTitleBarHiddenInset, InvisibleTitleBarHeight: 34},
	})
	go app.RunSyncLoop(ctx, syncSvc, 6*time.Hour)

	if err := wapp.Run(); err != nil {
		log.Fatal(err)
	}
}
