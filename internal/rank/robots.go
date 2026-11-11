// SPDX-License-Identifier: AGPL-3.0-or-later

package rank

import (
	"bufio"
	"bytes"
	"strings"
)

// robotsInfo is the part of robots.txt an audit cares about.
type robotsInfo struct {
	blocksRoot bool     // the "*" group disallows the whole site
	sitemaps   []string // Sitemap: lines, in order
}

// parseRobots reads robots.txt. Only the group for "*" decides blocksRoot:
// it blocks the root when it has "Disallow: /" (or "/*") and no "Allow: /".
func parseRobots(body []byte) robotsInfo {
	var out robotsInfo
	var (
		inStar, prevAgent bool
		disallowAll       bool
		allowRoot         bool
	)
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		val = strings.TrimSpace(val)
		switch key {
		case "user-agent":
			if !prevAgent {
				inStar = false // a new group starts
			}
			if val == "*" {
				inStar = true
			}
			prevAgent = true
			continue
		case "sitemap":
			if val != "" {
				out.sitemaps = append(out.sitemaps, val)
			}
		case "disallow":
			if inStar && (val == "/" || val == "/*") {
				disallowAll = true
			}
		case "allow":
			if inStar && (val == "/" || val == "/*") {
				allowRoot = true
			}
		}
		prevAgent = false
	}
	out.blocksRoot = disallowAll && !allowRoot
	return out
}
