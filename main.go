// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := application.New(application.Options{
		Name:   "SiteMoment",
		Assets: application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
		Mac:    application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
	})
	app.Window.NewWithOptions(application.WebviewWindowOptions{Title: "SiteMoment", Width: 1440, Height: 900, URL: "/"})
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
