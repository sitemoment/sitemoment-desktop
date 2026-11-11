// SPDX-License-Identifier: AGPL-3.0-or-later

package rank

import (
	"context"
	"net/url"
	"strings"
	"sync"
)

// URLCheck is where an old (previously indexed or archived) URL goes today.
type URLCheck struct {
	URL         string `json:"url"`
	FinalURL    string `json:"finalUrl"`
	FinalStatus int    `json:"finalStatus"`
	Redirected  bool   `json:"redirected"`
	// Permanent: every redirect hop is 301/308 (302/307 pass less signal).
	Permanent bool `json:"permanent"`
	// Lost: the URL ends on 404/410 (or another 4xx/5xx) with no redirect.
	Lost bool `json:"lost"`
	// ToHome: it redirects to the home page, which search engines often treat as a soft 404.
	ToHome bool `json:"toHome"`
	// Suggest is the closest live page to 301 a lost URL to, with a 0–1 similarity.
	Suggest      string  `json:"suggest,omitempty"`
	SuggestScore float64 `json:"suggestScore,omitempty"`
	Error        string  `json:"error,omitempty"`
}

// CheckURLs traces every URL (at most concurrency at a time, default 3) and,
// for lost ones, suggests the most similar indexable page of current.
func CheckURLs(ctx context.Context, f *Fetcher, urls []string, current []CrawledPage, concurrency int) []URLCheck {
	if concurrency <= 0 {
		concurrency = 3
	}
	targets := indexablePages(current)
	out := make([]URLCheck, len(urls))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i, u := range urls {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, u string) {
			defer wg.Done()
			defer func() { <-sem }()
			out[i] = checkURL1(ctx, f, u, targets)
		}(i, u)
	}
	wg.Wait()
	return out
}

func checkURL1(ctx context.Context, f *Fetcher, u string, targets []CrawledPage) URLCheck {
	c := URLCheck{URL: u}
	t, err := f.Trace(ctx, u)
	if err != nil {
		c.Error = "bad_url"
		return c
	}
	c.URL, c.FinalURL, c.FinalStatus, c.Error = t.URL, t.FinalURL, t.FinalStatus, t.Error
	c.Redirected, c.Permanent = t.Redirected(), t.Permanent()
	if c.Redirected && isHome(t.FinalURL) && !isHome(t.URL) {
		c.ToHome = true
	}
	c.Lost = c.Error == "" && c.FinalStatus >= 400
	if c.Lost || c.ToHome {
		c.Suggest, c.SuggestScore = closestPage(t.URL, targets)
	}
	return c
}

func isHome(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && strings.Trim(u.Path, "/") == "" && u.RawQuery == ""
}

// closestPage scores each target by shared path words (Jaccard), with a bonus
// when the last path segment matches, and returns the best one above 0.2.
func closestPage(old string, targets []CrawledPage) (string, float64) {
	ou, err := url.Parse(old)
	if err != nil {
		return "", 0
	}
	ow := pathWords(ou.Path)
	if len(ow) == 0 {
		return "", 0
	}
	oLast := lastSegment(ou.Path)
	best, bestScore := "", 0.0
	for _, p := range targets {
		tu, err := url.Parse(p.FinalURL)
		if err != nil || isHome(p.FinalURL) {
			continue
		}
		tw := pathWords(tu.Path)
		tw = append(tw, words(p.Title)...)
		score := jaccard(ow, tw)
		if oLast != "" && oLast == lastSegment(tu.Path) {
			score += 0.5
		}
		score = min(score, 1)
		if score > bestScore || score == bestScore && best != "" && len(p.FinalURL) < len(best) {
			best, bestScore = p.FinalURL, score
		}
	}
	if bestScore < 0.2 {
		return "", 0
	}
	return best, float64(int(bestScore*100)) / 100
}

func pathWords(p string) []string {
	return strings.FieldsFunc(strings.ToLower(p), func(r rune) bool {
		return r == '/' || r == '-' || r == '_' || r == '.'
	})
}

func lastSegment(p string) string {
	segs := strings.Split(strings.Trim(strings.ToLower(p), "/"), "/")
	return segs[len(segs)-1]
}

func jaccard(a, b []string) float64 {
	sa, sb := map[string]bool{}, map[string]bool{}
	for _, w := range a {
		sa[w] = true
	}
	for _, w := range b {
		sb[w] = true
	}
	inter := 0
	for w := range sa {
		if sb[w] {
			inter++
		}
	}
	union := len(sa) + len(sb) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}
