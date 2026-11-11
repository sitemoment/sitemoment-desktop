// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"database/sql"
)

// GARow is one GA4 row of a slice: "date" (Dim ""), "landing" (Dim is the
// landing page path) or "channel" (Dim is the default channel group).
type GARow struct {
	Day, Dim                                    string
	Sessions, Users, EngagedSessions, KeyEvents float64
}

// GATotals sums the "date" slice. Users are added up per day, so they are
// not unique users over the period.
type GATotals struct {
	Sessions        float64 `json:"sessions"`
	Users           float64 `json:"users"`
	EngagedSessions float64 `json:"engagedSessions"`
	KeyEvents       float64 `json:"keyEvents"`
}

// DayValue is one day's value of one metric.
type DayValue struct {
	Day   string  `json:"day"`
	Value float64 `json:"value"`
}

// ReplaceGA replaces a slice's rows for days [from, through] and records
// every one of those days as fetched (source "ga"), even days without rows.
func (s *Store) ReplaceGA(ctx context.Context, siteID int64, slice, from, through string, rows []GARow, fetchedAt int64) error {
	perDay := map[string]int{}
	for _, r := range rows {
		perDay[r.Day]++
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM ga_facts WHERE site_id = ? AND slice = ? AND day BETWEEN ? AND ?", siteID, slice, from, through); err != nil {
			return err
		}
		ins, err := tx.PrepareContext(ctx, "INSERT INTO ga_facts (site_id, slice, day, dim, sessions, users, engaged_sessions, key_events) VALUES (?, ?, ?, ?, ?, ?, ?, ?) "+
			"ON CONFLICT(site_id, slice, day, dim) DO UPDATE SET sessions = excluded.sessions, users = excluded.users, "+
			"engaged_sessions = excluded.engaged_sessions, key_events = excluded.key_events")
		if err != nil {
			return err
		}
		defer ins.Close()
		for _, r := range rows {
			if _, err := ins.ExecContext(ctx, siteID, slice, r.Day, r.Dim, r.Sessions, r.Users, r.EngagedSessions, r.KeyEvents); err != nil {
				return err
			}
		}
		return recordDays(ctx, tx, siteID, "ga", slice, from, through, perDay, fetchedAt)
	})
}

// GATotals sums the "date" slice over [from, to].
func (s *Store) GATotals(ctx context.Context, siteID int64, from, to string) (GATotals, error) {
	var t GATotals
	err := s.db.QueryRowContext(ctx,
		"SELECT COALESCE(SUM(sessions), 0), COALESCE(SUM(users), 0), COALESCE(SUM(engaged_sessions), 0), COALESCE(SUM(key_events), 0) "+
			"FROM ga_facts WHERE site_id = ? AND slice = 'date' AND day BETWEEN ? AND ?", siteID, from, to).
		Scan(&t.Sessions, &t.Users, &t.EngagedSessions, &t.KeyEvents)
	return t, err
}

// GADaily is sessions per day of the "date" slice over [from, to] (days
// without a row are left out).
func (s *Store) GADaily(ctx context.Context, siteID int64, from, to string) ([]DayValue, error) {
	return s.dayValues(ctx, "SELECT day, sessions FROM ga_facts WHERE site_id = ? AND slice = 'date' AND day BETWEEN ? AND ? ORDER BY day", siteID, from, to)
}

func (s *Store) dayValues(ctx context.Context, query string, args ...any) ([]DayValue, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DayValue{}
	for rows.Next() {
		var v DayValue
		if err := rows.Scan(&v.Day, &v.Value); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
