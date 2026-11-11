// SPDX-License-Identifier: AGPL-3.0-or-later

// Package collectors holds the built-in collectors: one directory per
// collector with manifest.json, schema.json and collector.js (see
// internal/collect). testdata holds saved pages for the collector tests in
// frontend/src/collectors.test.ts.
package collectors

import "embed"

//go:embed */manifest.json */schema.json */collector.js
var FS embed.FS
