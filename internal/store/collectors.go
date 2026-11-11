// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// Collector run statuses.
const (
	RunRunning   = "running"
	RunNeedHuman = "need_human"
	RunOK        = "ok"
	RunError     = "error"
	RunCancelled = "cancelled"
)

// CollectorRun is one execution of a collector on one input. Result is the
// collector's JSON output (empty unless Status is "ok").
type CollectorRun struct {
	ID         int64  `json:"id"`
	Collector  string `json:"collector"`
	InputHash  string `json:"-"`
	Input      string `json:"input"`
	Status     string `json:"status"`
	Result     string `json:"-"`
	Error      string `json:"error"`
	CreatedAt  int64  `json:"createdAt"`
	FinishedAt int64  `json:"finishedAt"`
	ExpiresAt  int64  `json:"-"`
}

const collectorRunCols = "id, collector, input_hash, input, status, result, error, created_at, finished_at, expires_at"

func scanCollectorRun(row interface{ Scan(...any) error }) (CollectorRun, error) {
	var r CollectorRun
	err := row.Scan(&r.ID, &r.Collector, &r.InputHash, &r.Input, &r.Status, &r.Result, &r.Error, &r.CreatedAt, &r.FinishedAt, &r.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

// InsertCollectorRun records a run that is starting now and returns its id.
func (s *Store) InsertCollectorRun(ctx context.Context, collector, inputHash, input string, now int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, "INSERT INTO collector_runs (collector, input_hash, input, status, created_at) VALUES (?, ?, ?, ?, ?)",
		collector, inputHash, input, RunRunning, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// SetCollectorRunStatus changes an unfinished run's status (running or need_human).
func (s *Store) SetCollectorRunStatus(ctx context.Context, id int64, status string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE collector_runs SET status = ? WHERE id = ?", status, id)
	return err
}

// FinishCollectorRun ends a run; an "ok" run is a cache entry until expiresAt.
func (s *Store) FinishCollectorRun(ctx context.Context, id int64, status, result, errMsg string, finishedAt, expiresAt int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE collector_runs SET status = ?, result = ?, error = ?, finished_at = ?, expires_at = ? WHERE id = ?",
		status, result, errMsg, finishedAt, expiresAt, id)
	return err
}

// CachedCollectorRun returns the newest successful run of collector on the
// same input that has not expired at now, or ErrNotFound.
func (s *Store) CachedCollectorRun(ctx context.Context, collector, inputHash string, now int64) (CollectorRun, error) {
	return scanCollectorRun(s.db.QueryRowContext(ctx, "SELECT "+collectorRunCols+" FROM collector_runs "+
		"WHERE collector = ? AND input_hash = ? AND status = 'ok' AND expires_at > ? ORDER BY id DESC LIMIT 1", collector, inputHash, now))
}

// CollectorRunsSince counts the collector's real runs (cache hits are not
// runs) started at or after since.
func (s *Store) CollectorRunsSince(ctx context.Context, collector string, since int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM collector_runs WHERE collector = ? AND created_at >= ?", collector, since).Scan(&n)
	return n, err
}

// CollectorRun returns one run or ErrNotFound.
func (s *Store) CollectorRun(ctx context.Context, id int64) (CollectorRun, error) {
	return scanCollectorRun(s.db.QueryRowContext(ctx, "SELECT "+collectorRunCols+" FROM collector_runs WHERE id = ?", id))
}

// CollectorRuns lists runs newest first, all or only those with status.
func (s *Store) CollectorRuns(ctx context.Context, status string, limit int) ([]CollectorRun, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+collectorRunCols+" FROM collector_runs WHERE (? = '' OR status = ?) ORDER BY id DESC LIMIT ?",
		status, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CollectorRun{}
	for rows.Next() {
		r, err := scanCollectorRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CancelUnfinishedCollectorRuns ends runs left running by a previous app
// session (the app quit or crashed mid-run).
func (s *Store) CancelUnfinishedCollectorRuns(ctx context.Context, now int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE collector_runs SET status = 'cancelled', error = '应用退出，采集中断', finished_at = ? "+
		"WHERE status IN ('running', 'need_human')", now)
	return err
}

// PruneCollectorRuns deletes finished runs started before before, so the
// table (also used as the cache and the daily-count source) doesn't grow
// forever. Meant to be called once at startup.
func (s *Store) PruneCollectorRuns(ctx context.Context, before int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM collector_runs WHERE created_at < ? AND status NOT IN ('running', 'need_human')", before)
	return err
}

// CollectorGrant is the user's permission for a third-party collector to
// read the listed hosts, given for one version and one sha256 of its
// package (manifest.json + schema.json + collector.js): a code change
// without a version bump still revokes the grant.
type CollectorGrant struct {
	Collector string   `json:"collector"`
	Version   string   `json:"version"`
	Hosts     []string `json:"hosts"`
	SHA       string   `json:"sha"`
	GrantedAt int64    `json:"grantedAt"`
}

// CollectorGrant returns the grant for a collector or ErrNotFound.
func (s *Store) CollectorGrant(ctx context.Context, collector string) (CollectorGrant, error) {
	g := CollectorGrant{Collector: collector}
	var hosts string
	err := s.db.QueryRowContext(ctx, "SELECT version, hosts, sha, granted_at FROM collector_grants WHERE collector = ?", collector).
		Scan(&g.Version, &hosts, &g.SHA, &g.GrantedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return g, ErrNotFound
	}
	if err != nil {
		return g, err
	}
	return g, json.Unmarshal([]byte(hosts), &g.Hosts)
}

// GrantCollector records (or replaces) the user's permission.
func (s *Store) GrantCollector(ctx context.Context, collector, version string, hosts []string, sha string, now int64) error {
	raw, err := json.Marshal(hosts)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO collector_grants (collector, version, hosts, sha, granted_at) VALUES (?, ?, ?, ?, ?) "+
		"ON CONFLICT(collector) DO UPDATE SET version = excluded.version, hosts = excluded.hosts, sha = excluded.sha, granted_at = excluded.granted_at",
		collector, version, string(raw), sha, now)
	return err
}
