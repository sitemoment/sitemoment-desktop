// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// Crawl is one health check of a site (a row of crawls).
type Crawl struct {
	ID         int64  `json:"id"`
	SiteID     int64  `json:"siteId"`
	Origin     string `json:"origin"` // schedule | manual
	Status     string `json:"status"` // running | done | failed
	StartedAt  int64  `json:"startedAt"`
	FinishedAt int64  `json:"finishedAt"`
	Pages      int    `json:"pages"`
	OKPages    int    `json:"okPages"`
	Score      int    `json:"score"`
	P0         int    `json:"p0"`
	P1         int    `json:"p1"`
	P2         int    `json:"p2"`
	// Summary is the check's JSON summary (health.Summary).
	Summary string `json:"summary"`
	Error   string `json:"error"`
}

// Issue is one graded finding of a check.
type Issue struct {
	CrawlID  int64    `json:"crawlId"`
	Code     string   `json:"code"`
	Severity string   `json:"severity"` // p0 | p1 | p2
	Title    string   `json:"title"`
	Detail   string   `json:"detail"`
	Count    int      `json:"count"`
	URLs     []string `json:"urls"`
	// New: the previous finished check did not report this code.
	New bool `json:"new"`
}

// Page is what the site's checks remember about one URL.
type Page struct {
	URL      string `json:"url"`
	FinalURL string `json:"finalUrl"`
	Status   int    `json:"status"`
	// OK: the last check got 2xx HTML without a redirect.
	OK        bool   `json:"ok"`
	Title     string `json:"title"`
	Canonical string `json:"canonical"`
	Noindex   bool   `json:"noindex"`
	InSitemap bool   `json:"inSitemap"`
	Error     string `json:"error"`
	FirstSeen int64  `json:"firstSeen"`
	LastSeen  int64  `json:"lastSeen"`
	LastOK    int64  `json:"lastOk"`
	// IndexVerdict is the last URL Inspection verdict (PASS, NEUTRAL, FAIL; "" never inspected).
	IndexVerdict   string `json:"indexVerdict"`
	IndexCoverage  string `json:"indexCoverage"`
	IndexCheckedAt int64  `json:"indexCheckedAt"`
}

// SiteHealth is a site's latest finished check, for the site list.
type SiteHealth struct {
	SiteID    int64 `json:"siteId"`
	CrawlID   int64 `json:"crawlId"`
	Score     int   `json:"score"`
	P0        int   `json:"p0"`
	P1        int   `json:"p1"`
	P2        int   `json:"p2"`
	CheckedAt int64 `json:"checkedAt"`
}

const crawlCols = "id, site_id, origin, status, started_at, finished_at, pages, ok_pages, score, p0, p1, p2, summary, error"

func scanCrawl(row interface{ Scan(...any) error }) (Crawl, error) {
	var c Crawl
	err := row.Scan(&c.ID, &c.SiteID, &c.Origin, &c.Status, &c.StartedAt, &c.FinishedAt, &c.Pages, &c.OKPages, &c.Score, &c.P0, &c.P1, &c.P2, &c.Summary, &c.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

// StartCrawl records a running check and returns its id.
func (s *Store) StartCrawl(ctx context.Context, siteID int64, origin string, now int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, "INSERT INTO crawls (site_id, origin, status, started_at) VALUES (?, ?, 'running', ?)", siteID, origin, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// FinishCrawl stores a finished check (c.ID must be a running crawl) and its issues.
func (s *Store) FinishCrawl(ctx context.Context, c Crawl, issues []Issue) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "UPDATE crawls SET status = 'done', finished_at = ?, pages = ?, ok_pages = ?, score = ?, p0 = ?, p1 = ?, p2 = ?, summary = ?, error = '' "+
			"WHERE id = ? AND status = 'running'", c.FinishedAt, c.Pages, c.OKPages, c.Score, c.P0, c.P1, c.P2, c.Summary, c.ID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("store: crawl %d is not running", c.ID)
		}
		for _, is := range issues {
			urls := is.URLs
			if urls == nil {
				urls = []string{}
			}
			raw, _ := json.Marshal(urls)
			if _, err := tx.ExecContext(ctx, "INSERT INTO issues (crawl_id, code, severity, title, detail, count, urls, is_new) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
				c.ID, is.Code, is.Severity, is.Title, is.Detail, is.Count, string(raw), is.New); err != nil {
				return err
			}
		}
		return nil
	})
}

// FailCrawl marks a running check failed.
func (s *Store) FailCrawl(ctx context.Context, id int64, msg string, now int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE crawls SET status = 'failed', error = ?, finished_at = ? WHERE id = ? AND status = 'running'", msg, now, id)
	return err
}

// AbortRunningCrawls fails every running check (at startup: none can still be running).
func (s *Store) AbortRunningCrawls(ctx context.Context, msg string, now int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE crawls SET status = 'failed', error = ?, finished_at = ? WHERE status = 'running'", msg, now)
	return err
}

// Crawl returns one check.
func (s *Store) Crawl(ctx context.Context, id int64) (Crawl, error) {
	return scanCrawl(s.db.QueryRowContext(ctx, "SELECT "+crawlCols+" FROM crawls WHERE id = ?", id))
}

// Crawls lists a site's checks, newest first.
func (s *Store) Crawls(ctx context.Context, siteID int64, limit int) ([]Crawl, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+crawlCols+" FROM crawls WHERE site_id = ? ORDER BY id DESC LIMIT ?", siteID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Crawl{}
	for rows.Next() {
		c, err := scanCrawl(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// LatestCrawl is the site's newest finished check, or ErrNotFound.
func (s *Store) LatestCrawl(ctx context.Context, siteID int64) (Crawl, error) {
	return scanCrawl(s.db.QueryRowContext(ctx, "SELECT "+crawlCols+" FROM crawls WHERE site_id = ? AND status = 'done' ORDER BY id DESC LIMIT 1", siteID))
}

// CrawlBefore is the newest finished check of the site started at or before at, or ErrNotFound.
func (s *Store) CrawlBefore(ctx context.Context, siteID, at int64) (Crawl, error) {
	return scanCrawl(s.db.QueryRowContext(ctx, "SELECT "+crawlCols+" FROM crawls WHERE site_id = ? AND status = 'done' AND started_at <= ? ORDER BY id DESC LIMIT 1", siteID, at))
}

// LastCrawlAt is when the site's newest check (any status) started, or 0.
func (s *Store) LastCrawlAt(ctx context.Context, siteID int64) (int64, error) {
	var at sql.NullInt64
	err := s.db.QueryRowContext(ctx, "SELECT MAX(started_at) FROM crawls WHERE site_id = ?", siteID).Scan(&at)
	return at.Int64, err
}

// CrawlIssues lists a check's issues, most severe first.
func (s *Store) CrawlIssues(ctx context.Context, crawlID int64) ([]Issue, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT crawl_id, code, severity, title, detail, count, urls, is_new FROM issues WHERE crawl_id = ? ORDER BY severity, id", crawlID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Issue{}
	for rows.Next() {
		var is Issue
		var urls string
		if err := rows.Scan(&is.CrawlID, &is.Code, &is.Severity, &is.Title, &is.Detail, &is.Count, &urls, &is.New); err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(urls), &is.URLs) != nil || is.URLs == nil {
			is.URLs = []string{}
		}
		out = append(out, is)
	}
	return out, rows.Err()
}

// SiteHealths lists every site's newest finished check.
func (s *Store) SiteHealths(ctx context.Context) ([]SiteHealth, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT c.site_id, c.id, c.score, c.p0, c.p1, c.p2, c.finished_at FROM crawls c
WHERE c.id = (SELECT MAX(id) FROM crawls WHERE site_id = c.site_id AND status = 'done') ORDER BY c.site_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SiteHealth{}
	for rows.Next() {
		var h SiteHealth
		if err := rows.Scan(&h.SiteID, &h.CrawlID, &h.Score, &h.P0, &h.P1, &h.P2, &h.CheckedAt); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// PruneCrawls keeps the site's newest keep checks (their issues go with them).
func (s *Store) PruneCrawls(ctx context.Context, siteID int64, keep int) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM crawls WHERE site_id = ? AND id NOT IN (SELECT id FROM crawls WHERE site_id = ? ORDER BY id DESC LIMIT ?)", siteID, siteID, keep)
	return err
}

const pageCols = "url, final_url, status, ok, title, canonical, noindex, in_sitemap, error, first_seen, last_seen, last_ok, index_verdict, index_coverage, index_checked_at"

func scanPage(row interface{ Scan(...any) error }) (Page, error) {
	var p Page
	err := row.Scan(&p.URL, &p.FinalURL, &p.Status, &p.OK, &p.Title, &p.Canonical, &p.Noindex, &p.InSitemap, &p.Error,
		&p.FirstSeen, &p.LastSeen, &p.LastOK, &p.IndexVerdict, &p.IndexCoverage, &p.IndexCheckedAt)
	return p, err
}

// SitePages returns everything remembered about a site's pages, by URL.
func (s *Store) SitePages(ctx context.Context, siteID int64) (map[string]Page, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+pageCols+" FROM pages WHERE site_id = ?", siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Page{}
	for rows.Next() {
		p, err := scanPage(rows)
		if err != nil {
			return nil, err
		}
		out[p.URL] = p
	}
	return out, rows.Err()
}

// SavePages upserts what a check saw. FirstSeen, LastOK and the inspection
// fields of existing rows are kept (LastOK moves forward when p.OK).
func (s *Store) SavePages(ctx context.Context, siteID int64, pages []Page) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		st, err := tx.PrepareContext(ctx, "INSERT INTO pages (site_id, url, final_url, status, ok, title, canonical, noindex, in_sitemap, error, first_seen, last_seen, last_ok) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(site_id, url) DO UPDATE SET final_url = excluded.final_url, status = excluded.status, ok = excluded.ok, "+
			"title = excluded.title, canonical = excluded.canonical, noindex = excluded.noindex, in_sitemap = excluded.in_sitemap, error = excluded.error, "+
			"last_seen = excluded.last_seen, last_ok = MAX(pages.last_ok, excluded.last_ok)")
		if err != nil {
			return err
		}
		defer st.Close()
		for _, p := range pages {
			lastOK := int64(0)
			if p.OK {
				lastOK = p.LastSeen
			}
			if _, err := st.ExecContext(ctx, siteID, p.URL, p.FinalURL, p.Status, p.OK, p.Title, p.Canonical, p.Noindex, p.InSitemap, p.Error, p.LastSeen, p.LastSeen, lastOK); err != nil {
				return err
			}
		}
		return nil
	})
}

// SetInspection records a URL Inspection result (adding the URL when the
// checks never saw it, e.g. a task's page).
func (s *Store) SetInspection(ctx context.Context, siteID int64, url, verdict, coverage string, at int64) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO pages (site_id, url, index_verdict, index_coverage, index_checked_at) VALUES (?, ?, ?, ?, ?) "+
		"ON CONFLICT(site_id, url) DO UPDATE SET index_verdict = excluded.index_verdict, index_coverage = excluded.index_coverage, index_checked_at = excluded.index_checked_at",
		siteID, url, verdict, coverage, at)
	return err
}

// InspectionsSince counts the site's URL inspections at or after since.
func (s *Store) InspectionsSince(ctx context.Context, siteID, since int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM pages WHERE site_id = ? AND index_checked_at >= ?", siteID, since).Scan(&n)
	return n, err
}

// PrunePages forgets pages no check has reached since before (inspection-only rows included).
func (s *Store) PrunePages(ctx context.Context, siteID, before int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM pages WHERE site_id = ? AND MAX(last_seen, index_checked_at) < ?", siteID, before)
	return err
}

// PageQuery filters the remembered pages of a site.
type PageQuery struct {
	SiteID int64
	// Filter is all, error (4xx/5xx or failed), noindex, redirect or not_indexed.
	Filter   string
	Contains string
	Limit    int // default 100, at most 500
}

var pageFilters = map[string]string{
	"all":         "",
	"error":       " AND (status >= 400 OR error != '')",
	"noindex":     " AND noindex = 1",
	"redirect":    " AND final_url != '' AND final_url != url",
	"not_indexed": " AND index_verdict != '' AND index_verdict != 'PASS'",
}

// Pages lists remembered pages (problems first, then by URL) and how many match.
func (s *Store) Pages(ctx context.Context, q PageQuery) ([]Page, int, error) {
	if q.Filter == "" {
		q.Filter = "all"
	}
	cond, ok := pageFilters[q.Filter]
	if !ok {
		return nil, 0, fmt.Errorf("%w: page filter %s", ErrBadQuery, q.Filter)
	}
	cond = " WHERE site_id = ? AND last_seen > 0" + cond
	args := []any{q.SiteID}
	if q.Filter == "not_indexed" {
		cond = " WHERE site_id = ?" + pageFilters["not_indexed"]
	}
	if q.Contains != "" {
		cond += " AND instr(lower(url), lower(?)) > 0"
		args = append(args, q.Contains)
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	limit = min(limit, 500)
	var total int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM pages"+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+pageCols+" FROM pages"+cond+" ORDER BY ok, url LIMIT ?", append(args, limit)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Page{}
	for rows.Next() {
		p, err := scanPage(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, p)
	}
	return out, total, rows.Err()
}

// ImpressionPages is the set of pages with Search Console impressions in [from, to] (the "page" slice).
func (s *Store) ImpressionPages(ctx context.Context, siteID int64, from, to string) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT DISTINCT page FROM gsc_facts WHERE site_id = ? AND slice = 'page' AND day BETWEEN ? AND ? AND impressions > 0", siteID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out[p] = true
	}
	return out, rows.Err()
}

// Page returns what the checks remember about one URL, or ErrNotFound.
func (s *Store) Page(ctx context.Context, siteID int64, url string) (Page, error) {
	p, err := scanPage(s.db.QueryRowContext(ctx, "SELECT "+pageCols+" FROM pages WHERE site_id = ? AND url = ?", siteID, url))
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}
