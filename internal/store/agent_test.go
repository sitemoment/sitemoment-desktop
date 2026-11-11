// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"errors"
	"testing"
)

func TestAgentSessionLifecycle(t *testing.T) {
	st := open(t)
	site, err := st.AddSite(ctx, "example.com", 1)
	ok(t, err)
	a, err := st.CreateAgentSession(ctx, site.ID, "confirm", 10)
	ok(t, err)
	if a.SiteID != site.ID || a.Status != AgentIdle || a.Turn != 0 || a.SpendMode != "confirm" {
		t.Fatalf("created %+v", a)
	}
	noSite, err := st.CreateAgentSession(ctx, 0, "auto", 11)
	ok(t, err)
	if noSite.SiteID != 0 {
		t.Fatalf("no-site session %+v", noSite)
	}

	a, err = st.ClaimAgentTurn(ctx, a.ID, false, 20)
	ok(t, err)
	if a.Status != AgentRunning || a.Turn != 1 {
		t.Fatalf("claimed %+v", a)
	}
	if _, err := st.ClaimAgentTurn(ctx, a.ID, false, 21); !errors.Is(err, ErrAgentBusy) {
		t.Fatalf("second claim: %v", err)
	}
	if _, err := st.ClaimAgentTurn(ctx, a.ID, true, 21); !errors.Is(err, ErrAgentBusy) {
		t.Fatalf("resume while running: %v", err)
	}
	ok(t, st.ReleaseAgentTurn(ctx, a.ID, AgentAwaitingConfirm, `{"step":1}`, 22))
	a, err = st.ClaimAgentTurn(ctx, a.ID, true, 23)
	ok(t, err)
	if a.Turn != 1 || a.Pending != `{"step":1}` {
		t.Fatalf("resumed %+v", a)
	}
	ok(t, st.ReleaseAgentTurn(ctx, a.ID, AgentIdle, "", 24))
	if _, err := st.ClaimAgentTurn(ctx, a.ID, true, 25); !errors.Is(err, ErrAgentBusy) {
		t.Fatalf("resume while idle: %v", err)
	}
	if _, err := st.ClaimAgentTurn(ctx, 999, false, 25); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}

	ok(t, st.TitleAgentSession(ctx, a.ID, "first"))
	ok(t, st.TitleAgentSession(ctx, a.ID, "second"))
	ok(t, st.AddAgentCredits(ctx, a.ID, 1.5))
	ok(t, st.SetAgentSpendMode(ctx, a.ID, "auto", 30))
	a, err = st.AgentSession(ctx, a.ID)
	ok(t, err)
	if a.Title != "first" || a.Credits != 1.5 || a.SpendMode != "auto" || a.UpdatedAt != 30 {
		t.Fatalf("session %+v", a)
	}
	list, err := st.AgentSessions(ctx, site.ID)
	ok(t, err)
	if len(list) != 1 || list[0].ID != a.ID {
		t.Fatalf("list %+v", list)
	}

	_, err = st.ClaimAgentTurn(ctx, a.ID, false, 40)
	ok(t, err)
	n, err := st.ResetRunningAgentSessions(ctx)
	ok(t, err)
	if a, _ = st.AgentSession(ctx, a.ID); n != 1 || a.Status != AgentIdle {
		t.Fatalf("reset %d %+v", n, a)
	}

	ok(t, st.DeleteSite(ctx, site.ID))
	if _, err := st.AgentSession(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("session survived its site: %v", err)
	}
}

func TestDeleteAgentSessionGuardsRunningSessions(t *testing.T) {
	st := open(t)
	a, err := st.CreateAgentSession(ctx, 0, "confirm", 1)
	ok(t, err)
	_, err = st.ClaimAgentTurn(ctx, a.ID, false, 2)
	ok(t, err)
	if err := st.DeleteAgentSession(ctx, a.ID); !errors.Is(err, ErrAgentBusy) {
		t.Fatalf("delete while running: %v", err)
	}
	if _, err := st.AgentSession(ctx, a.ID); err != nil {
		t.Fatalf("session removed despite the guard: %v", err)
	}
	ok(t, st.ReleaseAgentTurn(ctx, a.ID, AgentIdle, "", 3))
	ok(t, st.DeleteAgentSession(ctx, a.ID))
	if _, err := st.AgentSession(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	if err := st.DeleteAgentSession(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing: %v", err)
	}
}

func TestAgentMessagesAndToolCalls(t *testing.T) {
	st := open(t)
	a, err := st.CreateAgentSession(ctx, 0, "confirm", 1)
	ok(t, err)
	_, err = st.AddAgentMessage(ctx, AgentMessage{SessionID: a.ID, Turn: 1, Role: "user", Content: "hi", CreatedAt: 2})
	ok(t, err)
	_, err = st.AddAgentMessage(ctx, AgentMessage{SessionID: a.ID, Turn: 1, Role: "assistant", ToolCalls: `[{"id":"c1"}]`, Native: `{"content":[]}`, NativeOf: "anthropic", CreatedAt: 3})
	ok(t, err)
	_, err = st.AddAgentMessage(ctx, AgentMessage{SessionID: a.ID, Turn: 1, Role: "tool", ToolCallID: "c1", Content: "{}", CreatedAt: 4})
	ok(t, err)
	msgs, err := st.AgentMessages(ctx, a.ID)
	ok(t, err)
	if len(msgs) != 3 || msgs[1].NativeOf != "anthropic" || msgs[1].ToolCalls != `[{"id":"c1"}]` || msgs[2].ToolCallID != "c1" {
		t.Fatalf("messages %+v", msgs)
	}

	id, err := st.InsertAgentToolCall(ctx, AgentToolCall{SessionID: a.ID, Turn: 1, CallID: "c1", Tool: "gsc_query", Label: "GSC 数据", Kind: "local", Args: "{}", Status: "running", StartedAt: 5})
	ok(t, err)
	ok(t, st.FinishAgentToolCall(ctx, AgentToolCall{ID: id, Status: "ok", Label: "GSC 数据：query", Cached: true, Credits: 2, Card: `{"type":"lines"}`, FinishedAt: 6}))
	calls, err := st.AgentToolCalls(ctx, a.ID)
	ok(t, err)
	if len(calls) != 1 || calls[0].Status != "ok" || !calls[0].Cached || calls[0].Credits != 2 || calls[0].Label != "GSC 数据：query" || calls[0].FinishedAt != 6 {
		t.Fatalf("calls %+v", calls)
	}
}

func TestPruneWebCache(t *testing.T) {
	st := open(t)
	ok(t, st.SetWebCache(ctx, "expired", "v", 1, 10))
	ok(t, st.SetWebCache(ctx, "fresh", "v", 1, 100))
	n, err := st.PruneWebCache(ctx, 50)
	ok(t, err)
	if n != 1 {
		t.Fatalf("pruned %d rows, want 1", n)
	}
	if _, _, hit, _ := st.WebCache(ctx, "expired", 5); hit {
		t.Fatal("expired entry survived the prune (checked against an earlier now)")
	}
	if _, _, hit, err := st.WebCache(ctx, "fresh", 50); err != nil || !hit {
		t.Fatalf("fresh entry removed: hit=%v err=%v", hit, err)
	}
}

func TestWebCache(t *testing.T) {
	st := open(t)
	if _, _, hit, err := st.WebCache(ctx, "k", 10); err != nil || hit {
		t.Fatalf("empty: %v %v", hit, err)
	}
	ok(t, st.SetWebCache(ctx, "k", "v1", 10, 20))
	if v, at, hit, err := st.WebCache(ctx, "k", 15); err != nil || !hit || v != "v1" || at != 10 {
		t.Fatalf("hit: %q %d %v %v", v, at, hit, err)
	}
	if _, _, hit, _ := st.WebCache(ctx, "k", 20); hit {
		t.Fatal("expired entry served")
	}
	ok(t, st.SetWebCache(ctx, "k", "v2", 30, 40))
	if v, _, hit, _ := st.WebCache(ctx, "k", 31); !hit || v != "v2" {
		t.Fatalf("replace: %q %v", v, hit)
	}
}

func ok(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
