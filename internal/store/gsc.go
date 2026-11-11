// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"database/sql"
	"time"
)

// DayLayout is how days are stored.
const DayLayout = "2006-01-02"

// GSCRow is one Search Console row for a slice ("date", "query" or "page").
type GSCRow struct {
	Day         string
	Query       string
	Page        string
	Clicks      float64
	Impressions float64
	Position    float64
}

// ReplaceGSC replaces a slice's rows for days [from, through] and records
// every one of those days as fetched at fetchedAt, even days without rows.
func (s *Store) ReplaceGSC(ctx context.Context, siteID int64, slice, from, through string, rows []GSCRow, fetchedAt int64) error {
	perDay := map[string]int{}
	for _, r := range rows {
		perDay[r.Day]++
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM gsc_facts WHERE site_id = ? AND slice = ? AND day BETWEEN ? AND ?", siteID, slice, from, through); err != nil {
			return err
		}
		ins, err := tx.PrepareContext(ctx, "INSERT INTO gsc_facts (site_id, slice, day, query, page, clicks, impressions, position) VALUES (?, ?, ?, ?, ?, ?, ?, ?) "+
			"ON CONFLICT(site_id, slice, day, query, page) DO UPDATE SET clicks = excluded.clicks, impressions = excluded.impressions, position = excluded.position")
		if err != nil {
			return err
		}
		defer ins.Close()
		for _, r := range rows {
			if _, err := ins.ExecContext(ctx, siteID, slice, r.Day, r.Query, r.Page, r.Clicks, r.Impressions, r.Position); err != nil {
				return err
			}
		}
		return recordDays(ctx, tx, siteID, "gsc", slice, from, through, perDay, fetchedAt)
	})
}

// recordDays marks every day in [from, through] of a source's slice as
// fetched at fetchedAt with perDay[day] rows (0 for days without rows).
func recordDays(ctx context.Context, tx *sql.Tx, siteID int64, source, slice, from, through string, perDay map[string]int, fetchedAt int64) error {
	start, err := time.Parse(DayLayout, from)
	if err != nil {
		return err
	}
	end, err := time.Parse(DayLayout, through)
	if err != nil {
		return err
	}
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		day := d.Format(DayLayout)
		if _, err := tx.ExecContext(ctx, "INSERT INTO sync_days (site_id, source, slice, day, rows, fetched_at) VALUES (?, ?, ?, ?, ?, ?) "+
			"ON CONFLICT(site_id, source, slice, day) DO UPDATE SET rows = excluded.rows, fetched_at = excluded.fetched_at",
			siteID, source, slice, day, perDay[day], fetchedAt); err != nil {
			return err
		}
	}
	return nil
}

// SyncDays maps each fetched day of a slice to when it was fetched.
func (s *Store) SyncDays(ctx context.Context, siteID int64, source, slice string) (map[string]int64, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT day, fetched_at FROM sync_days WHERE site_id = ? AND source = ? AND slice = ?", siteID, source, slice)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var day string
		var at int64
		if err := rows.Scan(&day, &at); err != nil {
			return nil, err
		}
		out[day] = at
	}
	return out, rows.Err()
}

// DayImpressions sums the "date" slice's impressions per day over [from,
// through]. Days with no row (never fetched, or fetched with zero rows) are
// absent from the result.
func (s *Store) DayImpressions(ctx context.Context, siteID int64, from, through string) (map[string]float64, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT day, SUM(impressions) FROM gsc_facts WHERE site_id = ? AND slice = 'date' AND day BETWEEN ? AND ? GROUP BY day", siteID, from, through)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var day string
		var imp float64
		if err := rows.Scan(&day, &imp); err != nil {
			return nil, err
		}
		out[day] = imp
	}
	return out, rows.Err()
}

// LatestDay is the newest fetched day of a slice, or "".
func (s *Store) LatestDay(ctx context.Context, siteID int64, source, slice string) (string, error) {
	var day sql.NullString
	err := s.db.QueryRowContext(ctx, "SELECT MAX(day) FROM sync_days WHERE site_id = ? AND source = ? AND slice = ?", siteID, source, slice).Scan(&day)
	return day.String, err
}
