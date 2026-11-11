// SPDX-License-Identifier: AGPL-3.0-or-later

package rank

import (
	"bufio"
	"bytes"
	"net/url"
	"regexp"
	"strings"
)

// robotsInfo is the part of robots.txt an audit and a crawl care about.
type robotsInfo struct {
	blocksRoot bool     // the "*" group disallows the whole site
	sitemaps   []string // Sitemap: lines, in order
	groups     []robotsGroup
}

// robotsGroup is one group: the user agents it names and its rules.
type robotsGroup struct {
	agents []string // lower-cased product tokens ("*" for all)
	rules  []robotsRule
}

type robotsRule struct {
	allow   bool
	pattern string
}

// parseRobots reads robots.txt (RFC 9309). Only the group for "*" decides
// blocksRoot: it blocks the root when it has "Disallow: /" (or "/*") and no
// "Allow: /".
func parseRobots(body []byte) robotsInfo {
	var out robotsInfo
	var (
		inStar, prevAgent bool
		disallowAll       bool
		allowRoot         bool
		cur               *robotsGroup
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
				out.groups = append(out.groups, robotsGroup{})
				cur = &out.groups[len(out.groups)-1]
			}
			if val == "*" {
				inStar = true
			}
			if tok := strings.ToLower(strings.SplitN(val, "/", 2)[0]); tok != "" {
				cur.agents = append(cur.agents, tok)
			}
			prevAgent = true
			continue
		case "sitemap":
			if val != "" {
				out.sitemaps = append(out.sitemaps, val)
			}
		case "disallow", "allow":
			if inStar && (val == "/" || val == "/*") {
				if key == "disallow" {
					disallowAll = true
				} else {
					allowRoot = true
				}
			}
			// An empty Disallow allows everything: it is no rule at all.
			if cur != nil && val != "" {
				cur.rules = append(cur.rules, robotsRule{allow: key == "allow", pattern: val})
			}
		}
		prevAgent = false
	}
	out.blocksRoot = disallowAll && !allowRoot
	return out
}

// allowed reports whether agent (a product token such as "sitemomentbot")
// may fetch rawURL. The groups naming the agent apply, else the "*" groups;
// the longest matching rule wins and Allow wins a tie (RFC 9309 §2.2.2).
// /robots.txt itself is always allowed.
func (ri *robotsInfo) allowed(agent, rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return true
	}
	p := u.EscapedPath()
	if p == "" {
		p = "/"
	}
	if p == "/robots.txt" {
		return true
	}
	if u.RawQuery != "" {
		p += "?" + u.RawQuery
	}
	agent = strings.ToLower(agent)
	var own, star []robotsRule
	for _, g := range ri.groups {
		for _, a := range g.agents {
			switch a {
			case agent:
				own = append(own, g.rules...)
			case "*":
				star = append(star, g.rules...)
			}
		}
	}
	rules := own
	if rules == nil {
		rules = star
	}
	best, allow := -1, true
	for _, r := range rules {
		if !robotsMatch(r.pattern, p) {
			continue
		}
		if n := len(r.pattern); n > best || (n == best && r.allow) {
			best, allow = n, r.allow
		}
	}
	return allow
}

// robotsMatch matches a rule pattern against a path: "*" is any run of
// characters, a trailing "$" anchors the end, everything else is a prefix.
func robotsMatch(pattern, p string) bool {
	anchored := strings.HasSuffix(pattern, "$")
	parts := strings.Split(strings.TrimSuffix(pattern, "$"), "*")
	for i := range parts {
		parts[i] = regexp.QuoteMeta(parts[i])
	}
	expr := "^" + strings.Join(parts, ".*")
	if anchored {
		expr += "$"
	}
	re, err := regexp.Compile(expr)
	return err == nil && re.MatchString(p)
}
