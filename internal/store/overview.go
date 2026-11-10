// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"database/sql"
)

// Totals are Search Console totals for a period. Position is the average
// position weighted by impressions.
type Totals struct {
	Clicks      float64 `json:"clicks"`
	Impressions float64 `json:"impressions"`
	CTR         float64 `json:"ctr"`
	Position    float64 `json:"position"`
}

// DayPoint is one day of the "date" slice.
type DayPoint struct {
	Day         string  `json:"day"`
	Clicks      float64 `json:"clicks"`
	Impressions float64 `json:"impressions"`
}

// QueryStat is a query's numbers for a period, with its position in the
// previous period when it had impressions then.
type QueryStat struct {
	Query        string   `json:"query"`
	Clicks       float64  `json:"clicks"`
	Impressions  float64  `json:"impressions"`
	Position     float64  `json:"position"`
	PrevPosition *float64 `json:"prevPosition"`
}

// GSCTotals sums the "date" slice over [from, to].
func (s *Store) GSCTotals(ctx context.Context, siteID int64, from, to string) (Totals, error) {
	var t Totals
	var weighted float64
	err := s.db.QueryRowContext(ctx,
		"SELECT COALESCE(SUM(clicks), 0), COALESCE(SUM(impressions), 0), COALESCE(SUM(position * impressions), 0) "+
			"FROM gsc_facts WHERE site_id = ? AND slice = 'date' AND day BETWEEN ? AND ?", siteID, from, to).
		Scan(&t.Clicks, &t.Impressions, &weighted)
	if t.Impressions > 0 {
		t.CTR = t.Clicks / t.Impressions
		t.Position = weighted / t.Impressions
	}
	return t, err
}

// GSCDaily returns the "date" slice over [from, to] by day.
func (s *Store) GSCDaily(ctx context.Context, siteID int64, from, to string) ([]DayPoint, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT day, clicks, impressions FROM gsc_facts WHERE site_id = ? AND slice = 'date' AND day BETWEEN ? AND ? ORDER BY day", siteID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DayPoint{}
	for rows.Next() {
		var p DayPoint
		if err := rows.Scan(&p.Day, &p.Clicks, &p.Impressions); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// TopQueries ranks queries by clicks (then impressions) over [from, to].
func (s *Store) TopQueries(ctx context.Context, siteID int64, from, to, prevFrom, prevTo string, limit int) ([]QueryStat, error) {
	rows, err := s.db.QueryContext(ctx, `
WITH cur AS (
  SELECT query, SUM(clicks) AS c, SUM(impressions) AS i, SUM(position * impressions) / NULLIF(SUM(impressions), 0) AS p
  FROM gsc_facts WHERE site_id = ? AND slice = 'query' AND day BETWEEN ? AND ? GROUP BY query),
prev AS (
  SELECT query, SUM(position * impressions) / NULLIF(SUM(impressions), 0) AS p
  FROM gsc_facts WHERE site_id = ? AND slice = 'query' AND day BETWEEN ? AND ? GROUP BY query)
SELECT cur.query, cur.c, cur.i, COALESCE(cur.p, 0), prev.p
FROM cur LEFT JOIN prev ON prev.query = cur.query
ORDER BY cur.c DESC, cur.i DESC, cur.query
LIMIT ?`, siteID, from, to, siteID, prevFrom, prevTo, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []QueryStat{}
	for rows.Next() {
		var q QueryStat
		var prev sql.NullFloat64
		if err := rows.Scan(&q.Query, &q.Clicks, &q.Impressions, &q.Position, &prev); err != nil {
			return nil, err
		}
		if prev.Valid {
			v := prev.Float64
			q.PrevPosition = &v
		}
		out = append(out, q)
	}
	return out, rows.Err()
}
