// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// Task is a piece of SEO work with machine-checkable acceptance conditions.
type Task struct {
	ID        int64  `json:"id"`
	SiteID    int64  `json:"siteId"`
	SessionID int64  `json:"sessionId"` // the agent session that proposed it (0: none)
	Title     string `json:"title"`
	Detail    string `json:"detail"`
	// Status is proposed (waiting for the user), open, passed, failed or dismissed.
	Status string `json:"status"`
	// Conditions and Results are JSON (tasks.Condition and tasks.Outcome lists).
	Conditions string `json:"conditions"`
	Results    string `json:"results"`
	Days       int    `json:"days"`
	CreatedAt  int64  `json:"createdAt"`
	StartedAt  int64  `json:"startedAt"` // when it became open
	DueAt      int64  `json:"dueAt"`
	CheckedAt  int64  `json:"checkedAt"`
	DoneAt     int64  `json:"doneAt"`
}

const taskCols = "id, site_id, COALESCE(session_id, 0), title, detail, status, conditions, results, days, created_at, started_at, due_at, checked_at, done_at"

func scanTask(row interface{ Scan(...any) error }) (Task, error) {
	var t Task
	err := row.Scan(&t.ID, &t.SiteID, &t.SessionID, &t.Title, &t.Detail, &t.Status, &t.Conditions, &t.Results, &t.Days,
		&t.CreatedAt, &t.StartedAt, &t.DueAt, &t.CheckedAt, &t.DoneAt)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

// AddTask stores a new task and returns it with its id.
func (s *Store) AddTask(ctx context.Context, t Task) (Task, error) {
	res, err := s.db.ExecContext(ctx, "INSERT INTO tasks (site_id, session_id, title, detail, status, conditions, results, days, created_at, started_at, due_at) "+
		"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)", t.SiteID, nullID(t.SessionID), t.Title, t.Detail, t.Status, t.Conditions, t.Results, t.Days, t.CreatedAt, t.StartedAt, t.DueAt)
	if err != nil {
		return Task{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Task{}, err
	}
	return s.Task(ctx, id)
}

// Task returns one task or ErrNotFound.
func (s *Store) Task(ctx context.Context, id int64) (Task, error) {
	return scanTask(s.db.QueryRowContext(ctx, "SELECT "+taskCols+" FROM tasks WHERE id = ?", id))
}

// Tasks lists a site's tasks (siteID 0: every site) with one of statuses
// (none: all), newest first.
func (s *Store) Tasks(ctx context.Context, siteID int64, statuses ...string) ([]Task, error) {
	q := "SELECT " + taskCols + " FROM tasks WHERE (? = 0 OR site_id = ?)"
	args := []any{siteID, siteID}
	if len(statuses) > 0 {
		q += " AND status IN (" + strings.TrimSuffix(strings.Repeat("?,", len(statuses)), ",") + ")"
		for _, st := range statuses {
			args = append(args, st)
		}
	}
	rows, err := s.db.QueryContext(ctx, q+" ORDER BY id DESC", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// UpdateTask saves a task's status, results and times. When from is not
// empty it only updates a task still in that status and reports false
// otherwise (a concurrent change won).
func (s *Store) UpdateTask(ctx context.Context, t Task, from string) (bool, error) {
	res, err := s.db.ExecContext(ctx, "UPDATE tasks SET status = ?, results = ?, started_at = ?, due_at = ?, checked_at = ?, done_at = ? WHERE id = ? AND (? = '' OR status = ?)",
		t.Status, t.Results, t.StartedAt, t.DueAt, t.CheckedAt, t.DoneAt, t.ID, from, from)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// DeleteTask removes a task.
func (s *Store) DeleteTask(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM tasks WHERE id = ?", id)
	return err
}

// Notice is an in-app notification.
type Notice struct {
	ID     int64 `json:"id"`
	SiteID int64 `json:"siteId"`
	// Kind is task_proposed, task_opened, task_passed, task_failed, health_issues, health_failed or report_ready.
	Kind  string `json:"kind"`
	Title string `json:"title"`
	Body  string `json:"body"`
	// Ref is what the notice opens: "task:<id>", "crawl:<id>" or "report:<id>".
	Ref       string `json:"ref"`
	Read      bool   `json:"read"`
	CreatedAt int64  `json:"createdAt"`
}

// keepNotices is how many notices are kept.
const keepNotices = 200

// AddNotice stores a notice (keeping the newest keepNotices) and returns it with its id.
func (s *Store) AddNotice(ctx context.Context, n Notice) (Notice, error) {
	err := s.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "INSERT INTO notices (site_id, kind, title, body, ref, created_at) VALUES (?, ?, ?, ?, ?, ?)",
			nullID(n.SiteID), n.Kind, n.Title, n.Body, n.Ref, n.CreatedAt)
		if err != nil {
			return err
		}
		if n.ID, err = res.LastInsertId(); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM notices WHERE id NOT IN (SELECT id FROM notices ORDER BY id DESC LIMIT ?)", keepNotices)
		return err
	})
	return n, err
}

// Notices lists the newest notices and how many are unread.
func (s *Store) Notices(ctx context.Context, limit int) ([]Notice, int, error) {
	var unread int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM notices WHERE read = 0").Scan(&unread); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT id, COALESCE(site_id, 0), kind, title, body, ref, read, created_at FROM notices ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Notice{}
	for rows.Next() {
		var n Notice
		if err := rows.Scan(&n.ID, &n.SiteID, &n.Kind, &n.Title, &n.Body, &n.Ref, &n.Read, &n.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, n)
	}
	return out, unread, rows.Err()
}

// MarkNoticesRead marks one notice read (id 0: all of them).
func (s *Store) MarkNoticesRead(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE notices SET read = 1 WHERE ? = 0 OR id = ?", id, id)
	return err
}

// GSCKeyStat sums one query or page (slice "query" or "page", exact key)
// over [from, to]; Position is impression-weighted.
func (s *Store) GSCKeyStat(ctx context.Context, siteID int64, slice, key, from, to string) (GSCStat, error) {
	col, ok := map[string]string{"query": "query", "page": "page"}[slice]
	if !ok {
		return GSCStat{}, ErrBadQuery
	}
	r := GSCStat{Key: key}
	err := s.db.QueryRowContext(ctx, "SELECT COALESCE(SUM(clicks), 0), COALESCE(SUM(impressions), 0), "+
		"COALESCE(SUM(position * impressions) / NULLIF(SUM(impressions), 0), 0) FROM gsc_facts "+
		"WHERE site_id = ? AND slice = ? AND "+col+" = ? AND day BETWEEN ? AND ?", siteID, slice, key, from, to).
		Scan(&r.Clicks, &r.Impressions, &r.Position)
	if r.Impressions > 0 {
		r.CTR = r.Clicks / r.Impressions
	}
	return r, err
}
