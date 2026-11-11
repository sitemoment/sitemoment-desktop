// SPDX-License-Identifier: AGPL-3.0-or-later

package rank

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
)

// maxSitemapChildren bounds how many child sitemaps of an index are read.
const maxSitemapChildren = 50

// ReadSitemap returns up to limit page URLs from a sitemap: a <urlset>, or a
// <sitemapindex> whose child sitemaps are read one level deep. Only URLs on
// the sitemap's own host are returned (and only same-host child sitemaps are
// followed), deduplicated, in document order. A failed child sitemap is
// skipped; a failure of sitemapURL itself is returned (status ≥ 400 as
// FetchError{Reason: "status"}).
func ReadSitemap(ctx context.Context, f *Fetcher, sitemapURL string, limit int) ([]string, error) {
	return readSitemap(ctx, f, sitemapURL, limit, maxSitemapChildren)
}

func readSitemap(ctx context.Context, f *Fetcher, sitemapURL string, limit, maxChildren int) ([]string, error) {
	if limit <= 0 {
		return []string{}, nil
	}
	root, err := url.Parse(sitemapURL)
	if err != nil || root.Host == "" {
		return nil, &FetchError{Reason: "bad_url", Err: errBadURL}
	}
	host := strings.ToLower(root.Hostname())
	sameHost := func(raw string) bool {
		u, err := url.Parse(raw)
		return err == nil && (u.Scheme == "http" || u.Scheme == "https") && strings.ToLower(u.Hostname()) == host
	}

	sm, err := fetchSitemap(ctx, f, sitemapURL)
	if err != nil {
		return nil, err
	}
	out := []string{}
	seen := map[string]bool{}
	add := func(locs []string) {
		for _, l := range locs {
			if len(out) >= limit {
				return
			}
			if sameHost(l) && !seen[l] {
				seen[l] = true
				out = append(out, l)
			}
		}
	}
	add(sm.urls)
	followed := 0
	for _, child := range sm.children {
		if len(out) >= limit || followed >= maxChildren || ctx.Err() != nil {
			break
		}
		if !sameHost(child) {
			continue
		}
		followed++
		c, err := fetchSitemap(ctx, f, child)
		if err != nil {
			continue
		}
		add(c.urls) // a nested index is not followed further
	}
	return out, nil
}

// parsedSitemap is one sitemap document.
type parsedSitemap struct {
	urls     []string // <urlset><url><loc>
	children []string // <sitemapindex><sitemap><loc>
}

var errNotSitemap = errors.New("not a sitemap")

func fetchSitemap(ctx context.Context, f *Fetcher, u string) (*parsedSitemap, error) {
	resp, err := f.Get(ctx, u)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, &FetchError{Reason: "status", Err: fmt.Errorf("HTTP %d", resp.StatusCode)}
	}
	body := resp.Body
	if len(body) > 2 && body[0] == 0x1f && body[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		body, err = io.ReadAll(io.LimitReader(zr, f.cfg.MaxBytes+1))
		if err != nil {
			return nil, err
		}
		if int64(len(body)) > f.cfg.MaxBytes {
			return nil, &FetchError{Reason: "too_large", Err: errors.New("sitemap over the size cap")}
		}
	}
	return parseSitemap(body)
}

// parseSitemap reads a urlset or sitemapindex, ignoring namespaces.
func parseSitemap(body []byte) (*parsedSitemap, error) {
	dec := xml.NewDecoder(bytes.NewReader(body))
	dec.Strict = false
	out := &parsedSitemap{}
	var (
		rootKind string
		stack    []string
		text     strings.Builder
	)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			if rootKind == "" {
				return nil, errNotSitemap
			}
			break // keep what was read before the error
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := strings.ToLower(t.Name.Local)
			if rootKind == "" {
				if name != "urlset" && name != "sitemapindex" {
					return nil, errNotSitemap
				}
				rootKind = name
			}
			stack = append(stack, name)
			text.Reset()
		case xml.CharData:
			text.Write(t)
		case xml.EndElement:
			if n := len(stack); n >= 2 && stack[n-1] == "loc" {
				loc := strings.TrimSpace(text.String())
				if loc != "" {
					switch {
					case rootKind == "urlset" && stack[n-2] == "url":
						out.urls = append(out.urls, loc)
					case rootKind == "sitemapindex" && stack[n-2] == "sitemap":
						out.children = append(out.children, loc)
					}
				}
			}
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			text.Reset()
		}
	}
	if rootKind == "" {
		return nil, errNotSitemap
	}
	return out, nil
}
