// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"database/sql"
	"errors"
)

// Agent session states.
const (
	AgentIdle            = "idle"
	AgentRunning         = "running"
	AgentAwaitingConfirm = "awaiting_confirm"
)

// ErrAgentBusy means the session is already running a turn, or (for a
// resume) is not waiting for a confirmation.
var ErrAgentBusy = errors.New("store: the agent session is busy")

// AgentSession is one conversation with the agent. Pending is the JSON of
// paid calls waiting for the user's approval (status awaiting_confirm).
type AgentSession struct {
	ID        int64
	SiteID    int64 // 0: no site
	Title     string
	SpendMode string
	Turn      int
	Status    string
	Pending   string
	Credits   float64
	CreatedAt int64
	UpdatedAt int64
}

// AgentMessage is one stored chat message. ToolCalls is the JSON of the
// assistant's tool calls; Native/NativeOf keep a provider's own encoding of
// an assistant turn (see llm.Message).
type AgentMessage struct {
	ID         int64
	SessionID  int64
	Turn       int
	Role       string
	Content    string
	ToolCalls  string
	ToolCallID string
	Native     string
	NativeOf   string
	CreatedAt  int64
}

// AgentToolCall is one tool call the agent made, for the UI. StartedAt and
// FinishedAt are Unix milliseconds.
type AgentToolCall struct {
	ID         int64
	SessionID  int64
	Turn       int
	CallID     string
	Tool       string
	Label      string
	Kind       string
	Args       string
	Status     string
	Cached     bool
	Credits    float64
	Card       string
	Error      string
	StartedAt  int64
	FinishedAt int64
}

const agentSessionCols = "id, COALESCE(site_id, 0), title, spend_mode, turn, status, pending, credits, created_at, updated_at"

func scanAgentSession(row interface{ Scan(...any) error }) (AgentSession, error) {
	var a AgentSession
	err := row.Scan(&a.ID, &a.SiteID, &a.Title, &a.SpendMode, &a.Turn, &a.Status, &a.Pending, &a.Credits, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

func nullID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

// CreateAgentSession starts an idle session for siteID (0: none).
func (s *Store) CreateAgentSession(ctx context.Context, siteID int64, mode string, now int64) (AgentSession, error) {
	res, err := s.db.ExecContext(ctx, "INSERT INTO agent_sessions (site_id, spend_mode, created_at, updated_at) VALUES (?, ?, ?, ?)",
		nullID(siteID), mode, now, now)
	if err != nil {
		return AgentSession{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return AgentSession{}, err
	}
	return s.AgentSession(ctx, id)
}

// AgentSession returns one session or ErrNotFound.
func (s *Store) AgentSession(ctx context.Context, id int64) (AgentSession, error) {
	return scanAgentSession(s.db.QueryRowContext(ctx, "SELECT "+agentSessionCols+" FROM agent_sessions WHERE id = ?", id))
}

// AgentSessions lists a site's sessions (0: those without a site), newest first.
func (s *Store) AgentSessions(ctx context.Context, siteID int64) ([]AgentSession, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+agentSessionCols+" FROM agent_sessions WHERE COALESCE(site_id, 0) = ? ORDER BY updated_at DESC, id DESC", siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AgentSession{}
	for rows.Next() {
		a, err := scanAgentSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// DeleteAgentSession removes a session with its messages and tool calls.
// The guard against deleting a running session (ErrAgentBusy) is enforced
// here, atomically with the delete, so a caller's own check-then-delete
// can't race a concurrent Start/Resume that claims the turn in between.
func (s *Store) DeleteAgentSession(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM agent_sessions WHERE id = ? AND status != ?", id, AgentRunning)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	if _, err := s.AgentSession(ctx, id); err != nil {
		return err // ErrNotFound: already gone
	}
	return ErrAgentBusy
}

// TitleAgentSession sets the title once (a session keeps its first title).
func (s *Store) TitleAgentSession(ctx context.Context, id int64, title string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE agent_sessions SET title = ? WHERE id = ? AND title = ''", title, id)
	return err
}

// SetAgentSpendMode sets a session's spend mode.
func (s *Store) SetAgentSpendMode(ctx context.Context, id int64, mode string, now int64) error {
	res, err := s.db.ExecContext(ctx, "UPDATE agent_sessions SET spend_mode = ?, updated_at = ? WHERE id = ?", mode, now, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ClaimAgentTurn marks a session running and returns it. A new turn
// (resume false) may start from idle or awaiting_confirm and bumps the turn
// number; a resume needs awaiting_confirm and keeps the turn. Otherwise it
// returns ErrAgentBusy (or ErrNotFound).
func (s *Store) ClaimAgentTurn(ctx context.Context, id int64, resume bool, now int64) (AgentSession, error) {
	q := "UPDATE agent_sessions SET status = 'running', turn = turn + 1, updated_at = ? WHERE id = ? AND status IN ('idle', 'awaiting_confirm')"
	if resume {
		q = "UPDATE agent_sessions SET status = 'running', updated_at = ? WHERE id = ? AND status = 'awaiting_confirm'"
	}
	res, err := s.db.ExecContext(ctx, q, now, id)
	if err != nil {
		return AgentSession{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if _, err := s.AgentSession(ctx, id); err != nil {
			return AgentSession{}, err
		}
		return AgentSession{}, ErrAgentBusy
	}
	return s.AgentSession(ctx, id)
}

// ReleaseAgentTurn ends a running turn with status (idle or
// awaiting_confirm) and the pending calls JSON ("" for none).
func (s *Store) ReleaseAgentTurn(ctx context.Context, id int64, status, pending string, now int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE agent_sessions SET status = ?, pending = ?, updated_at = ? WHERE id = ?", status, pending, now, id)
	return err
}

// AddAgentCredits adds SiteMoment credits a session spent.
func (s *Store) AddAgentCredits(ctx context.Context, id int64, credits float64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE agent_sessions SET credits = credits + ? WHERE id = ?", credits, id)
	return err
}

// ResetRunningAgentSessions marks sessions left running (the app quit
// mid-turn) idle and returns how many there were.
func (s *Store) ResetRunningAgentSessions(ctx context.Context) (int, error) {
	res, err := s.db.ExecContext(ctx, "UPDATE agent_sessions SET status = 'idle' WHERE status = 'running'")
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// AddAgentMessage stores a message and returns its id.
func (s *Store) AddAgentMessage(ctx context.Context, m AgentMessage) (int64, error) {
	res, err := s.db.ExecContext(ctx, "INSERT INTO agent_messages (session_id, turn, role, content, tool_calls, tool_call_id, native, native_of, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		m.SessionID, m.Turn, m.Role, m.Content, m.ToolCalls, m.ToolCallID, m.Native, m.NativeOf, m.CreatedAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// AgentMessages lists a session's messages in order.
func (s *Store) AgentMessages(ctx context.Context, sessionID int64) ([]AgentMessage, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, session_id, turn, role, content, tool_calls, tool_call_id, native, native_of, created_at FROM agent_messages WHERE session_id = ? ORDER BY id", sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AgentMessage{}
	for rows.Next() {
		var m AgentMessage
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Turn, &m.Role, &m.Content, &m.ToolCalls, &m.ToolCallID, &m.Native, &m.NativeOf, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// InsertAgentToolCall records a tool call that is starting and returns its id.
func (s *Store) InsertAgentToolCall(ctx context.Context, c AgentToolCall) (int64, error) {
	res, err := s.db.ExecContext(ctx, "INSERT INTO agent_tool_calls (session_id, turn, call_id, tool, label, kind, args, status, started_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		c.SessionID, c.Turn, c.CallID, c.Tool, c.Label, c.Kind, c.Args, c.Status, c.StartedAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// FinishAgentToolCall stores a tool call's outcome.
func (s *Store) FinishAgentToolCall(ctx context.Context, c AgentToolCall) error {
	_, err := s.db.ExecContext(ctx, "UPDATE agent_tool_calls SET status = ?, label = ?, cached = ?, credits = ?, card = ?, error = ?, finished_at = ? WHERE id = ?",
		c.Status, c.Label, c.Cached, c.Credits, c.Card, c.Error, c.FinishedAt, c.ID)
	return err
}

// AgentToolCalls lists a session's tool calls in order.
func (s *Store) AgentToolCalls(ctx context.Context, sessionID int64) ([]AgentToolCall, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, session_id, turn, call_id, tool, label, kind, args, status, cached, credits, card, error, started_at, finished_at FROM agent_tool_calls WHERE session_id = ? ORDER BY id", sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AgentToolCall{}
	for rows.Next() {
		var c AgentToolCall
		if err := rows.Scan(&c.ID, &c.SessionID, &c.Turn, &c.CallID, &c.Tool, &c.Label, &c.Kind, &c.Args, &c.Status, &c.Cached, &c.Credits, &c.Card, &c.Error, &c.StartedAt, &c.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
