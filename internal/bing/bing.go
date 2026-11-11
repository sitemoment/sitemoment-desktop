// SPDX-License-Identifier: AGPL-3.0-or-later

// Package bing is a Bing Webmaster Tools API client (the JSON endpoint,
// authenticated with the user's API key). The key travels in the query
// string, so errors never include the request URL.
package bing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const defaultBase = "https://ssl.bing.com/webmaster/api.svc/json"

// maxAttempts is how many times a throttled or failed request is tried.
const maxAttempts = 3

var (
	// ErrInvalidKey is Bing's error code 3.
	ErrInvalidKey = errors.New("bing: the API key is not valid")
	// ErrThrottled is error code 4 or 5 (Bing throttles with HTTP 400).
	ErrThrottled = errors.New("bing: Bing Webmaster is throttling this key, try again later")
	// ErrNotAuthorized is error code 13 or 14: the key's account cannot read the site.
	ErrNotAuthorized = errors.New("bing: this key cannot read that site")
	// ErrServer is a 5xx on every attempt.
	ErrServer = errors.New("bing: Bing Webmaster server error")
)

// Client calls the Bing Webmaster API.
type Client struct {
	HTTP *http.Client
	Base string // empty: Bing's

	// sleep waits between attempts; nil uses a real, ctx-aware sleep.
	sleep func(context.Context, time.Duration) error
}

// Site is a site in the user's Bing Webmaster account.
type Site struct {
	URL      string `json:"url"`
	Verified bool   `json:"verified"`
}

// Traffic is one day of search clicks and impressions.
type Traffic struct {
	Day                 string
	Clicks, Impressions float64
}

// Stat is one query's (or page's) numbers for the week starting Week. Key
// is the query, or the page URL. Position is the average impression
// position, 0 when Bing has none.
type Stat struct {
	Week, Key                     string
	Clicks, Impressions, Position float64
}

// KeywordWeek is one week of Bing searches for a keyword: Impressions is
// exact match, BroadImpressions broad match.
type KeywordWeek struct {
	Week                          string
	Impressions, BroadImpressions float64
}

var wcfDate = regexp.MustCompile(`^/Date\((-?\d+)([-+]\d{4})?\)/$`)

// ParseDate turns a WCF date ("/Date(1786665600000)/", optionally with an
// offset like "-0700") into YYYY-MM-DD, in the offset's zone (else UTC).
func ParseDate(s string) (string, error) {
	m := wcfDate.FindStringSubmatch(s)
	if m == nil {
		return "", fmt.Errorf("bing: bad date %q", s)
	}
	ms, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return "", fmt.Errorf("bing: bad date %q", s)
	}
	loc := time.UTC
	if off := m[2]; off != "" {
		h, _ := strconv.Atoi(off[1:3])
		mi, _ := strconv.Atoi(off[3:5])
		secs := h*3600 + mi*60
		if off[0] == '-' {
			secs = -secs
		}
		loc = time.FixedZone("", secs)
	}
	return time.UnixMilli(ms).In(loc).Format("2006-01-02"), nil
}

func realSleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// redact drops the request URL (it carries the API key) from a transport error.
func redact(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// get calls method and decodes the "d" payload into out (left alone when
// Bing answers with no data).
func (c *Client) get(ctx context.Context, method string, q url.Values, out any) error {
	sleep := c.sleep
	if sleep == nil {
		sleep = realSleep
	}
	base := c.Base
	if base == "" {
		base = defaultBase
	}
	endpoint := strings.TrimSuffix(base, "/") + "/" + method + "?" + q.Encode()
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			if err := sleep(ctx, time.Duration(attempt-1)*2*time.Second); err != nil {
				return err
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return fmt.Errorf("bing: %s: cannot build the request", method)
		}
		res, err := c.HTTP.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			lastErr = fmt.Errorf("bing: %s: %w", method, redact(err))
			continue
		}
		data, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
		res.Body.Close()
		if err != nil {
			return fmt.Errorf("bing: %s: %w", method, redact(err))
		}
		if res.StatusCode == http.StatusOK {
			return decodeD(method, data, out)
		}
		var e struct {
			ErrorCode int    `json:"ErrorCode"`
			Message   string `json:"Message"`
		}
		json.Unmarshal(data, &e)
		switch {
		case e.ErrorCode == 3:
			return ErrInvalidKey
		case e.ErrorCode == 13 || e.ErrorCode == 14:
			return ErrNotAuthorized
		case e.ErrorCode == 4 || e.ErrorCode == 5 || res.StatusCode == http.StatusTooManyRequests:
			lastErr = ErrThrottled
		case res.StatusCode >= 500:
			lastErr = fmt.Errorf("%w (%s HTTP %d)", ErrServer, method, res.StatusCode)
		default:
			return fmt.Errorf("bing: %s HTTP %d (error code %d)", method, res.StatusCode, e.ErrorCode)
		}
	}
	return lastErr
}

func decodeD(method string, data []byte, out any) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	var env struct {
		D json.RawMessage `json:"d"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("bing: %s: %w", method, err)
	}
	if len(env.D) == 0 || string(env.D) == "null" {
		return nil
	}
	if err := json.Unmarshal(env.D, out); err != nil {
		return fmt.Errorf("bing: %s: %w", method, err)
	}
	return nil
}

// Sites lists the sites in the key's account, by URL.
func (c *Client) Sites(ctx context.Context, key string) ([]Site, error) {
	var raw []struct {
		URL        string `json:"Url"`
		IsVerified bool   `json:"IsVerified"`
	}
	if err := c.get(ctx, "GetUserSites", url.Values{"apikey": {key}}, &raw); err != nil {
		return nil, err
	}
	out := make([]Site, 0, len(raw))
	for _, r := range raw {
		out = append(out, Site{URL: r.URL, Verified: r.IsVerified})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].URL < out[j].URL })
	return out, nil
}

// Traffic is the site's daily clicks and impressions (days without data are
// left out by Bing).
func (c *Client) Traffic(ctx context.Context, key, siteURL string) ([]Traffic, error) {
	var raw []struct {
		Date                string
		Clicks, Impressions float64
	}
	if err := c.get(ctx, "GetRankAndTrafficStats", url.Values{"apikey": {key}, "siteUrl": {siteURL}}, &raw); err != nil {
		return nil, err
	}
	out := make([]Traffic, 0, len(raw))
	for _, r := range raw {
		day, err := ParseDate(r.Date)
		if err != nil {
			return nil, err
		}
		out = append(out, Traffic{Day: day, Clicks: r.Clicks, Impressions: r.Impressions})
	}
	return out, nil
}

// Queries is the site's top queries, one row per query and week.
func (c *Client) Queries(ctx context.Context, key, siteURL string) ([]Stat, error) {
	return c.stats(ctx, "GetQueryStats", key, siteURL)
}

// Pages is the site's top pages, one row per page and week.
func (c *Client) Pages(ctx context.Context, key, siteURL string) ([]Stat, error) {
	return c.stats(ctx, "GetPageStats", key, siteURL)
}

func (c *Client) stats(ctx context.Context, method, key, siteURL string) ([]Stat, error) {
	var raw []struct {
		Query, Date                                string
		Clicks, Impressions, AvgImpressionPosition float64
	}
	if err := c.get(ctx, method, url.Values{"apikey": {key}, "siteUrl": {siteURL}}, &raw); err != nil {
		return nil, err
	}
	out := make([]Stat, 0, len(raw))
	for _, r := range raw {
		week, err := ParseDate(r.Date)
		if err != nil {
			return nil, err
		}
		pos := r.AvgImpressionPosition
		if pos < 0 {
			pos = 0
		}
		out = append(out, Stat{Week: week, Key: r.Query, Clicks: r.Clicks, Impressions: r.Impressions, Position: pos})
	}
	return out, nil
}

// KeywordStats is Bing's weekly search history for q, oldest first.
// country is a lowercase two-letter code ("us"), language a full tag ("en-US").
func (c *Client) KeywordStats(ctx context.Context, key, q, country, language string) ([]KeywordWeek, error) {
	var raw []struct {
		Date                          string
		Impressions, BroadImpressions float64
	}
	if err := c.get(ctx, "GetKeywordStats", url.Values{"apikey": {key}, "q": {q}, "country": {country}, "language": {language}}, &raw); err != nil {
		return nil, err
	}
	out := make([]KeywordWeek, 0, len(raw))
	for _, r := range raw {
		week, err := ParseDate(r.Date)
		if err != nil {
			return nil, err
		}
		out = append(out, KeywordWeek{Week: week, Impressions: r.Impressions, BroadImpressions: r.BroadImpressions})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Week < out[j].Week })
	return out, nil
}
