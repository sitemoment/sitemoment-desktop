// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// BingDay is one day of Bing clicks and impressions.
type BingDay struct {
	Day                 string
	Clicks, Impressions float64
}

// BingStat is one query's (or page's) numbers for the week starting Week.
type BingStat struct {
	Week, Key                     string
	Clicks, Impressions, Position float64
}

// BingTotals sums Bing's daily traffic over a period.
type BingTotals struct {
	Clicks      float64 `json:"clicks"`
	Impressions float64 `json:"impressions"`
}

// bingStatTables maps a stat kind to its table and key column.
var bingStatTables = map[string][2]string{
	"query": {"bing_queries", "query"},
	"page":  {"bing_pages", "page"},
}

// ReplaceBingDaily writes the days Bing returned (older days Bing no longer
// returns are kept), records them in the ledger (slice "traffic") and marks
// a successful fetch (slice "fetch", day = the fetch day in UTC).
func (s *Store) ReplaceBingDaily(ctx context.Context, siteID int64, rows []BingDay, fetchedAt int64) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		for _, r := range rows {
			if _, err := tx.ExecContext(ctx, "INSERT INTO bing_daily (site_id, day, clicks, impressions) VALUES (?, ?, ?, ?) "+
				"ON CONFLICT(site_id, day) DO UPDATE SET clicks = excluded.clicks, impressions = excluded.impressions",
				siteID, r.Day, r.Clicks, r.Impressions); err != nil {
				return err
			}
			if err := recordDays(ctx, tx, siteID, "bing", "traffic", r.Day, r.Day, map[string]int{r.Day: 1}, fetchedAt); err != nil {
				return err
			}
		}
		today := time.Unix(fetchedAt, 0).UTC().Format(DayLayout)
		return recordDays(ctx, tx, siteID, "bing", "fetch", today, today, map[string]int{today: len(rows)}, fetchedAt)
	})
}

// ReplaceBingStats writes the weeks Bing returned for kind ("query" or
// "page"), replacing those weeks' rows and keeping older weeks.
func (s *Store) ReplaceBingStats(ctx context.Context, siteID int64, kind string, rows []BingStat, fetchedAt int64) error {
	t, ok := bingStatTables[kind]
	if !ok {
		return errors.New("store: unknown Bing stat kind " + kind)
	}
	perWeek := map[string]int{}
	for _, r := range rows {
		perWeek[r.Week]++
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		for week := range perWeek {
			if _, err := tx.ExecContext(ctx, "DELETE FROM "+t[0]+" WHERE site_id = ? AND week = ?", siteID, week); err != nil {
				return err
			}
		}
		ins, err := tx.PrepareContext(ctx, "INSERT INTO "+t[0]+" (site_id, week, "+t[1]+", clicks, impressions, position) VALUES (?, ?, ?, ?, ?, ?) "+
			"ON CONFLICT(site_id, week, "+t[1]+") DO UPDATE SET clicks = excluded.clicks, impressions = excluded.impressions, position = excluded.position")
		if err != nil {
			return err
		}
		defer ins.Close()
		for _, r := range rows {
			if _, err := ins.ExecContext(ctx, siteID, r.Week, r.Key, r.Clicks, r.Impressions, r.Position); err != nil {
				return err
			}
		}
		for week, n := range perWeek {
			if err := recordDays(ctx, tx, siteID, "bing", kind, week, week, map[string]int{week: n}, fetchedAt); err != nil {
				return err
			}
		}
		return nil
	})
}

// LastFetched is the newest fetched_at of a source's slice, or 0.
func (s *Store) LastFetched(ctx context.Context, siteID int64, source, slice string) (int64, error) {
	var at sql.NullInt64
	err := s.db.QueryRowContext(ctx, "SELECT MAX(fetched_at) FROM sync_days WHERE site_id = ? AND source = ? AND slice = ?", siteID, source, slice).Scan(&at)
	return at.Int64, err
}

// BingTotals sums daily clicks and impressions over [from, to].
func (s *Store) BingTotals(ctx context.Context, siteID int64, from, to string) (BingTotals, error) {
	var t BingTotals
	err := s.db.QueryRowContext(ctx, "SELECT COALESCE(SUM(clicks), 0), COALESCE(SUM(impressions), 0) FROM bing_daily WHERE site_id = ? AND day BETWEEN ? AND ?",
		siteID, from, to).Scan(&t.Clicks, &t.Impressions)
	return t, err
}

// BingDaily is clicks per day over [from, to] (days without a row are left out).
func (s *Store) BingDaily(ctx context.Context, siteID int64, from, to string) ([]DayValue, error) {
	return s.dayValues(ctx, "SELECT day, clicks FROM bing_daily WHERE site_id = ? AND day BETWEEN ? AND ? ORDER BY day", siteID, from, to)
}
