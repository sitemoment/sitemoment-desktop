// SPDX-License-Identifier: AGPL-3.0-or-later

package rank

import (
	"context"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// auxBudget bounds the robots.txt, sitemap and http:// fetches made after
// the page itself, so a whole audit stays inside the API's 20 s budget.
const auxBudget = 8 * time.Second

// auditSitemapLimit and auditSitemapChildren bound the sitemap count an
// audit reports (sitemapUrls is the number found within these bounds).
const (
	auditSitemapLimit    = 50000
	auditSitemapChildren = 5
)

// Audit fetches rawURL, checks it is an HTML page (status < 400, text/html or
// application/xhtml+xml), then looks at robots.txt, the sitemap (from robots
// Sitemap: lines on the same host, else /sitemap.xml) and, for HTTPS pages,
// whether the http:// version redirects to HTTPS, and returns the analyzed
// report. Failures to load the page are *FetchError; failures of the extra
// fetches only affect their checks.
func Audit(ctx context.Context, f *Fetcher, rawURL string) (*Report, error) {
	pageURL, err := normalizeURL(rawURL, f.anyPort)
	if err != nil {
		return nil, &FetchError{Reason: "bad_url", Err: err}
	}
	resp, err := f.Get(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, &FetchError{Reason: "status", Err: fmt.Errorf("HTTP %d", resp.StatusCode)}
	}
	if !isHTML(resp) {
		return nil, &FetchError{Reason: "not_html", Err: fmt.Errorf("content type %q", resp.Header.Get("Content-Type"))}
	}

	site := siteInfo(ctx, f, resp)
	return Analyze(pageURL, resp, site), nil
}

// siteInfo runs the site-level fetches concurrently.
func siteInfo(ctx context.Context, f *Fetcher, resp *Response) SiteInfo {
	ctx, cancel := context.WithTimeout(ctx, auxBudget)
	defer cancel()
	var site SiteInfo
	origin, err := originOf(resp.FinalURL)
	if err != nil {
		return site
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { // robots.txt, then the sitemap it names
		defer wg.Done()
		var sitemaps []string
		if r, err := f.Get(ctx, origin.String()+"/robots.txt"); err == nil && r.StatusCode >= 200 && r.StatusCode < 300 {
			site.RobotsTxt = true
			ri := parseRobots(r.Body)
			site.RobotsBlocksRoot = ri.blocksRoot
			sitemaps = ri.sitemaps
		}
		smURL := origin.String() + "/sitemap.xml"
		for _, s := range sitemaps {
			if u, err := url.Parse(s); err == nil && strings.EqualFold(u.Hostname(), origin.Hostname()) {
				smURL = s
				break
			}
		}
		if urls, err := readSitemap(ctx, f, smURL, auditSitemapLimit, auditSitemapChildren); err == nil {
			site.Sitemap = true
			n := len(urls)
			site.SitemapURLs = &n
		}
	}()
	go func() { // does http:// redirect to https://?
		defer wg.Done()
		site.HTTPRedirectsToHTTPS = httpRedirects(ctx, f, resp)
	}()
	wg.Wait()
	return site
}

// httpRedirects answers the HTTP→HTTPS question for the page: true when the
// page fetch itself was upgraded, or the http:// variant of an HTTPS page
// ends on https; false when the http:// variant serves the page without
// redirecting; nil when unknown (no default port, or the variant failed).
func httpRedirects(ctx context.Context, f *Fetcher, resp *Response) *bool {
	yes, no := true, false
	if resp.HTTPSUpgrade {
		return &yes
	}
	fu, err := url.Parse(resp.FinalURL)
	if err != nil || fu.Scheme != "https" || fu.Port() != "" {
		return nil
	}
	v := *fu
	v.Scheme = "http"
	r, err := f.Get(ctx, v.String())
	if err != nil || r.StatusCode >= 400 {
		return nil
	}
	if r.HTTPSUpgrade || strings.HasPrefix(r.FinalURL, "https://") {
		return &yes
	}
	return &no
}

// isHTML accepts text/html and application/xhtml+xml, sniffing when the
// server sends no Content-Type.
func isHTML(resp *Response) bool {
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = http.DetectContentType(resp.Body)
	}
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return false
	}
	return mt == "text/html" || mt == "application/xhtml+xml"
}
