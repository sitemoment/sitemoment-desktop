// SPDX-License-Identifier: AGPL-3.0-or-later
//go:build darwin

package main

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

func onNavigated(w *application.WebviewWindow, fn func()) {
	w.OnWindowEvent(events.Mac.WebViewDidFinishNavigation, func(*application.WindowEvent) { fn() })
}
