// SPDX-License-Identifier: AGPL-3.0-or-later
package agent

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/sitemoment/sitemoment-desktop/internal/llm"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

// ViewOf is a session as the UI shows it.
func ViewOf(s store.AgentSession) SessionView {
	return SessionView{ID: s.ID, SiteID: s.SiteID, Title: s.Title, SpendMode: s.SpendMode, Status: s.Status, Credits: s.Credits, UpdatedAt: s.UpdatedAt}
}

func toolViewOf(c store.AgentToolCall) ToolView {
	v := ToolView{CallID: c.CallID, Tool: c.Tool, Label: c.Label, Kind: c.Kind, Status: c.Status, Cached: c.Cached, Credits: c.Credits, Error: c.Error}
	if c.FinishedAt > 0 {
		v.Ms = c.FinishedAt - c.StartedAt
	}
	if c.Card != "" {
		var card Card
		if json.Unmarshal([]byte(c.Card), &card) == nil {
			v.Card = &card
		}
	}
	return v
}

// Transcript rebuilds a session for the UI from its messages and tool calls.
func (a *Agent) Transcript(ctx context.Context, id int64) (Transcript, error) {
	sess, err := a.Store.AgentSession(ctx, id)
	if err != nil {
		return Transcript{}, err
	}
	msgs, err := a.Store.AgentMessages(ctx, id)
	if err != nil {
		return Transcript{}, err
	}
	calls, err := a.Store.AgentToolCalls(ctx, id)
	if err != nil {
		return Transcript{}, err
	}
	type key struct {
		turn int
		id   string
	}
	byCall := map[key]store.AgentToolCall{}
	for _, c := range calls {
		byCall[key{c.Turn, c.CallID}] = c
	}
	tr := Transcript{Session: ViewOf(sess), Items: []Item{}}
	for _, m := range msgs {
		switch {
		case m.Role == "user":
			tr.Items = append(tr.Items, Item{Kind: "user", Turn: m.Turn, Text: m.Content, At: m.CreatedAt})
		case m.Role == "assistant" && m.ToolCalls != "":
			if s := strings.TrimSpace(m.Content); s != "" {
				tr.Items = append(tr.Items, Item{Kind: "note", Turn: m.Turn, Text: s, At: m.CreatedAt})
			}
			var tcs []llm.ToolCall
			_ = json.Unmarshal([]byte(m.ToolCalls), &tcs)
			for _, c := range tcs {
				if row, ok := byCall[key{m.Turn, c.ID}]; ok {
					v := toolViewOf(row)
					tr.Items = append(tr.Items, Item{Kind: "tool", Turn: m.Turn, Tool: &v, At: row.StartedAt / 1000})
				}
			}
		case m.Role == "assistant" && m.Content == stopNote:
			tr.Items = append(tr.Items, Item{Kind: "stopped", Turn: m.Turn, Text: m.Content, At: m.CreatedAt})
		case m.Role == "assistant":
			tr.Items = append(tr.Items, Item{Kind: "answer", Turn: m.Turn, Text: m.Content, At: m.CreatedAt})
		}
	}
	if sess.Status == store.AgentAwaitingConfirm {
		if p := pendingOf(sess.Pending); p != nil {
			tr.Pending = p.view()
		}
	}
	return tr, nil
}
