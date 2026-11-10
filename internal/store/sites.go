// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strings"
)

// Site is one website the user tracks.
type Site struct {
	ID            int64  `json:"id"`
	Host          string `json:"host"`
	Name          string `json:"name"`
	GoogleAccount string `json:"googleAccount"`
	GSCProperty   string `json:"gscProperty"`
	CreatedAt     int64  `json:"createdAt"`
}

// Connection is an authorized account (kind "google" for now). The token
// itself lives in the OS keychain, never here.
type Connection struct {
	Kind      string `json:"kind"`
	Account   string `json:"account"`
	CreatedAt int64  `json:"createdAt"`
}

// ErrBadHost means the input is not a public host name.
var ErrBadHost = errors.New("store: not a valid site host")

// NormalizeHost turns "https://Example.com/x" or "example.com" into
// "example.com".
func NormalizeHost(raw string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" || strings.ContainsAny(s, " \t") {
		return "", ErrBadHost
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", ErrBadHost
	}
	h := u.Hostname()
	if !strings.Contains(h, ".") || strings.ContainsAny(h, "_") || strings.HasPrefix(h, ".") || strings.HasSuffix(h, ".") {
		return "", ErrBadHost
	}
	return h, nil
}

const siteCols = "id, host, name, google_account, gsc_property, created_at"

func scanSite(row interface{ Scan(...any) error }) (Site, error) {
	var s Site
	err := row.Scan(&s.ID, &s.Host, &s.Name, &s.GoogleAccount, &s.GSCProperty, &s.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return s, ErrNotFound
	}
	return s, err
}

// AddSite adds the site (or returns the existing one with the same host).
func (s *Store) AddSite(ctx context.Context, raw string, now int64) (Site, error) {
	host, err := NormalizeHost(raw)
	if err != nil {
		return Site{}, err
	}
	if _, err := s.db.ExecContext(ctx,
		"INSERT INTO sites (host, created_at) VALUES (?, ?) ON CONFLICT(host) DO NOTHING", host, now); err != nil {
		return Site{}, err
	}
	return scanSite(s.db.QueryRowContext(ctx, "SELECT "+siteCols+" FROM sites WHERE host = ?", host))
}

// Sites lists all sites by host.
func (s *Store) Sites(ctx context.Context) ([]Site, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+siteCols+" FROM sites ORDER BY host")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Site{}
	for rows.Next() {
		site, err := scanSite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, site)
	}
	return out, rows.Err()
}

// Site returns one site or ErrNotFound.
func (s *Store) Site(ctx context.Context, id int64) (Site, error) {
	return scanSite(s.db.QueryRowContext(ctx, "SELECT "+siteCols+" FROM sites WHERE id = ?", id))
}

// DeleteSite removes a site and (by cascade) its data.
func (s *Store) DeleteSite(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sites WHERE id = ?", id)
	return err
}

// BindGSC sets which Google account and Search Console property feed the site.
func (s *Store) BindGSC(ctx context.Context, id int64, account, property string) error {
	res, err := s.db.ExecContext(ctx, "UPDATE sites SET google_account = ?, gsc_property = ? WHERE id = ?", account, property, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpsertConnection records an authorized account.
func (s *Store) UpsertConnection(ctx context.Context, kind, account string, now int64) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO connections (kind, account, created_at) VALUES (?, ?, ?) ON CONFLICT(kind, account) DO NOTHING", kind, account, now)
	return err
}

// Connections lists the accounts of one kind by account name.
func (s *Store) Connections(ctx context.Context, kind string) ([]Connection, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT kind, account, created_at FROM connections WHERE kind = ? ORDER BY account", kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Connection{}
	for rows.Next() {
		var c Connection
		if err := rows.Scan(&c.Kind, &c.Account, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeleteConnection forgets an account.
func (s *Store) DeleteConnection(ctx context.Context, kind, account string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM connections WHERE kind = ? AND account = ?", kind, account)
	return err
}
