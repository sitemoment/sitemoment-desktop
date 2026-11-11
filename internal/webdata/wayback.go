// SPDX-License-Identifier: AGPL-3.0-or-later

package webdata

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	pathpkg "path"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DefaultWaybackURL is the Internet Archive (free, no key).
const DefaultWaybackURL = "https://web.archive.org"

// ArchivedURL is one page the Wayback Machine captured with HTTP 200.
type ArchivedURL struct {
	URL string `json:"url"`
	// FirstSeen is the first 200 capture, YYYY-MM-DD.
	FirstSeen string `json:"firstSeen"`
}

// Wayback lists a host's historic pages from the CDX API: the URLs a site
// used to have, to check after a redesign that each still resolves.
type Wayback struct {
	http    *http.Client
	baseURL string
}

// NewWayback builds the client.
func NewWayback(baseURL string, opts ...Option) *Wayback {
	return &Wayback{http: newClient(60*time.Second, opts...), baseURL: baseURL}
}

var archiveSkipExt = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true, ".svg": true, ".ico": true,
	".css": true, ".js": true, ".json": true, ".xml": true, ".txt": true, ".pdf": true, ".zip": true, ".woff2": true,
}

// URLs returns up to limit (≤ 5000) distinct HTML pages captured on host,
// sorted by URL. Asset URLs and the robots/sitemap files are dropped.
func (w *Wayback) URLs(ctx context.Context, host string, limit int) ([]ArchivedURL, error) {
	if limit <= 0 || limit > 5000 {
		limit = 5000
	}
	q := url.Values{}
	q.Set("url", host+"/*")
	q.Set("output", "json")
	q.Set("fl", "original,timestamp")
	q.Add("filter", "statuscode:200")
	q.Add("filter", "mimetype:text/html")
	q.Set("collapse", "urlkey")
	q.Set("limit", strconv.Itoa(limit))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(w.baseURL, "/")+"/cdx/search/cdx?"+q.Encode(), nil)
	if err != nil {
		return nil, HTTPError(err.Error())
	}
	// archive.org answers 429 to Go's default User-Agent.
	req.Header.Set("User-Agent", "SiteMomentBot/1.0 (+https://sitemoment.com/rank)")
	resp, err := w.http.Do(req)
	if err != nil {
		return nil, HTTPError(err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode == 429 {
		return nil, RateLimited(retryAfter(resp.Header, 60, retryAfterCapSecs))
	}
	if !success(resp.StatusCode) {
		return nil, HTTPError(statusText(resp.StatusCode))
	}
	v, err := decodeJSON(readBody(resp.Body))
	if err != nil {
		return nil, DecodeError(err.Error())
	}
	rows, ok := asArr(v)
	if !ok && v != nil {
		return nil, DecodeError("CDX response is not an array")
	}
	seen := map[string]bool{}
	out := []ArchivedURL{}
	for i, row := range rows {
		if i == 0 {
			continue // header
		}
		orig, ok1 := asStr(at(row, 0))
		ts, ok2 := asStr(at(row, 1))
		if !ok1 || !ok2 {
			continue
		}
		u, err := url.Parse(orig)
		if err != nil || u.Host == "" {
			continue
		}
		p := strings.ToLower(u.Path)
		// Query-string captures are tracking or session noise, never pages worth a redirect.
		if u.RawQuery != "" || archiveSkipExt[pathpkg.Ext(p)] || p == "/robots.txt" || hasSkipPrefix(p) {
			continue
		}
		u.Host = strings.TrimSuffix(strings.ToLower(u.Host), ":80")
		u.Host = strings.TrimSuffix(u.Host, ":443")
		u.Fragment = ""
		key := strings.TrimPrefix(u.Host, "www.") + u.RequestURI()
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, ArchivedURL{URL: u.String(), FirstSeen: cdxDate(ts)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].URL < out[j].URL })
	return out, nil
}

func cdxDate(ts string) string {
	if len(ts) < 8 {
		return ""
	}
	return fmt.Sprintf("%s-%s-%s", ts[:4], ts[4:6], ts[6:8])
}

// archiveSkipPrefixes are framework and CDN paths, never content pages.
var archiveSkipPrefixes = []string{"/cdn-cgi/", "/_astro/", "/_next/", "/wp-content/", "/wp-includes/", "/wp-json/", "/static/", "/assets/"}

func hasSkipPrefix(p string) bool {
	for _, pre := range archiveSkipPrefixes {
		if strings.HasPrefix(p, pre) {
			return true
		}
	}
	return false
}
