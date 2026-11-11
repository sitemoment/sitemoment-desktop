// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// Keyword kinds: a target the user wants to rank for, a query Search
// Console found the site already gets traffic for, or a scene word (how
// people describe the need, from autocomplete and collectors).
const (
	KindTarget = "target"
	KindFound  = "found"
	KindScene  = "scene"
)

var (
	// ErrBadKeyword means the keyword is empty or too long.
	ErrBadKeyword = errors.New("store: a keyword is 1 to 200 characters")
	// ErrBadKind means the kind is not target, found or scene.
	ErrBadKind = errors.New("store: keyword kind must be target, found or scene")
)

// sourceRank orders where a number came from: a number never replaces one
// from a better source.
var sourceRank = map[string]int{"": 0, "bing": 1, "sitemoment": 2}

// metricCols are the keyword numbers (each has <name>, <name>_source, <name>_at).
var metricCols = map[string]bool{"volume": true, "kd": true, "cpc": true}

// NormalizeKeyword lower-cases a keyword and collapses its whitespace.
func NormalizeKeyword(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

func validKind(kind string) bool { return kind == KindTarget || kind == KindFound || kind == KindScene }

// Metric is one keyword number with its source and when it was fetched.
type Metric struct {
	Value  *float64 `json:"value"`
	Source string   `json:"source"` // "" (none), "bing" or "sitemoment"
	At     int64    `json:"at"`
}

// Keyword is a row of the keyword workspace. Clicks, Impressions and
// Position come from Search Console over the window given to Keywords
// (Position is nil when the query had no impressions then).
type Keyword struct {
	ID          int64    `json:"id"`
	SiteID      int64    `json:"siteId"`
	Keyword     string   `json:"keyword"`
	Kind        string   `json:"kind"`
	Volume      Metric   `json:"volume"`
	KD          Metric   `json:"kd"`
	CPC         Metric   `json:"cpc"`
	Notes       string   `json:"notes"`
	CreatedAt   int64    `json:"createdAt"`
	Clicks      float64  `json:"clicks"`
	Impressions float64  `json:"impressions"`
	Position    *float64 `json:"position"`
}

const keywordCols = "id, site_id, keyword, kind, volume, volume_source, volume_at, kd, kd_source, kd_at, cpc, cpc_source, cpc_at, notes, created_at"

func nullable(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	x := v.Float64
	return &x
}

// scanKeyword scans keywordCols, then extra destinations.
func scanKeyword(row interface{ Scan(...any) error }, extra ...any) (Keyword, error) {
	var k Keyword
	var vol, kd, cpc sql.NullFloat64
	dest := []any{&k.ID, &k.SiteID, &k.Keyword, &k.Kind, &vol, &k.Volume.Source, &k.Volume.At, &kd, &k.KD.Source, &k.KD.At,
		&cpc, &k.CPC.Source, &k.CPC.At, &k.Notes, &k.CreatedAt}
	err := row.Scan(append(dest, extra...)...)
	if errors.Is(err, sql.ErrNoRows) {
		return k, ErrNotFound
	}
	k.Volume.Value, k.KD.Value, k.CPC.Value = nullable(vol), nullable(kd), nullable(cpc)
	return k, err
}

// AddKeyword adds raw (normalized) to the site's keywords as kind. An
// existing keyword keeps its numbers and takes the new kind, except that
// "found" never replaces a target or scene the user chose.
func (s *Store) AddKeyword(ctx context.Context, siteID int64, raw, kind string, now int64) (Keyword, error) {
	kw := NormalizeKeyword(raw)
	if kw == "" || len([]rune(kw)) > 200 {
		return Keyword{}, ErrBadKeyword
	}
	if !validKind(kind) {
		return Keyword{}, ErrBadKind
	}
	if _, err := s.db.ExecContext(ctx, "INSERT INTO keywords (site_id, keyword, kind, created_at) VALUES (?, ?, ?, ?) "+
		"ON CONFLICT(site_id, keyword) DO UPDATE SET kind = CASE WHEN excluded.kind = 'found' THEN keywords.kind ELSE excluded.kind END",
		siteID, kw, kind, now); err != nil {
		return Keyword{}, err
	}
	return scanKeyword(s.db.QueryRowContext(ctx, "SELECT "+keywordCols+" FROM keywords WHERE site_id = ? AND keyword = ?", siteID, kw))
}

// Keyword returns one keyword (without Search Console numbers) or ErrNotFound.
func (s *Store) Keyword(ctx context.Context, id int64) (Keyword, error) {
	return scanKeyword(s.db.QueryRowContext(ctx, "SELECT "+keywordCols+" FROM keywords WHERE id = ?", id))
}

// UpdateKeyword sets a keyword's kind and notes.
func (s *Store) UpdateKeyword(ctx context.Context, id int64, kind, notes string) error {
	if !validKind(kind) {
		return ErrBadKind
	}
	if len([]rune(notes)) > 2000 {
		return errors.New("store: notes are at most 2000 characters")
	}
	res, err := s.db.ExecContext(ctx, "UPDATE keywords SET kind = ?, notes = ? WHERE id = ?", kind, notes, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteKeyword removes a keyword (no-op when absent).
func (s *Store) DeleteKeyword(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM keywords WHERE id = ?", id)
	return err
}

// SetKeywordMetric stores a keyword number (metric "volume", "kd" or "cpc")
// from source ("bing" or "sitemoment") fetched at `at`, unless the stored
// number came from a better source. It reports whether it wrote.
func (s *Store) SetKeywordMetric(ctx context.Context, id int64, metric string, value float64, source string, at int64) (bool, error) {
	if !metricCols[metric] {
		return false, errors.New("store: unknown keyword metric " + metric)
	}
	rank, ok := sourceRank[source]
	if !ok || source == "" {
		return false, errors.New("store: unknown keyword source " + source)
	}
	wrote := false
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var old string
		err := tx.QueryRowContext(ctx, "SELECT "+metric+"_source FROM keywords WHERE id = ?", id).Scan(&old)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if sourceRank[old] > rank {
			return nil
		}
		if _, err := tx.ExecContext(ctx, "UPDATE keywords SET "+metric+" = ?, "+metric+"_source = ?, "+metric+"_at = ? WHERE id = ?",
			value, source, at, id); err != nil {
			return err
		}
		wrote = true
		return nil
	})
	return wrote, err
}

// AddFoundKeywords adds the site's top `limit` Search Console queries by
// impressions over [from, to] as "found" keywords, leaving existing
// keywords alone, and returns how many it added.
func (s *Store) AddFoundKeywords(ctx context.Context, siteID int64, from, to string, limit int, now int64) (int, error) {
	res, err := s.db.ExecContext(ctx, "INSERT OR IGNORE INTO keywords (site_id, keyword, kind, created_at) "+
		"SELECT ?, lower(trim(query)), 'found', ? FROM gsc_facts WHERE site_id = ? AND slice = 'query' AND day BETWEEN ? AND ? AND trim(query) <> '' "+
		"GROUP BY lower(trim(query)) ORDER BY SUM(impressions) DESC, lower(trim(query)) LIMIT ?",
		siteID, now, siteID, from, to, limit)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// Keywords lists the site's keywords of kind ("" = all) with their Search
// Console clicks, impressions and impression-weighted position over [from,
// to]: targets first, then scene words, then found words, each by
// impressions.
func (s *Store) Keywords(ctx context.Context, siteID int64, kind, from, to string) ([]Keyword, error) {
	if kind != "" && !validKind(kind) {
		return nil, ErrBadKind
	}
	cols := "k." + strings.ReplaceAll(keywordCols, ", ", ", k.")
	rows, err := s.db.QueryContext(ctx, `
WITH g AS (
  SELECT lower(trim(query)) AS q, SUM(clicks) AS c, SUM(impressions) AS i,
         SUM(position * impressions) / NULLIF(SUM(impressions), 0) AS p
  FROM gsc_facts WHERE site_id = ? AND slice = 'query' AND day BETWEEN ? AND ? GROUP BY lower(trim(query)))
SELECT `+cols+`, COALESCE(g.c, 0), COALESCE(g.i, 0), g.p
FROM keywords k LEFT JOIN g ON g.q = k.keyword
WHERE k.site_id = ? AND (? = '' OR k.kind = ?)
ORDER BY CASE k.kind WHEN 'target' THEN 0 WHEN 'scene' THEN 1 ELSE 2 END, COALESCE(g.i, 0) DESC, k.keyword`,
		siteID, from, to, siteID, kind, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Keyword{}
	for rows.Next() {
		var pos sql.NullFloat64
		var clicks, impressions float64
		k, err := scanKeyword(rows, &clicks, &impressions, &pos)
		if err != nil {
			return nil, err
		}
		k.Clicks, k.Impressions, k.Position = clicks, impressions, nullable(pos)
		out = append(out, k)
	}
	return out, rows.Err()
}
