// SPDX-License-Identifier: AGPL-3.0-or-later

package rank

import "time"

// Report is the stored and rendered audit result (the "report" JSON of the
// spec). ID is left empty here; the API fills in the audit's public id.
type Report struct {
	ID         string      `json:"id"`
	URL        string      `json:"url"`
	FinalURL   string      `json:"finalUrl"`
	FetchedAt  time.Time   `json:"fetchedAt"`
	StatusCode int         `json:"statusCode"`
	TimeMs     int64       `json:"timeMs"`
	Bytes      int64       `json:"bytes"`
	HTTPS      bool        `json:"https"`
	Score      int         `json:"score"`
	Checks     []Check     `json:"checks"`
	Page       PageInfo    `json:"page"`
	Site       SiteSummary `json:"site"`
}

// Check statuses and groups.
const (
	StatusPass = "pass"
	StatusWarn = "warn"
	StatusFail = "fail"

	GroupMeta        = "meta"
	GroupContent     = "content"
	GroupLinks       = "links"
	GroupTechnical   = "technical"
	GroupSocial      = "social"
	GroupPerformance = "performance"
)

// Check is one audit check. Detail is a one-sentence plain-English finding
// or fix; Value is the measured value when there is one.
type Check struct {
	ID     string `json:"id"`
	Group  string `json:"group"`
	Status string `json:"status"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Value  any    `json:"value,omitempty"`
}

// PageInfo is what the analyzer extracted from the HTML.
type PageInfo struct {
	Title            string     `json:"title"`
	Description      string     `json:"description"`
	H1               []string   `json:"h1"`
	Headings         Headings   `json:"headings"`
	Words            int        `json:"words"`
	Canonical        string     `json:"canonical"`
	Lang             string     `json:"lang"`
	Robots           string     `json:"robots"`
	Viewport         bool       `json:"viewport"`
	Images           int        `json:"images"`
	ImagesMissingAlt int        `json:"imagesMissingAlt"`
	Links            LinkCounts `json:"links"`
	JSONLDTypes      []string   `json:"jsonLdTypes"`
	OG               OpenGraph  `json:"og"`
	Twitter          Twitter    `json:"twitter"`
}

// Headings counts sub-headings.
type Headings struct {
	H2 int `json:"h2"`
	H3 int `json:"h3"`
}

// LinkCounts counts <a href> links relative to the final host.
type LinkCounts struct {
	Internal int `json:"internal"`
	External int `json:"external"`
	Nofollow int `json:"nofollow"`
}

// OpenGraph holds og:title, og:description and og:image.
type OpenGraph struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Image       string `json:"image"`
}

// Twitter holds the twitter:card value.
type Twitter struct {
	Card string `json:"card"`
}

// SiteSummary is the site-level part of the report.
type SiteSummary struct {
	RobotsTxt   bool `json:"robotsTxt"`
	Sitemap     bool `json:"sitemap"`
	SitemapURLs *int `json:"sitemapUrls"`
}

// SiteInfo is what Audit learned about the site besides the page itself.
// Nil pointers mean "not known".
type SiteInfo struct {
	RobotsTxt            bool // robots.txt answered 2xx
	RobotsBlocksRoot     bool // it disallows "/" for all user agents
	Sitemap              bool // a sitemap was reachable and parsed
	SitemapURLs          *int
	HTTPRedirectsToHTTPS *bool
}
