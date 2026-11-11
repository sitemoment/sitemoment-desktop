// SPDX-License-Identifier: AGPL-3.0-or-later

package rank

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// maxPageText caps the visible text returned by FetchPage.
const maxPageText = 20000

// FetchPage is Audit plus the page's visible text (headings marked with
// "#"), at most 20 000 bytes, for a reader that needs the copy itself.
func FetchPage(ctx context.Context, f *Fetcher, rawURL string) (*Report, string, error) {
	pageURL, err := normalizeURL(rawURL, f.anyPort)
	if err != nil {
		return nil, "", &FetchError{Reason: "bad_url", Err: err}
	}
	resp, err := f.Get(ctx, pageURL)
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode >= 400 {
		return nil, "", &FetchError{Reason: "status", Err: fmt.Errorf("HTTP %d", resp.StatusCode)}
	}
	if !isHTML(resp) {
		return nil, "", &FetchError{Reason: "not_html", Err: fmt.Errorf("content type %q", resp.Header.Get("Content-Type"))}
	}
	site := siteInfo(ctx, f, resp)
	return Analyze(pageURL, resp, site), pageText(resp.Body), nil
}

// pageText is the body's visible text, one block per line; headings get "#" marks.
func pageText(body []byte) string {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return ""
	}
	var b strings.Builder
	var line strings.Builder
	flush := func() {
		if t := collapse(line.String()); t != "" && b.Len() < maxPageText {
			b.WriteString(t)
			b.WriteByte('\n')
		}
		line.Reset()
	}
	var walk func(n *html.Node, visible bool)
	walk = func(n *html.Node, visible bool) {
		if n.Type == html.TextNode {
			if visible {
				line.WriteString(n.Data)
				line.WriteByte(' ')
			}
			return
		}
		if n.Type == html.ElementNode {
			if n.Namespace == "svg" {
				return
			}
			switch n.DataAtom {
			case atom.Head, atom.Script, atom.Style, atom.Noscript, atom.Template:
				return
			case atom.Body:
				visible = true
			case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
				flush()
				line.WriteString(strings.Repeat("#", int(n.Data[1]-'0')) + " ")
			case atom.P, atom.Div, atom.Li, atom.Section, atom.Article, atom.Tr, atom.Br, atom.Header, atom.Footer, atom.Nav, atom.Main, atom.Dt, atom.Dd, atom.Blockquote:
				flush()
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, visible)
		}
		if n.Type == html.ElementNode {
			switch n.DataAtom {
			case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6, atom.P, atom.Div, atom.Li, atom.Section, atom.Article, atom.Tr, atom.Dt, atom.Dd, atom.Blockquote:
				flush()
			}
		}
	}
	walk(doc, false)
	flush()
	out := b.String()
	if len(out) > maxPageText {
		cut := maxPageText
		for cut > 0 && out[cut]&0xC0 == 0x80 { // don't split a UTF-8 sequence
			cut--
		}
		out = out[:cut]
	}
	return strings.TrimSpace(out)
}
