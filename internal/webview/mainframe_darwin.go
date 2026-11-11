// SPDX-License-Identifier: AGPL-3.0-or-later
//go:build darwin

package webview

import "github.com/wailsapp/wails/v3/pkg/application"

// isMainFrame reports whether o describes the page's top-level document.
// Wails fills in IsMainFrame correctly on macOS.
func isMainFrame(o *application.OriginInfo) bool {
	return o.IsMainFrame
}
