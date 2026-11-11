// SPDX-License-Identifier: AGPL-3.0-or-later
package collect

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SERP is the google-serp collector's output.
type SERP struct {
	URL        string    `json:"url"`
	Query      string    `json:"query"`
	GL         string    `json:"gl"`
	HL         string    `json:"hl"`
	Location   string    `json:"location"`
	AIOverview bool      `json:"aiOverview"`
	Organic    []Organic `json:"organic"`
	Related    []string  `json:"related"`
	PAA        []string  `json:"paa"`
}

// Organic is one organic result. URLFrom says how URL was found: "direct"
// (the link itself), "redirect" (Google's /url redirect, decoded or
// followed) or "breadcrumb" (only the site, from the displayed breadcrumb).
type Organic struct {
	Rank       int    `json:"rank"`
	Title      string `json:"title"`
	URL        string `json:"url"`
	URLFrom    string `json:"urlFrom"`
	Domain     string `json:"domain"`
	Breadcrumb string `json:"breadcrumb"`
	Snippet    string `json:"snippet"`
	Redirect   string `json:"redirect,omitempty"`
}

// GoogleResolver turns the opaque "/url?q=<token>" links of a Google
// results page into real URLs. In the hidden WebView Google links most
// results through such tokens; a plain GET of the link (without following
// it, no cookies) answers 302 with the target in Location. HEAD does not
// work (200 without Location). Right after a search Google throttles a
// burst of these GETs: it answers 200 with a page that refreshes to
// /goto after a few seconds, so links are resolved one at a time, and the
// throttled ones once more after a pause. Whatever is still unresolved
// falls back to the site from the result's breadcrumb.
type GoogleResolver struct {
	Base    string        // default https://www.google.com
	HTTP    *http.Client  // its Transport is used; redirects are never followed
	Timeout time.Duration // per request; default 5s
	Gap     time.Duration // between requests; default 1s
	Retry   time.Duration // pause before retrying throttled links; default 10s
	Budget  time.Duration // for the whole page; default 45s
	Sleep   func(ctx context.Context, d time.Duration) error
}

const safariUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.0 Safari/605.1.15"

func or(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}

func (g *GoogleResolver) sleep(ctx context.Context, d time.Duration) error {
	if g.Sleep != nil {
		return g.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Post resolves the result's redirect links and fills URL, URLFrom and
// Domain of every organic result.
func (g *GoogleResolver) Post(ctx context.Context, data json.RawMessage) (json.RawMessage, error) {
	var s SERP
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	base := strings.TrimSuffix(g.Base, "/")
	if base == "" {
		base = "https://www.google.com"
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if g.HTTP != nil {
		client.Transport = g.HTTP.Transport
	}
	ctx, cancel := context.WithTimeout(ctx, or(g.Budget, 45*time.Second))
	defer cancel()
	var todo []*Organic
	for i := range s.Organic {
		if o := &s.Organic[i]; o.URL == "" && strings.HasPrefix(o.Redirect, "/url?") {
			todo = append(todo, o)
		}
	}
	for pass := 0; pass < 2 && len(todo) > 0; pass++ {
		if pass > 0 && g.sleep(ctx, or(g.Retry, 10*time.Second)) != nil {
			break
		}
		var throttled []*Organic
		for i, o := range todo {
			if i > 0 && g.sleep(ctx, or(g.Gap, time.Second)) != nil {
				break
			}
			u, retry := follow(ctx, client, base+o.Redirect, or(g.Timeout, 5*time.Second))
			switch {
			case u != "":
				o.URL, o.URLFrom = u, "redirect"
			case retry:
				throttled = append(throttled, o)
			}
		}
		todo = throttled
	}
	for i := range s.Organic {
		o := &s.Organic[i]
		if o.URL == "" {
			if h := breadcrumbHost(o.Breadcrumb); h != "" {
				o.URL, o.URLFrom = "https://"+h+"/", "breadcrumb"
			}
		}
		o.Redirect = ""
		if u, err := url.Parse(o.URL); err == nil && o.URL != "" {
			o.Domain = strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
		}
	}
	return json.Marshal(s)
}

// follow GETs a Google redirect link and returns its http(s) Location;
// retry reports Google's throttle page (200 with a meta refresh).
func follow(ctx context.Context, client *http.Client, link string, timeout time.Duration) (target string, retry bool) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return "", false
	}
	req.Header.Set("User-Agent", safariUA)
	res, err := client.Do(req)
	if err != nil {
		return "", false
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusOK {
		head, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return "", bytes.Contains(head, []byte(`http-equiv="refresh"`)) || bytes.Contains(head, []byte("url=/goto?"))
	}
	if res.StatusCode < 300 || res.StatusCode > 399 {
		return "", false
	}
	u, err := url.Parse(res.Header.Get("Location"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", false
	}
	return u.String(), false
}

// breadcrumbHost is the site of a breadcrumb like "www.kapwing.com › Tools".
func breadcrumbHost(b string) string {
	h, _, _ := strings.Cut(strings.TrimSpace(b), "›")
	h = strings.ToLower(strings.TrimSpace(h))
	h = strings.TrimPrefix(strings.TrimPrefix(h, "https://"), "http://")
	h, _, _ = strings.Cut(h, "/")
	if !hostPattern.MatchString(h) {
		return ""
	}
	return h
}
