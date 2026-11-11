// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/sitemoment/sitemoment-desktop/internal/agent"
	"github.com/sitemoment/sitemoment-desktop/internal/llm"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

// scriptModel answers every call with the next scripted reply; block makes
// a call wait until its context ends.
type scriptModel struct {
	mu      sync.Mutex
	replies []*llm.Reply
	block   chan struct{}
}

func (m *scriptModel) Label() string { return "测试模型" }

func (m *scriptModel) Chat(ctx context.Context, _ llm.Request, onText func(string)) (*llm.Reply, error) {
	if m.block != nil {
		close(m.block)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.replies[0]
	m.replies = m.replies[1:]
	if onText != nil {
		onText(r.Content)
	}
	return r, nil
}

func agentService(t *testing.T, m *scriptModel) (*AgentService, *store.Store, int64) {
	t.Helper()
	st := openStore(t)
	site, err := st.AddSite(ctx, "example.com", 1)
	if err != nil {
		t.Fatal(err)
	}
	a := agent.New(st, func(context.Context) (llm.Model, error) { return m, nil }, &agent.Deps{}, nil)
	return &AgentService{Agent: a, Store: st}, st, site.ID
}

func TestAgentServiceConversation(t *testing.T) {
	m := &scriptModel{replies: []*llm.Reply{
		{Content: "先读手册。", ToolCalls: []llm.ToolCall{{ID: "c1", Type: "function", Function: llm.FunctionCall{Name: "load_playbook", Arguments: `{"topic":"keywords"}`}}}},
		{Content: "**结论**：先查数。"},
	}}
	s, _, siteID := agentService(t, m)
	sess, err := s.Create(ctx, siteID)
	if err != nil || sess.SpendMode != agent.ModeConfirm || sess.Status != store.AgentIdle {
		t.Fatalf("create %+v %v", sess, err)
	}
	if label, err := s.ModelLabel(ctx); err != nil || label != "测试模型" {
		t.Fatalf("label %q %v", label, err)
	}
	if err := s.Send(ctx, sess.ID, "怎么选词？"); err != nil {
		t.Fatal(err)
	}
	s.Agent.Wait()
	tr, err := s.Transcript(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, it := range tr.Items {
		kinds = append(kinds, it.Kind)
	}
	if len(kinds) != 4 || kinds[0] != "user" || kinds[1] != "note" || kinds[2] != "tool" || kinds[3] != "answer" {
		t.Fatalf("items %v", kinds)
	}
	if tool := tr.Items[2].Tool; tool.Label != "查阅手册：选词与数据口径：先便宜后贵、口径写法、能不能做的判断" || tool.Status != "ok" || tool.Kind != agent.KindWeb {
		t.Fatalf("tool %+v", tool)
	}
	if tr.Session.Title != "怎么选词？" || tr.Pending != nil {
		t.Fatalf("session %+v", tr.Session)
	}
	list, err := s.Sessions(ctx, siteID)
	if err != nil || len(list) != 1 {
		t.Fatalf("sessions %v %v", list, err)
	}
}

func TestAgentServiceModeAndDelete(t *testing.T) {
	m := &scriptModel{block: make(chan struct{})}
	s, st, siteID := agentService(t, m)
	sess, _ := s.Create(ctx, siteID)
	if _, err := s.SetMode(ctx, sess.ID, "spend_everything"); err == nil {
		t.Fatal("accepted a bad mode")
	}
	v, err := s.SetMode(ctx, sess.ID, agent.ModeAuto)
	if err != nil || v.SpendMode != agent.ModeAuto {
		t.Fatalf("set mode %+v %v", v, err)
	}
	next, _ := s.Create(ctx, siteID)
	if next.SpendMode != agent.ModeAuto {
		t.Fatalf("new session mode %q, want the last chosen", next.SpendMode)
	}

	if err := s.Send(ctx, sess.ID, "x"); err != nil {
		t.Fatal(err)
	}
	<-m.block
	if err := s.Delete(ctx, sess.ID); err == nil {
		t.Fatal("deleted a running session")
	}
	if err := s.Confirm(ctx, sess.ID, true); err == nil {
		t.Fatal("confirmed a session that is not waiting")
	}
	if err := s.Stop(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	s.Agent.Wait()
	if err := s.Delete(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AgentSession(ctx, sess.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
}
