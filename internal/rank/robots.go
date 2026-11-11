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
	// re is pattern compiled once, at parse time (RFC 9309 §2.2.3: "*" is
	// any run of characters, a trailing "$" anchors the end).
	re *regexp.Regexp
}

// newRobotsRule builds a rule with its matcher precompiled, so allowed does
// not recompile a regexp for every rule on every URL it checks.
func newRobotsRule(allow bool, pattern string) robotsRule {
	anchored := strings.HasSuffix(pattern, "$")
	parts := strings.Split(strings.TrimSuffix(pattern, "$"), "*")
	for i := range parts {
		parts[i] = regexp.QuoteMeta(parts[i])
	}
	expr := "^" + strings.Join(parts, ".*")
	if anchored {
		expr += "$"
	}
	re, _ := regexp.Compile(expr) // every part is QuoteMeta'd: this always compiles
	return robotsRule{allow: allow, pattern: pattern, re: re}
}

const (
	// maxRobotsBytes matches Google's own robots.txt parser: anything past
	// the first 500 KiB of the file is ignored.
	maxRobotsBytes = 500 << 10
	// maxRobotsLineBytes bounds a single line; no real directive needs more.
	maxRobotsLineBytes = 256 << 10
)

// parseRobots reads robots.txt (RFC 9309). Only the group for "*" decides
// blocksRoot: it blocks the root when it has "Disallow: /" (or "/*") and no
// "Allow: /". Anything past the first maxRobotsBytes is ignored; a line
// over maxRobotsLineBytes stops the scan, keeping whatever was parsed so
// far rather than failing outright.
func parseRobots(body []byte) robotsInfo {
	body = bytes.TrimPrefix(body, []byte("\xEF\xBB\xBF")) // a leading UTF-8 BOM would otherwise glue onto "User-agent" and hide the first group
	if len(body) > maxRobotsBytes {
		body = body[:maxRobotsBytes]
	}
	var out robotsInfo
	var (
		inStar, prevAgent bool
		disallowAll       bool
		allowRoot         bool
		cur               *robotsGroup
	)
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 0, 64<<10), maxRobotsLineBytes)
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
				cur.rules = append(cur.rules, newRobotsRule(key == "allow", percentNormalize(val)))
			}
		}
		prevAgent = false
	}
	// sc.Err() (e.g. ErrTooLong from a line over maxRobotsLineBytes) just
	// stops the scan early; whatever groups and rules were already parsed
	// are kept rather than discarded.
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
	p = percentNormalize(p)
	agent = strings.ToLower(agent)
	var own, star []robotsRule
	matchedOwn := false
	for _, g := range ri.groups {
		for _, a := range g.agents {
			switch a {
			case agent:
				matchedOwn = true
				own = append(own, g.rules...)
			case "*":
				star = append(star, g.rules...)
			}
		}
	}
	// An explicit (even empty) group for our own agent replaces "*"
	// entirely (RFC 9309 §2.2.1): it must not fall back to the wildcard
	// group just because it happens to have no rules of its own.
	rules := star
	if matchedOwn {
		rules = own
	}
	best, allow := -1, true
	for _, r := range rules {
		if r.re == nil || !r.re.MatchString(p) {
			continue
		}
		if n := len(r.pattern); n > best || (n == best && r.allow) {
			best, allow = n, r.allow
		}
	}
	return allow
}

const hexDigits = "0123456789ABCDEF"

// percentNormalize mirrors how url.EscapedPath encodes a path: every
// non-ASCII byte becomes %XX (upper-case hex) and any %-escape already
// present is upper-cased. Applied to both rule patterns (at parse time) and
// the request path (at match time), so a rule written with raw UTF-8 (e.g.
// "Disallow: /café") or a lower-case escape (e.g. "%2f") still compares
// byte for byte against Go's escaped path, as RFC 9309 §2.2.2 requires.
func percentNormalize(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '%' && i+2 < len(s) && isHexDigit(s[i+1]) && isHexDigit(s[i+2]):
			b.WriteByte('%')
			b.WriteByte(upperHexDigit(s[i+1]))
			b.WriteByte(upperHexDigit(s[i+2]))
			i += 2
		case c < 0x80:
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&0xF])
		}
	}
	return b.String()
}

func isHexDigit(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func upperHexDigit(c byte) byte {
	if c >= 'a' && c <= 'f' {
		return c - 'a' + 'A'
	}
	return c
}
