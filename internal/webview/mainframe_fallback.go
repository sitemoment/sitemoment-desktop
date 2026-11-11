// SPDX-License-Identifier: AGPL-3.0-or-later
//go:build !darwin

package webview

import "github.com/wailsapp/wails/v3/pkg/application"

// isMainFrame reports whether o describes the page's top-level document.
// On Windows and Linux, Wails never sets IsMainFrame (it is always false),
// so every message would otherwise be dropped as if it came from an
// iframe; fall back to comparing Origin and TopOrigin instead.
func isMainFrame(o *application.OriginInfo) bool {
	return mainFrameSameOrigin(o)
}
