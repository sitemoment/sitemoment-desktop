// SPDX-License-Identifier: AGPL-3.0-or-later

// Package agent is the local SEO agent: a tool-calling loop over the
// configured chat model, with local data, free web, collector and
// SiteMoment paid data tools, ported from SiteMoment's backend
// (internal/seoagent). Sessions live in SQLite; progress goes to the UI as
// Wails events.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/cloud"
	"github.com/sitemoment/sitemoment-desktop/internal/rank"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
	"github.com/sitemoment/sitemoment-desktop/internal/webdata"
)

// maxToolText caps what one tool result sends back to the model.
const maxToolText = 10000

// Tool is one function the agent may call.
type Tool struct {
	Name string
	// Label is the product name shown to users ("站点体检").
	Label       string
	Description string
	Params      json.RawMessage
	Kind        string
	// Paid tools spend SiteMoment credits (unless cached).
	Paid bool
	// Subject picks the arg shown after the label ("站点体检：example.com").
	Subject func(args map[string]any) string
	// Quote (paid tools) returns the worst-case credits and whether the answer is cached.
	Quote func(ctx context.Context, env *Env, args map[string]any) (float64, bool, error)
	Run   func(ctx context.Context, env *Env, args map[string]any) (*Result, error)
}

// LabelFor is "<label>：<subject>".
func (t *Tool) LabelFor(args map[string]any) string {
	if t.Subject == nil {
		return t.Label
	}
	if s := strings.TrimSpace(t.Subject(args)); s != "" {
		return t.Label + "：" + truncateRunes(s, 120)
	}
	return t.Label
}

// Result is a tool's answer.
type Result struct {
	// Text goes back to the model (capped at maxToolText).
	Text    string
	Card    *Card
	Credits float64
	Cached  bool
}

// Deps are the clients tools share. Nil clients turn their tools off.
type Deps struct {
	Store      *store.Store
	Fetcher    *rank.Fetcher
	PSI        *webdata.PSI
	Wayback    *webdata.Wayback
	Suggest    *webdata.Suggest
	Collectors Collectors
	Cloud      Cloud
	// Tasks backs create_task and list_tasks (nil: they answer that tasks are unavailable).
	Tasks Tasks
	// CrawlPages caps site_crawl (default 150).
	CrawlPages int
	Now        func() time.Time

	mu sync.Mutex
	// crawls caches site_crawl results, bounded to maxCrawls hosts (LRU) so
	// a long session can't grow it without limit.
	crawls     map[string]*rank.CrawlResult
	crawlOrder []string // least recently used first
	catalog    []cloud.Tool
	catalogAt  time.Time
}

// maxCrawls caps how many hosts' crawl results Deps keeps in memory.
const maxCrawls = 20

// Env is what one tool call sees: the shared clients and the session's site.
type Env struct {
	*Deps
	// Site is the session's site (ID 0 when the session has none).
	Site store.Site
	// SessionID is the session the call belongs to (create_task records it).
	SessionID int64
}

func (d *Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// touchCrawlLocked moves host to the most-recently-used end of crawlOrder
// (appending it if it was not already there). Callers must hold d.mu.
func (d *Deps) touchCrawlLocked(host string) {
	for i, h := range d.crawlOrder {
		if h == host {
			d.crawlOrder = append(d.crawlOrder[:i], d.crawlOrder[i+1:]...)
			break
		}
	}
	d.crawlOrder = append(d.crawlOrder, host)
}

func (d *Deps) rememberCrawl(r *rank.CrawlResult) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.crawls == nil {
		d.crawls = map[string]*rank.CrawlResult{}
	}
	d.crawls[r.Host] = r
	d.touchCrawlLocked(r.Host)
	for len(d.crawlOrder) > maxCrawls {
		oldest := d.crawlOrder[0]
		d.crawlOrder = d.crawlOrder[1:]
		delete(d.crawls, oldest)
	}
}

func (d *Deps) crawlOf(host string) *rank.CrawlResult {
	d.mu.Lock()
	defer d.mu.Unlock()
	host = strings.TrimPrefix(strings.ToLower(host), "www.")
	r := d.crawls[host]
	if r != nil {
		d.touchCrawlLocked(host)
	}
	return r
}

func schema(s string) json.RawMessage { return json.RawMessage(s) }

func subject(k string) func(map[string]any) string {
	return func(a map[string]any) string { return argString(a, k) }
}

// hostOf returns the bare host of a URL or domain ("" when it has none).
func hostOf(s string) string {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}

// siteURL turns "example.com" or a URL into a start URL.
func siteURL(s string) string {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	return s
}

// argString reads a string arg.
func argString(args map[string]any, k string) string {
	s, _ := args[k].(string)
	return strings.TrimSpace(s)
}

// argInt reads a numeric arg with a default and bounds.
func argInt(args map[string]any, k string, def, lo, hi int) int {
	f, ok := args[k].(float64)
	if !ok {
		return def
	}
	return max(lo, min(hi, int(f)))
}

func argBool(args map[string]any, k string) bool {
	b, _ := args[k].(bool)
	return b
}

func argStrings(args map[string]any, k string) []string {
	var out []string
	switch v := args[k].(type) {
	case []any:
		for _, it := range v {
			if s, ok := it.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
	case string:
		for _, s := range strings.Split(v, ",") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

// jsonText marshals v compactly and caps it at maxToolText, marking truncation.
func jsonText(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	return capText(string(raw), maxToolText)
}

// fitJSON marshals v, calling shrink (which edits v and reports whether it
// changed anything) until the JSON fits in maxToolText; as a last resort it caps the text.
func fitJSON(v any, shrink func() bool) string {
	for {
		raw, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf(`{"error":%q}`, err.Error())
		}
		if len(raw) <= maxToolText || !shrink() {
			return capText(string(raw), maxToolText)
		}
	}
}

func capText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut] + " …[truncated]"
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// autoCard renders a web.cafe `data` value whose shape is not documented:
// the first array of objects becomes a table (≤ 6 columns, ≤ 20 rows),
// otherwise top-level scalars become a 指标/数值 table.
func autoCard(title string, data any) *Card {
	if rows, ok := firstObjectArray(data, 0); ok && len(rows) > 0 {
		cols := columnsOf(rows, 6)
		c := &Card{Type: "table", Title: title}
		for _, k := range cols {
			c.Columns = append(c.Columns, k)
		}
		for i, r := range rows {
			if i == 20 {
				c.Note = fmt.Sprintf("共 %d 行，显示前 20 行", len(rows))
				break
			}
			row := make([]any, len(cols))
			for j, k := range cols {
				row[j] = scalar(r[k])
			}
			c.Rows = append(c.Rows, row)
		}
		return c
	}
	obj, ok := data.(map[string]any)
	if !ok {
		return nil
	}
	keys := make([]string, 0, len(obj))
	for k, v := range obj {
		if isScalar(v) {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	sort.Strings(keys)
	c := &Card{Type: "table", Title: title, Columns: []any{"指标", "数值"}}
	for i, k := range keys {
		if i == 12 {
			break
		}
		c.Rows = append(c.Rows, []any{k, scalar(obj[k])})
	}
	return c
}

func firstObjectArray(v any, depth int) ([]map[string]any, bool) {
	if depth > 3 {
		return nil, false
	}
	switch x := v.(type) {
	case []any:
		var out []map[string]any
		for _, it := range x {
			if m, ok := it.(map[string]any); ok {
				out = append(out, m)
			}
		}
		if len(out) > 0 {
			return out, true
		}
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if rows, ok := firstObjectArray(x[k], depth+1); ok {
				return rows, true
			}
		}
	}
	return nil, false
}

// columnsOf picks up to n scalar keys, in first-row order of appearance (sorted for stability).
func columnsOf(rows []map[string]any, n int) []string {
	seen := map[string]bool{}
	var keys []string
	for _, r := range rows[:min(len(rows), 5)] {
		ks := make([]string, 0, len(r))
		for k, v := range r {
			if isScalar(v) && !seen[k] {
				ks = append(ks, k)
			}
		}
		sort.Strings(ks)
		for _, k := range ks {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	if len(keys) > n {
		keys = keys[:n]
	}
	return keys
}

func isScalar(v any) bool {
	switch v.(type) {
	case string, float64, bool, nil, json.Number:
		return true
	}
	return false
}

func scalar(v any) any {
	if s, ok := v.(string); ok {
		return truncateRunes(s, 120)
	}
	return v
}

func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}
