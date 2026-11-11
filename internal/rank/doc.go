// SPDX-License-Identifier: AGPL-3.0-or-later

// Package rank is the SEO audit engine behind Rank: an SSRF-guarded HTTP
// fetcher, an HTML analyzer that turns one page into a scored report of
// checks, robots.txt and sitemap readers, and the page terms used for
// "what to write next".
//
// Everything is safe for concurrent use; there is no package-level state.
package rank
