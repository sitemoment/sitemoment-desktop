// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"database/sql"
	"errors"
)

// WebCache returns the cached value under key unless it expired at now.
func (s *Store) WebCache(ctx context.Context, key string, now int64) (value string, fetchedAt int64, ok bool, err error) {
	err = s.db.QueryRowContext(ctx, "SELECT value, fetched_at FROM web_cache WHERE key = ? AND expires_at > ?", key, now).Scan(&value, &fetchedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, false, nil
	}
	return value, fetchedAt, err == nil, err
}

// SetWebCache stores value under key until expiresAt.
func (s *Store) SetWebCache(ctx context.Context, key, value string, fetchedAt, expiresAt int64) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO web_cache (key, value, fetched_at, expires_at) VALUES (?, ?, ?, ?) "+
		"ON CONFLICT(key) DO UPDATE SET value = excluded.value, fetched_at = excluded.fetched_at, expires_at = excluded.expires_at", key, value, fetchedAt, expiresAt)
	return err
}
