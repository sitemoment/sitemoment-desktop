// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"database/sql"
	"errors"
)

// Report is a generated site report (Markdown and self-contained HTML).
type Report struct {
	ID     int64  `json:"id"`
	SiteID int64  `json:"siteId"`
	Origin string `json:"origin"` // weekly | manual
	From   string `json:"from"`
	To     string `json:"to"`
	Title  string `json:"title"`
	// Markdown and HTML are left empty by Reports (the list).
	Markdown  string `json:"markdown"`
	HTML      string `json:"html"`
	CreatedAt int64  `json:"createdAt"`
}

// keepReports is how many reports a site keeps (a year of weekly ones).
const keepReports = 60

// AddReport stores a report (keeping the site's newest keepReports) and returns its id.
func (s *Store) AddReport(ctx context.Context, r Report) (int64, error) {
	var id int64
	err := s.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "INSERT INTO reports (site_id, origin, period_from, period_to, title, markdown, html, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
			r.SiteID, r.Origin, r.From, r.To, r.Title, r.Markdown, r.HTML, r.CreatedAt)
		if err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM reports WHERE site_id = ? AND id NOT IN (SELECT id FROM reports WHERE site_id = ? ORDER BY id DESC LIMIT ?)", r.SiteID, r.SiteID, keepReports)
		return err
	})
	return id, err
}

// Reports lists a site's reports without their bodies, newest first.
func (s *Store) Reports(ctx context.Context, siteID int64) ([]Report, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, site_id, origin, period_from, period_to, title, created_at FROM reports WHERE site_id = ? ORDER BY id DESC", siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Report{}
	for rows.Next() {
		var r Report
		if err := rows.Scan(&r.ID, &r.SiteID, &r.Origin, &r.From, &r.To, &r.Title, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Report returns one report with its bodies, or ErrNotFound.
func (s *Store) Report(ctx context.Context, id int64) (Report, error) {
	var r Report
	err := s.db.QueryRowContext(ctx, "SELECT id, site_id, origin, period_from, period_to, title, markdown, html, created_at FROM reports WHERE id = ?", id).
		Scan(&r.ID, &r.SiteID, &r.Origin, &r.From, &r.To, &r.Title, &r.Markdown, &r.HTML, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

// LastReportAt is when the site's newest report of origin was made, or 0.
func (s *Store) LastReportAt(ctx context.Context, siteID int64, origin string) (int64, error) {
	var at sql.NullInt64
	err := s.db.QueryRowContext(ctx, "SELECT MAX(created_at) FROM reports WHERE site_id = ? AND origin = ?", siteID, origin).Scan(&at)
	return at.Int64, err
}

// DeleteReport removes a report.
func (s *Store) DeleteReport(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM reports WHERE id = ?", id)
	return err
}
