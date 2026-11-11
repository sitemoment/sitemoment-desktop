// SPDX-License-Identifier: AGPL-3.0-or-later
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/llm"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

// fakeModel answers with scripted replies, one per call, and records requests.
type fakeModel struct {
	mu      sync.Mutex
	replies []func(ctx context.Context, req llm.Request) (*llm.Reply, error)
	reqs    []llm.Request
}

func (m *fakeModel) Label() string { return "fake" }

func (m *fakeModel) Chat(ctx context.Context, req llm.Request, onText func(string)) (*llm.Reply, error) {
	m.mu.Lock()
	m.reqs = append(m.reqs, req)
	if len(m.replies) == 0 {
		m.mu.Unlock()
		return &llm.Reply{Content: "（脚本用完了）"}, nil
	}
	next := m.replies[0]
	m.replies = m.replies[1:]
	m.mu.Unlock()
	r, err := next(ctx, req)
	if err == nil && onText != nil && r.Content != "" {
		onText(r.Content)
	}
	return r, err
}

func (m *fakeModel) requests() []llm.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]llm.Request(nil), m.reqs...)
}

func say(content string) func(context.Context, llm.Request) (*llm.Reply, error) {
	return func(context.Context, llm.Request) (*llm.Reply, error) { return &llm.Reply{Content: content}, nil }
}

func call(content string, calls ...llm.ToolCall) func(context.Context, llm.Request) (*llm.Reply, error) {
	return func(context.Context, llm.Request) (*llm.Reply, error) {
		return &llm.Reply{Content: content, ToolCalls: calls}, nil
	}
}

func tc(id, name, args string) llm.ToolCall {
	return llm.ToolCall{ID: id, Type: "function", Function: llm.FunctionCall{Name: name, Arguments: args}}
}

// events records emitted agent events.
type events struct {
	mu   sync.Mutex
	list []map[string]any
}

func (e *events) emit(name string, data any) {
	if name != EventName {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.list = append(e.list, data.(map[string]any))
}

func (e *events) types() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for _, ev := range e.list {
		out = append(out, ev["type"].(string))
	}
	return out
}

func (e *events) last(typ string) map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := len(e.list) - 1; i >= 0; i-- {
		if e.list[i]["type"] == typ {
			return e.list[i]
		}
	}
	return nil
}

// rig is an agent over a temp store with fakes.
type rig struct {
	st    *store.Store
	a     *Agent
	model *fakeModel
	ev    *events
	col   *fakeCollectors
	cloud *fakeCloud
	sess  store.AgentSession
}

func newRig(t *testing.T, mode string, replies ...func(context.Context, llm.Request) (*llm.Reply, error)) *rig {
	t.Helper()
	r := &rig{st: openStore(t), model: &fakeModel{replies: replies}, ev: &events{}, col: &fakeCollectors{}, cloud: &fakeCloud{signedIn: true, quote: 3, charge: 1.5}}
	site, err := r.st.AddSite(ctx, "example.com", 1)
	ok(t, err)
	r.sess, err = r.st.CreateAgentSession(ctx, site.ID, mode, 1)
	ok(t, err)
	r.a = New(r.st, func(context.Context) (llm.Model, error) { return r.model, nil }, &Deps{Collectors: r.col, Cloud: r.cloud}, r.ev.emit)
	r.a.Now = func() time.Time { return time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC) }
	return r
}

func (r *rig) send(t *testing.T, text string) {
	t.Helper()
	ok(t, r.a.Start(ctx, r.sess.ID, text))
	r.a.Wait()
}

func (r *rig) session(t *testing.T) store.AgentSession {
	t.Helper()
	s, err := r.st.AgentSession(ctx, r.sess.ID)
	ok(t, err)
	return s
}

func toolNames(req llm.Request) map[string]bool {
	out := map[string]bool{}
	for _, t := range req.Tools {
		out[t.Name] = true
	}
	return out
}

func TestTurnRunsToolsThenAnswers(t *testing.T) {
	r := newRig(t, ModeConfirm,
		call("我先查谷歌第一页。", tc("c1", "google_serp", `{"q":"thumbnail maker"}`)),
		say("**结论**：值得做。"))
	r.send(t, "thumbnail maker 值得做吗？")

	want := []string{"turn_started", "text", "note", "tool_started", "tool_finished", "text", "message", "turn_finished"}
	if got := r.ev.types(); !reflect.DeepEqual(got, want) {
		t.Fatalf("events %v, want %v", got, want)
	}
	fin := r.ev.last("tool_finished")["tool"].(ToolView)
	if fin.Status != "ok" || fin.Kind != KindCollector || fin.Label != "谷歌搜索结果：thumbnail maker" || fin.Card == nil || len(fin.Card.Rows) != 1 {
		t.Fatalf("tool %+v", fin)
	}
	if !strings.Contains(fin.Card.Note, "United States") {
		t.Fatalf("card note %q lacks the region", fin.Card.Note)
	}
	if ev := r.ev.last("turn_finished"); ev["status"] != store.AgentIdle {
		t.Fatalf("finished %v", ev)
	}
	reqs := r.model.requests()
	if len(reqs) != 2 {
		t.Fatalf("%d model calls", len(reqs))
	}
	if sys := reqs[0].Messages[0]; sys.Role != "system" || !strings.Contains(sys.Content, "example.com") || !strings.Contains(sys.Content, "验收") {
		t.Fatalf("system prompt %q", sys.Content)
	}
	tools := toolNames(reqs[0])
	for _, name := range []string{"gsc_query", "keywords", "google_serp", "people_also_ask", "lookup_keywords", "paid_serp", "load_playbook"} {
		if !tools[name] {
			t.Errorf("tool %s not offered", name)
		}
	}
	if tools["keyword_volume"] || tools["serp"] {
		t.Errorf("disabled or dropped paid tools offered: %v", tools)
	}
	last := reqs[1].Messages[len(reqs[1].Messages)-1]
	if last.Role != "tool" || last.ToolCallID != "c1" || !strings.Contains(last.Content, "本机浏览器采集") {
		t.Fatalf("tool result %+v", last)
	}
	s := r.session(t)
	if s.Title != "thumbnail maker 值得做吗？" || s.Status != store.AgentIdle || s.Turn != 1 {
		t.Fatalf("session %+v", s)
	}
}

func TestConfirmModeHoldsPaidCallsUntilApproved(t *testing.T) {
	r := newRig(t, ModeConfirm,
		call("", tc("c1", "lookup_keywords", `{"keywords":["ai chat"]}`), tc("c2", "google_serp", `{"q":"ai chat"}`)),
		say("**结论**：搜索量 1900。"))
	r.send(t, "ai chat 搜索量？")

	if r.cloud.dataCalls() != 0 {
		t.Fatal("paid call ran without approval")
	}
	if len(r.col.calls) != 1 {
		t.Fatalf("free call did not run: %d", len(r.col.calls))
	}
	p := r.ev.last("confirm_needed")["pending"].(*Pending)
	if len(p.Calls) != 1 || p.Calls[0].Tool != "lookup_keywords" || p.TotalMax != 3 || p.Calls[0].Label != "关键词库存数据：ai chat" {
		t.Fatalf("pending %+v", p)
	}
	if s := r.session(t); s.Status != store.AgentAwaitingConfirm || s.Pending == "" {
		t.Fatalf("session %+v", s)
	}
	tr, err := r.a.Transcript(ctx, r.sess.ID)
	ok(t, err)
	if tr.Pending == nil || tr.Pending.TotalMax != 3 {
		t.Fatalf("transcript pending %+v", tr.Pending)
	}

	ok(t, r.a.Resume(ctx, r.sess.ID, true))
	r.a.Wait()
	if r.cloud.dataCalls() != 1 {
		t.Fatalf("paid calls after approval: %d", r.cloud.dataCalls())
	}
	s := r.session(t)
	if s.Status != store.AgentIdle || s.Credits != 1.5 || s.Pending != "" {
		t.Fatalf("session %+v", s)
	}
	if ev := r.ev.last("turn_finished"); ev["credits"] != 1.5 {
		t.Fatalf("turn credits %v", ev)
	}
	reqs := r.model.requests()
	msgs := reqs[1].Messages
	var results []string
	for _, m := range msgs {
		if m.Role == "tool" {
			results = append(results, m.ToolCallID)
		}
	}
	if !reflect.DeepEqual(results, []string{"c2", "c1"}) {
		t.Fatalf("tool results %v", results)
	}
}

func TestQuoteErrorDoesNotRunTheCallOrNeedConfirmation(t *testing.T) {
	r := newRig(t, ModeConfirm, call("", tc("c1", "lookup_keywords", `{"keywords":["x"]}`)), say("ok"))
	r.cloud.quoteErr = errors.New("HTTP 503")
	r.send(t, "x")
	if r.cloud.dataCalls() != 0 {
		t.Fatal("paid call ran despite a failed price quote")
	}
	if ev := r.ev.last("confirm_needed"); ev != nil {
		t.Fatalf("a quote error should not pause for confirmation: %v", ev)
	}
	s := r.session(t)
	if s.Credits != 0 || s.Status != store.AgentIdle {
		t.Fatalf("session %+v", s)
	}
	msgs := r.model.requests()[1].Messages
	last := msgs[len(msgs)-1]
	if last.Role != "tool" || last.ToolCallID != "c1" {
		t.Fatalf("no tool result for the unquotable call: %+v", msgs)
	}
}

func TestAutoModeQuoteErrorDoesNotCountTowardTheCap(t *testing.T) {
	r := newRig(t, ModeAuto,
		call("", tc("c1", "lookup_keywords", `{"keywords":["a"]}`), tc("c2", "lookup_keywords", `{"keywords":["b"]}`)),
		say("ok"))
	r.cloud.quoteErr = errors.New("HTTP 429")
	r.send(t, "a 和 b")
	if r.cloud.dataCalls() != 0 {
		t.Fatalf("auto mode ran %d paid calls despite quote errors", r.cloud.dataCalls())
	}
	if r.session(t).Credits != 0 {
		t.Fatalf("credits charged despite quote errors: %+v", r.session(t))
	}
}

func TestDeclinedPaidCallsTellTheModel(t *testing.T) {
	r := newRig(t, ModeConfirm, call("", tc("c1", "lookup_keywords", `{"keywords":["x"]}`)), say("没有付费数据，先这样。"))
	r.send(t, "x")
	ok(t, r.a.Resume(ctx, r.sess.ID, false))
	r.a.Wait()
	if r.cloud.dataCalls() != 0 {
		t.Fatal("declined call ran")
	}
	msgs := r.model.requests()[1].Messages
	last := msgs[len(msgs)-1]
	if last.Role != "tool" || !strings.Contains(last.Content, "declined") {
		t.Fatalf("decline %+v", last)
	}
	if err := r.a.Resume(ctx, r.sess.ID, true); err == nil {
		t.Fatal("resumed a session that is not waiting")
	}
}

func TestNewMessageDeclinesPendingCalls(t *testing.T) {
	r := newRig(t, ModeConfirm, call("", tc("c1", "lookup_keywords", `{"keywords":["x"]}`)), say("好的。"))
	r.send(t, "x")
	r.send(t, "算了，不用付费数据")
	msgs := r.model.requests()[1].Messages
	var sawDecline bool
	for _, m := range msgs {
		if m.Role == "tool" && m.ToolCallID == "c1" && strings.Contains(m.Content, "新消息") {
			sawDecline = true
		}
	}
	if !sawDecline || r.session(t).Turn != 2 {
		t.Fatalf("decline missing: %+v", msgs)
	}
}

func TestAutoModeSpendsUpToTheTurnCap(t *testing.T) {
	r := newRig(t, ModeAuto,
		call("", tc("c1", "lookup_keywords", `{"keywords":["a"]}`), tc("c2", "lookup_keywords", `{"keywords":["b"]}`)),
		say("ok"))
	r.cloud.quote = 15
	r.send(t, "a 和 b")
	if r.cloud.dataCalls() != 1 {
		t.Fatalf("auto ran %d paid calls, want 1 (cap %d)", r.cloud.dataCalls(), autoTurnCap)
	}
	if p := r.ev.last("confirm_needed"); p == nil || len(p["pending"].(*Pending).Calls) != 1 {
		t.Fatalf("second call not held: %v", p)
	}
}

func TestFreeOnlyHidesPaidTools(t *testing.T) {
	r := newRig(t, ModeFreeOnly, call("", tc("c1", "lookup_keywords", `{"keywords":["a"]}`)), say("ok"))
	r.send(t, "a")
	req := r.model.requests()[0]
	if toolNames(req)["lookup_keywords"] {
		t.Fatal("paid tool offered in free_only")
	}
	if !strings.Contains(req.Messages[0].Content, "只用免费工具") {
		t.Fatal("prompt does not state the mode")
	}
	msgs := r.model.requests()[1].Messages
	if last := msgs[len(msgs)-1]; !strings.Contains(last.Content, "unknown_tool") || r.cloud.dataCalls() != 0 {
		t.Fatalf("result %+v", last)
	}
}

func TestStepLimitForcesAnAnswer(t *testing.T) {
	loop := call("", tc("c1", "load_playbook", `{"topic":"keywords"}`))
	r := newRig(t, ModeConfirm, loop, loop, say("最终回答"))
	r.a.MaxSteps = 2
	r.send(t, "x")
	reqs := r.model.requests()
	if len(reqs) != 3 || !reqs[2].NoToolUse || len(reqs[2].Tools) == 0 {
		t.Fatalf("final call %+v", reqs[len(reqs)-1].NoToolUse)
	}
	if ev := r.ev.last("message"); ev["content"] != "最终回答" {
		t.Fatalf("answer %v", ev)
	}
}

func TestStopEndsTheTurn(t *testing.T) {
	started := make(chan struct{})
	r := newRig(t, ModeConfirm, func(ctx context.Context, _ llm.Request) (*llm.Reply, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	ok(t, r.a.Start(ctx, r.sess.ID, "x"))
	<-started
	if err := r.a.Start(ctx, r.sess.ID, "y"); !errors.Is(err, ErrBusy) {
		t.Fatalf("second start: %v", err)
	}
	r.a.Stop(r.sess.ID)
	r.a.Wait()
	if ev := r.ev.last("stopped"); ev == nil {
		t.Fatalf("no stopped event: %v", r.ev.types())
	}
	msgs, err := r.st.AgentMessages(ctx, r.sess.ID)
	ok(t, err)
	if last := msgs[len(msgs)-1]; last.Content != stopNote || r.session(t).Status != store.AgentIdle {
		t.Fatalf("after stop %+v", last)
	}
}

func TestHostedCreditsExhaustedSuggestsOwnKey(t *testing.T) {
	r := newRig(t, ModeConfirm, func(context.Context, llm.Request) (*llm.Reply, error) {
		return nil, &llm.Error{Status: 402, Code: llm.CodeCreditsExhausted, Msg: "HTTP 402"}
	})
	r.send(t, "x")
	if msg, _ := r.ev.last("error")["message"].(string); !strings.Contains(msg, "自己的模型 key") {
		t.Fatalf("error %q", msg)
	}
	if r.session(t).Status != store.AgentIdle {
		t.Fatal("session not released")
	}
}

func TestModelNotConfiguredFailsBeforeClaiming(t *testing.T) {
	r := newRig(t, ModeConfirm)
	r.a.Models = func(context.Context) (llm.Model, error) { return nil, errors.New("请先登录 SiteMoment") }
	if err := r.a.Start(ctx, r.sess.ID, "x"); err == nil || r.session(t).Turn != 0 {
		t.Fatalf("start: %v", err)
	}
}

func TestTranscriptKeepsNativeAndRepairsInterruptedCalls(t *testing.T) {
	r := newRig(t, ModeConfirm, say("ok"))
	raw, _ := json.Marshal([]llm.ToolCall{tc("c9", "load_playbook", `{}`)})
	_, err := r.st.AddAgentMessage(ctx, store.AgentMessage{SessionID: r.sess.ID, Turn: 1, Role: "user", Content: "a", CreatedAt: 1})
	ok(t, err)
	_, err = r.st.AddAgentMessage(ctx, store.AgentMessage{SessionID: r.sess.ID, Turn: 1, Role: "assistant", ToolCalls: string(raw), Native: `{"role":"assistant"}`, NativeOf: "anthropic", CreatedAt: 2})
	ok(t, err)
	ok(t, r.st.ReleaseAgentTurn(ctx, r.sess.ID, store.AgentIdle, "", 2))
	r.send(t, "b")
	msgs := r.model.requests()[0].Messages
	if msgs[2].NativeOf != "anthropic" || string(msgs[2].Native) != `{"role":"assistant"}` {
		t.Fatalf("native lost: %+v", msgs[2])
	}
	if msgs[3].Role != "tool" || msgs[3].ToolCallID != "c9" || !strings.Contains(msgs[3].Content, "interrupted") {
		t.Fatalf("no stand-in result: %+v", msgs[3])
	}
}

func TestOldToolResultsAreShortened(t *testing.T) {
	r := newRig(t, ModeConfirm, say("ok"))
	long := strings.Repeat("x", 3000)
	_, err := r.st.AddAgentMessage(ctx, store.AgentMessage{SessionID: r.sess.ID, Turn: 1, Role: "tool", ToolCallID: "c1", Content: long, CreatedAt: 1})
	ok(t, err)
	_, err = r.st.AddAgentMessage(ctx, store.AgentMessage{SessionID: r.sess.ID, Turn: 2, Role: "tool", ToolCallID: "c2", Content: long, CreatedAt: 1})
	ok(t, err)
	// Turns 1 and 2 happened; the next one is turn 3.
	for range 2 {
		_, err = r.st.ClaimAgentTurn(ctx, r.sess.ID, false, 1)
		ok(t, err)
		ok(t, r.st.ReleaseAgentTurn(ctx, r.sess.ID, store.AgentIdle, "", 1))
	}
	r.send(t, "c")
	var lens []int
	for _, m := range r.model.requests()[0].Messages {
		if m.Role == "tool" {
			lens = append(lens, len(m.Content))
		}
	}
	if len(lens) != 2 || lens[0] >= 3000 || lens[1] != 3000 {
		t.Fatalf("tool result lengths %v", lens)
	}
}
