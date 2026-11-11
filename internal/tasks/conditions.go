// SPDX-License-Identifier: AGPL-3.0-or-later

// Package tasks keeps SEO tasks with structured acceptance conditions and
// checks them automatically: after every data sync and health check, and
// when the user asks. A task passes when all its conditions hold before it
// is due, and fails when it is due and they do not.
package tasks

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

// Condition kinds.
const (
	// KindPosition: a query's Search Console average position over the window is below Value.
	KindPosition = "keyword_position"
	// KindStatus: the URL answers Status (default 200) without being redirected first (301: with a permanent redirect).
	KindStatus = "url_status"
	// KindIndexed: Search Console URL Inspection says the page is on Google.
	KindIndexed = "page_indexed"
	// KindMetric: a Search Console metric of the site, a page or a query over the window compared with Value.
	KindMetric = "metric"
)

// Condition is one acceptance condition (stored as JSON in tasks.conditions).
type Condition struct {
	Kind    string `json:"kind"`
	Keyword string `json:"keyword,omitempty"`
	URL     string `json:"url,omitempty"`
	// Metric is clicks, impressions, ctr or position (metric conditions).
	Metric string `json:"metric,omitempty"`
	// Scope is site, page (URL) or query (Keyword) for metric conditions.
	Scope string `json:"scope,omitempty"`
	// Op is lt, lte, gt or gte.
	Op    string  `json:"op,omitempty"`
	Value float64 `json:"value,omitempty"`
	// Window is how many days of Search Console data count (7–90, default 28).
	Window int `json:"window,omitempty"`
	// Status is the HTTP status url_status expects: 200, 301, 404 or 410.
	Status int `json:"status,omitempty"`
}

// Outcome is a condition's latest check (stored as JSON in tasks.results).
type Outcome struct {
	// State is pass, unmet (checked, not there yet) or pending (no data to decide).
	State    string   `json:"state"`
	Current  *float64 `json:"current,omitempty"`
	Baseline *float64 `json:"baseline,omitempty"`
	Note     string   `json:"note"`
	At       int64    `json:"at"`
}

// Outcome states.
const (
	StatePass    = "pass"
	StateUnmet   = "unmet"
	StatePending = "pending"
)

const (
	maxConditions = 5
	defaultWindow = 28
	maxKeyword    = 200
)

var (
	ops     = map[string]string{"lt": "<", "lte": "≤", "gt": ">", "gte": "≥"}
	metrics = map[string]string{"clicks": "点击", "impressions": "展示", "ctr": "CTR", "position": "平均排名"}
	scopes  = map[string]bool{"site": true, "page": true, "query": true}
	// statuses url_status may expect.
	statuses = map[int]bool{200: true, 301: true, 404: true, 410: true}
)

// ErrCondition wraps every validation error.
var ErrCondition = errors.New("tasks: invalid condition")

func bad(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrCondition, fmt.Sprintf(format, a...))
}

// Validate checks and normalizes conditions for a site: defaults filled in,
// keywords lower-cased, URLs on the site's own host.
func Validate(conds []Condition, site store.Site) ([]Condition, error) {
	if len(conds) == 0 || len(conds) > maxConditions {
		return nil, bad("需要 1–%d 个验收条件", maxConditions)
	}
	out := make([]Condition, len(conds))
	for i, c := range conds {
		c.Keyword = strings.ToLower(strings.Join(strings.Fields(c.Keyword), " "))
		c.URL = strings.TrimSpace(c.URL)
		if c.Window == 0 {
			c.Window = defaultWindow
		}
		switch c.Kind {
		case KindPosition:
			if c.Keyword == "" || len([]rune(c.Keyword)) > maxKeyword {
				return nil, bad("keyword_position 需要 keyword（1–%d 个字符）", maxKeyword)
			}
			if c.Op == "" {
				c.Op = "lt"
			}
			if c.Op != "lt" && c.Op != "lte" {
				return nil, bad("keyword_position 的 op 只能是 lt 或 lte")
			}
			if c.Value < 1 || c.Value > 100 {
				return nil, bad("keyword_position 的 value 是目标排名，1–100")
			}
			c.URL, c.Metric, c.Scope, c.Status = "", "", "", 0
		case KindStatus, KindIndexed:
			if err := sameSite(c.URL, site.Host); err != nil {
				return nil, err
			}
			if c.Kind == KindStatus {
				if c.Status == 0 {
					c.Status = 200
				}
				if !statuses[c.Status] {
					return nil, bad("url_status 的 status 只能是 200、301、404 或 410")
				}
			} else {
				c.Status = 0
			}
			c.Keyword, c.Metric, c.Scope, c.Op, c.Value, c.Window = "", "", "", "", 0, 0
		case KindMetric:
			if _, ok := metrics[c.Metric]; !ok {
				return nil, bad("metric 只能是 clicks、impressions、ctr 或 position")
			}
			if c.Scope == "" {
				c.Scope = "site"
			}
			if !scopes[c.Scope] {
				return nil, bad("scope 只能是 site、page 或 query")
			}
			switch c.Scope {
			case "page":
				if err := sameSite(c.URL, site.Host); err != nil {
					return nil, err
				}
				c.Keyword = ""
			case "query":
				if c.Keyword == "" || len([]rune(c.Keyword)) > maxKeyword {
					return nil, bad("scope=query 需要 keyword")
				}
				c.URL = ""
			default:
				c.URL, c.Keyword = "", ""
			}
			if _, ok := ops[c.Op]; !ok {
				return nil, bad("op 只能是 lt、lte、gt 或 gte")
			}
			if c.Value < 0 {
				return nil, bad("value 不能是负数")
			}
			c.Status = 0
		default:
			return nil, bad("未知的条件类型 %q（可选 keyword_position、url_status、page_indexed、metric）", c.Kind)
		}
		if c.Kind == KindPosition || c.Kind == KindMetric {
			if c.Window < 7 || c.Window > 90 {
				return nil, bad("window 是 7–90 天")
			}
		}
		out[i] = c
	}
	return out, nil
}

// sameSite accepts an absolute http(s) URL on host or one of its subdomains.
func sameSite(raw, host string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return bad("url 要是完整的 http(s) 网址")
	}
	h := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	base := strings.TrimPrefix(host, "www.")
	if h != base && !strings.HasSuffix(h, "."+base) {
		return bad("url 要是这个站点（%s）的网址", host)
	}
	return nil
}

// Describe is a condition in words ("「thumbnail maker」近 28 天平均排名 < 10").
func Describe(c Condition) string {
	switch c.Kind {
	case KindPosition:
		return fmt.Sprintf("「%s」近 %d 天平均排名 %s %s", c.Keyword, c.Window, ops[c.Op], num(c.Value))
	case KindStatus:
		if c.Status == 301 {
			return c.URL + " 301 跳转"
		}
		return fmt.Sprintf("%s 返回 %d", c.URL, c.Status)
	case KindIndexed:
		return c.URL + " 已被谷歌收录"
	case KindMetric:
		subject := "全站"
		switch c.Scope {
		case "page":
			subject = c.URL
		case "query":
			subject = "「" + c.Keyword + "」"
		}
		v := num(c.Value)
		if c.Metric == "ctr" {
			v = num(c.Value*100) + "%"
		}
		return fmt.Sprintf("%s近 %d 天 GSC %s %s %s", subject, c.Window, metrics[c.Metric], ops[c.Op], v)
	}
	return c.Kind
}

func num(v float64) string {
	s := fmt.Sprintf("%.2f", v)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	return s
}

func compare(op string, cur, want float64) bool {
	switch op {
	case "lt":
		return cur < want
	case "lte":
		return cur <= want
	case "gt":
		return cur > want
	case "gte":
		return cur >= want
	}
	return false
}
