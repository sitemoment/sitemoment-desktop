// SPDX-License-Identifier: AGPL-3.0-or-later

package rank

import (
	"bytes"
	"encoding/json"
	"net/url"
	"strings"
	"unicode"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// facts is everything extracted from a page; PageInfo plus the internals the
// checks need but the report does not show.
type facts struct {
	PageInfo
	titleFound   bool
	headingSkips []string // e.g. "h2→h4"
	headingCount int
	jsonLDBlocks int
	jsonLDBroken int
	parsed       bool
	// internalLinks are same-site link targets without fragments, deduplicated, at most maxInternalLinks.
	internalLinks []string
}

// maxInternalLinks bounds the link targets one page contributes to a crawl.
const maxInternalLinks = 500

// extract parses body as HTML. pageURL (the final URL) resolves relative
// links and decides what is internal.
func extract(pageURL string, body []byte) facts {
	f := facts{PageInfo: PageInfo{H1: []string{}, JSONLDTypes: []string{}}}
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return f
	}
	f.parsed = true
	base, _ := url.Parse(pageURL)
	if base != nil {
		if b := findBase(doc); b != "" {
			if bu, err := base.Parse(b); err == nil {
				base = bu
			}
		}
	}
	pageHost := ""
	if u, err := url.Parse(pageURL); err == nil {
		pageHost = hostKey(u.Hostname())
	}

	var words strings.Builder
	prevLevel := 0
	seenTypes := map[string]bool{}

	var walk func(n *html.Node, visible bool)
	walk = func(n *html.Node, visible bool) {
		if n.Type == html.TextNode {
			if visible {
				words.WriteString(n.Data)
				words.WriteByte(' ')
			}
			return
		}
		if n.Type == html.ElementNode && n.Namespace == "" {
			switch n.DataAtom {
			case atom.Html:
				if f.Lang == "" {
					f.Lang = strings.TrimSpace(attr(n, "lang"))
				}
			case atom.Head:
				visible = false
			case atom.Body:
				visible = true
			case atom.Script:
				typ := strings.ToLower(strings.TrimSpace(attr(n, "type")))
				if i := strings.IndexByte(typ, ';'); i >= 0 {
					typ = strings.TrimSpace(typ[:i])
				}
				if typ == "application/ld+json" {
					f.jsonLDBlocks++
					var v any
					if err := json.Unmarshal([]byte(textOf(n)), &v); err != nil {
						f.jsonLDBroken++
					} else {
						for _, t := range jsonLDTypes(v) {
							if !seenTypes[t] {
								seenTypes[t] = true
								f.JSONLDTypes = append(f.JSONLDTypes, t)
							}
						}
					}
				}
				return
			case atom.Style, atom.Noscript, atom.Template:
				return
			case atom.Title:
				if !f.titleFound {
					f.titleFound = true
					f.Title = collapse(textOf(n))
				}
				return
			case atom.Meta:
				f.meta(n)
			case atom.Link:
				if hasToken(attr(n, "rel"), "canonical") && f.Canonical == "" {
					f.Canonical = resolve(base, attr(n, "href"))
				}
			case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
				level := int(n.Data[1] - '0')
				f.headingCount++
				if level > prevLevel+1 {
					from := "start"
					if prevLevel > 0 {
						from = "h" + string(rune('0'+prevLevel))
					}
					f.headingSkips = append(f.headingSkips, from+"→"+n.Data)
				}
				prevLevel = level
				switch level {
				case 1:
					f.H1 = append(f.H1, collapse(textOf(n)))
				case 2:
					f.Headings.H2++
				case 3:
					f.Headings.H3++
				}
			case atom.Img:
				f.Images++
				if _, ok := attrOK(n, "alt"); !ok {
					f.ImagesMissingAlt++
				}
			case atom.A:
				f.link(base, pageHost, n)
			}
		} else if n.Type == html.ElementNode && n.Namespace == "svg" {
			// SVG text (icons, <title>) is not page copy.
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, visible)
		}
	}
	walk(doc, false)
	f.Words = countWords(words.String())
	return f
}

// meta records the <meta> tags the checks use.
func (f *facts) meta(n *html.Node) {
	name := strings.ToLower(strings.TrimSpace(attr(n, "name")))
	prop := strings.ToLower(strings.TrimSpace(attr(n, "property")))
	content := collapse(attr(n, "content"))
	key := name
	if key == "" {
		key = prop
	}
	switch key {
	case "description":
		if f.Description == "" {
			f.Description = content
		}
	case "robots":
		if f.Robots == "" {
			f.Robots = content
		} else {
			f.Robots += ", " + content
		}
	case "viewport":
		f.Viewport = content != ""
	case "og:title":
		f.OG.Title = content
	case "og:description":
		f.OG.Description = content
	case "og:image":
		f.OG.Image = content
	case "twitter:card":
		f.Twitter.Card = content
	}
}

// link counts one <a href>.
func (f *facts) link(base *url.URL, pageHost string, n *html.Node) {
	href := strings.TrimSpace(attr(n, "href"))
	if href == "" || strings.HasPrefix(href, "#") || base == nil {
		return
	}
	u, err := base.Parse(href)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return
	}
	if hostKey(u.Hostname()) == pageHost {
		f.Links.Internal++
		u.Fragment, u.RawFragment = "", ""
		if t := u.String(); len(f.internalLinks) < maxInternalLinks && !containsString(f.internalLinks, t) {
			f.internalLinks = append(f.internalLinks, t)
		}
	} else {
		f.Links.External++
	}
	if hasToken(attr(n, "rel"), "nofollow") {
		f.Links.Nofollow++
	}
}

// jsonLDTypes collects @type values from a parsed JSON-LD value: the top
// level object, a top-level array and @graph members.
func jsonLDTypes(v any) []string {
	var out []string
	var add func(v any, depth int)
	add = func(v any, depth int) {
		if depth > 3 {
			return
		}
		switch t := v.(type) {
		case []any:
			for _, e := range t {
				add(e, depth+1)
			}
		case map[string]any:
			switch ty := t["@type"].(type) {
			case string:
				out = append(out, ty)
			case []any:
				for _, e := range ty {
					if s, ok := e.(string); ok {
						out = append(out, s)
					}
				}
			}
			if g, ok := t["@graph"]; ok {
				add(g, depth+1)
			}
		}
	}
	add(v, 0)
	return out
}

func findBase(n *html.Node) string {
	if n.Type == html.ElementNode && n.DataAtom == atom.Base {
		return attr(n, "href")
	}
	if n.Type == html.ElementNode && n.DataAtom == atom.Body {
		return ""
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if b := findBase(c); b != "" {
			return b
		}
	}
	return ""
}

func attr(n *html.Node, key string) string {
	v, _ := attrOK(n, key)
	return v
}

func attrOK(n *html.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if a.Namespace == "" && strings.EqualFold(a.Key, key) {
			return a.Val, true
		}
	}
	return "", false
}

// textOf concatenates the text below n.
func textOf(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

// collapse trims and squeezes whitespace.
func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

func hasToken(list, tok string) bool {
	for _, t := range strings.Fields(strings.ToLower(list)) {
		if t == tok {
			return true
		}
	}
	return false
}

func resolve(base *url.URL, href string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}
	if base == nil {
		return href
	}
	u, err := base.Parse(href)
	if err != nil {
		return href
	}
	return u.String()
}

// hostKey compares hosts case-insensitively and treats www.example.com and
// example.com as the same site.
func hostKey(h string) string {
	h = strings.TrimSuffix(strings.ToLower(h), ".")
	return strings.TrimPrefix(h, "www.")
}

// countWords counts runs of letters or digits; each CJK ideograph or kana
// counts as one word, since those scripts do not separate words by spaces.
func countWords(s string) int {
	n, in := 0, false
	for _, r := range s {
		switch {
		case unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana):
			n++
			in = false
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'' && in:
			if !in {
				n++
				in = true
			}
		default:
			in = false
		}
	}
	return n
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
