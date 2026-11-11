// SPDX-License-Identifier: AGPL-3.0-or-later
package webview

import "github.com/wailsapp/wails/v3/pkg/application"

// mainFrameSameOrigin reports whether o describes a message sent from a
// page's top-level document, by comparing Origin (the sending frame) and
// TopOrigin (the window's top-level frame): on platforms where Wails does
// not fill in IsMainFrame (see isMainFrame), a top-level document has no
// different ancestor, so Origin and TopOrigin are the same string, or both
// empty when Wails reports neither. An iframe's TopOrigin differs from its
// own Origin. This is a pure function of its inputs: it has no build
// constraints and is tested directly, independent of isMainFrame's
// platform-specific wiring.
func mainFrameSameOrigin(o *application.OriginInfo) bool {
	if o == nil {
		return false
	}
	return (o.Origin == "" && o.TopOrigin == "") || o.Origin == o.TopOrigin
}
