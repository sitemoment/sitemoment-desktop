// SPDX-License-Identifier: AGPL-3.0-or-later
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/llm"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

const (
	// defaultMaxSteps is model calls per turn before it must answer.
	defaultMaxSteps = 10
	// maxCallsPerStep caps tool calls the model may request at once.
	maxCallsPerStep = 8
	// freeParallel is free tools run at the same time.
	freeParallel = 4
	// oldToolText caps tool results from turns before the previous one.
	oldToolText = 1500
	// turnTimeout bounds one turn (collectors may wait for a human).
	turnTimeout = 20 * time.Minute
	// callTimeout bounds one tool call.
	callTimeout = 5 * time.Minute
	// EventName is the Wails event every agent event goes out on.
	EventName = "agent:event"
)

// ErrStopped is the cancel cause of a turn the user stopped.
var ErrStopped = errors.New("stopped by the user")

// ErrBusy means the session is already answering.
var ErrBusy = errors.New("这个会话正在回答，请等它结束或先停止")

const stopNote = "（已停止，这一轮没有给出结论。）"

// Agent runs turns of SEO conversations. Safe for concurrent use; one turn
// per session at a time (store.ClaimAgentTurn).
type Agent struct {
	Store  *store.Store
	Models func(ctx context.Context) (llm.Model, error)
	Deps   *Deps
	// Emit sends a UI event (main.go: to the main window only).
	Emit     func(name string, data any)
	Now      func() time.Time
	MaxSteps int

	mu      sync.Mutex
	cancels map[int64]context.CancelCauseFunc
	wg      sync.WaitGroup
}

// New builds an agent. deps holds the tools' clients (nil ones turn their
// tools off); its Store defaults to st.
func New(st *store.Store, models func(ctx context.Context) (llm.Model, error), deps *Deps, emit func(name string, data any)) *Agent {
	if deps == nil {
		deps = &Deps{}
	}
	if deps.Store == nil {
		deps.Store = st
	}
	return &Agent{Store: st, Models: models, Deps: deps, Emit: emit}
}

func (a *Agent) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *Agent) maxSteps() int {
	if a.MaxSteps > 0 {
		return a.MaxSteps
	}
	return defaultMaxSteps
}

// Wait blocks until every running turn has finished (tests, shutdown).
func (a *Agent) Wait() { a.wg.Wait() }

// pendingState is a turn paused for confirmation (agent_sessions.pending).
type pendingState struct {
	Step      int           `json:"step"`
	TurnSpent float64       `json:"turnSpent"`
	Calls     []pendingCall `json:"calls"`
}

type pendingCall struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Arguments  string  `json:"arguments"`
	Label      string  `json:"label"`
	MaxCredits float64 `json:"maxCredits"`
}

func pendingOf(raw string) *pendingState {
	if raw == "" {
		return nil
	}
	var p pendingState
	if json.Unmarshal([]byte(raw), &p) != nil || len(p.Calls) == 0 {
		return nil
	}
	return &p
}

func (p *pendingState) view() *Pending {
	v := &Pending{Calls: []PendingCall{}}
	for _, c := range p.Calls {
		v.Calls = append(v.Calls, PendingCall{CallID: c.ID, Tool: c.Name, Label: c.Label, MaxCredits: c.MaxCredits})
		v.TotalMax += c.MaxCredits
	}
	return v
}

// turn is the state of one running turn.
type turn struct {
	sess   store.AgentSession
	n      int
	spent  float64
	model  llm.Model
	env    *Env
	tools  []*Tool
	byName map[string]*Tool
	paid   bool // paid tools offered
	// approved lets held paid calls run after the user said yes.
	approved map[string]bool
	cancel   context.CancelCauseFunc
}

// Start begins a turn answering text and returns once it is running.
func (a *Agent) Start(ctx context.Context, sessionID int64, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("请输入问题")
	}
	model, err := a.Models(ctx)
	if err != nil {
		return err
	}
	sess, err := a.Store.ClaimAgentTurn(ctx, sessionID, false, a.now().Unix())
	if errors.Is(err, store.ErrAgentBusy) {
		return ErrBusy
	}
	if err != nil {
		return err
	}
	tctx, t := a.begin(sess, model)
	// A new message while paid calls wait for approval declines them.
	if p := pendingOf(sess.Pending); p != nil {
		a.declinePending(tctx, t, p, "用户发了新消息，没有批准这些付费调用")
	}
	if err := a.Store.TitleAgentSession(tctx, sess.ID, truncateRunes(text, 40)); err != nil {
		a.fail(tctx, t, err)
		return nil
	}
	if _, err := a.Store.AddAgentMessage(tctx, store.AgentMessage{SessionID: sess.ID, Turn: t.n, Role: "user", Content: text, CreatedAt: a.now().Unix()}); err != nil {
		a.fail(tctx, t, err)
		return nil
	}
	a.emit(t, "turn_started", nil)
	a.run(func() { a.loop(tctx, t, 0) })
	return nil
}

// Resume continues a turn paused for confirmation: approve runs the held
// paid calls, otherwise the model is told they were declined.
func (a *Agent) Resume(ctx context.Context, sessionID int64, approve bool) error {
	model, err := a.Models(ctx)
	if err != nil {
		return err
	}
	sess, err := a.Store.ClaimAgentTurn(ctx, sessionID, true, a.now().Unix())
	if errors.Is(err, store.ErrAgentBusy) {
		return errors.New("这个会话没有在等确认")
	}
	if err != nil {
		return err
	}
	tctx, t := a.begin(sess, model)
	p := pendingOf(sess.Pending)
	if p == nil {
		a.release(tctx, t, store.AgentIdle, "")
		return nil
	}
	t.spent = p.TurnSpent
	a.run(func() {
		if approve {
			calls := make([]llm.ToolCall, len(p.Calls))
			for i, c := range p.Calls {
				calls[i] = llm.ToolCall{ID: c.ID, Type: "function", Function: llm.FunctionCall{Name: c.Name, Arguments: c.Arguments}}
				t.approved[c.ID] = true
			}
			a.runCalls(tctx, t, calls)
		} else {
			a.declinePending(tctx, t, p, "用户拒绝了这些付费调用")
		}
		a.loop(tctx, t, p.Step+1)
	})
	return nil
}

// Stop cancels the session's running turn (no-op when none runs).
func (a *Agent) Stop(sessionID int64) {
	a.mu.Lock()
	cancel := a.cancels[sessionID]
	a.mu.Unlock()
	if cancel != nil {
		cancel(ErrStopped)
	}
}

// begin builds the turn and its cancellable context.
func (a *Agent) begin(sess store.AgentSession, model llm.Model) (context.Context, *turn) {
	ctx, cancel := context.WithCancelCause(context.Background())
	ctx, stopTimer := context.WithTimeout(ctx, turnTimeout)
	a.mu.Lock()
	if a.cancels == nil {
		a.cancels = map[int64]context.CancelCauseFunc{}
	}
	a.cancels[sess.ID] = cancel
	a.mu.Unlock()
	context.AfterFunc(ctx, stopTimer)
	t := &turn{sess: sess, n: sess.Turn, model: model, approved: map[string]bool{}, cancel: cancel}
	t.env = &Env{Deps: a.Deps}
	if sess.SiteID != 0 {
		if site, err := a.Store.Site(ctx, sess.SiteID); err == nil {
			t.env.Site = site
		}
	}
	t.tools, t.paid = a.toolset(ctx, t.env, sess.SpendMode)
	t.byName = map[string]*Tool{}
	for _, tool := range t.tools {
		t.byName[tool.Name] = tool
	}
	return ctx, t
}

// run runs fn in the background.
func (a *Agent) run(fn func()) {
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		fn()
	}()
}

func (a *Agent) declinePending(ctx context.Context, t *turn, p *pendingState, why string) {
	for _, c := range p.Calls {
		a.addToolMessage(ctx, t, c.ID, jsonText(map[string]string{"error": "declined", "message": why + "。不要再调用它，基于已有数据作答，并把缺的数据写进「下一轮要补的数据」。"}))
	}
}

func (a *Agent) loop(ctx context.Context, t *turn, start int) {
	for step := start; step < a.maxSteps(); step++ {
		msgs, err := a.transcript(ctx, t)
		if err != nil {
			a.fail(ctx, t, err)
			return
		}
		reply, err := t.model.Chat(ctx, llm.Request{Messages: msgs, Tools: toolDefs(t.tools)}, func(s string) {
			a.emit(t, "text", map[string]any{"delta": s})
		})
		if err != nil {
			a.fail(ctx, t, err)
			return
		}
		if len(reply.ToolCalls) == 0 {
			a.answer(ctx, t, reply)
			return
		}
		calls := reply.ToolCalls
		if len(calls) > maxCallsPerStep {
			calls = calls[:maxCallsPerStep]
		}
		raw, _ := json.Marshal(calls)
		if _, err := a.Store.AddAgentMessage(ctx, store.AgentMessage{SessionID: t.sess.ID, Turn: t.n, Role: "assistant", Content: reply.Content,
			ToolCalls: string(raw), Native: string(reply.Native), NativeOf: reply.NativeOf, CreatedAt: a.now().Unix()}); err != nil {
			a.fail(ctx, t, err)
			return
		}
		a.emit(t, "note", map[string]any{"content": strings.TrimSpace(reply.Content)})
		if paused := a.step(ctx, t, step, calls); paused {
			return
		}
		if ctx.Err() != nil {
			a.fail(ctx, t, context.Cause(ctx))
			return
		}
	}
	// Out of steps: one last call that may not use tools.
	msgs, err := a.transcript(ctx, t)
	if err != nil {
		a.fail(ctx, t, err)
		return
	}
	msgs = append(msgs, llm.Message{Role: "user", Content: "（系统：本轮工具调用次数已达上限。请基于已有数据直接作答，缺的数据写进「下一轮要补的数据」。）"})
	reply, err := t.model.Chat(ctx, llm.Request{Messages: msgs, Tools: toolDefs(t.tools), NoToolUse: true}, func(s string) {
		a.emit(t, "text", map[string]any{"delta": s})
	})
	if err != nil {
		a.fail(ctx, t, err)
		return
	}
	reply.ToolCalls = nil
	a.answer(ctx, t, reply)
}

// step runs one round of tool calls. Uncached paid calls that need the
// user's approval are held back and the turn pauses (returns true) once the
// others have run.
func (a *Agent) step(ctx context.Context, t *turn, step int, calls []llm.ToolCall) bool {
	var run []llm.ToolCall
	var held []pendingCall
	planned := t.spent
	for _, c := range calls {
		tool := t.byName[c.Function.Name]
		if tool == nil || !tool.Paid || t.sess.SpendMode == ModeFreeOnly {
			run = append(run, c)
			continue
		}
		args, err := parseArgs(c.Function.Arguments)
		if err != nil {
			run = append(run, c) // runCalls reports the bad arguments
			continue
		}
		maxCredits, cached, err := tool.Quote(ctx, t.env, args)
		if err != nil || cached {
			run = append(run, c)
			continue
		}
		if t.sess.SpendMode == ModeAuto && planned+maxCredits <= autoTurnCap {
			planned += maxCredits
			run = append(run, c)
			continue
		}
		held = append(held, pendingCall{ID: c.ID, Name: c.Function.Name, Arguments: c.Function.Arguments, Label: tool.LabelFor(args), MaxCredits: maxCredits})
	}
	a.runCalls(ctx, t, run)
	if len(held) == 0 {
		return false
	}
	p := pendingState{Step: step, TurnSpent: t.spent, Calls: held}
	raw, _ := json.Marshal(p)
	a.emit(t, "confirm_needed", map[string]any{"pending": p.view()})
	a.release(ctx, t, store.AgentAwaitingConfirm, string(raw))
	return true
}

// runCalls executes calls: free ones in parallel, paid ones one at a time.
// Rows and tool messages keep call order.
func (a *Agent) runCalls(ctx context.Context, t *turn, calls []llm.ToolCall) {
	runs := make([]*started, len(calls))
	results := make([]string, len(calls))
	for i, c := range calls {
		runs[i], results[i] = a.start(ctx, t, c)
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, freeParallel)
	for i, st := range runs {
		if st == nil || st.tool.Paid {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, st *started) {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = a.finish(ctx, t, st)
		}(i, st)
	}
	wg.Wait()
	for i, st := range runs {
		if st != nil && st.tool.Paid {
			results[i] = a.finish(ctx, t, st)
		}
	}
	for i, c := range calls {
		a.addToolMessage(ctx, t, c.ID, results[i])
	}
}

func parseArgs(raw string) (map[string]any, error) {
	args := map[string]any{}
	if strings.TrimSpace(raw) == "" {
		return args, nil
	}
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil, fmt.Errorf("arguments are not a JSON object: %w", err)
	}
	return args, nil
}

// started is a recorded call about to run.
type started struct {
	call  llm.ToolCall
	tool  *Tool
	args  map[string]any
	view  ToolView
	rowID int64
	at    time.Time
}

// start validates and records a call. A call that cannot run returns nil and
// the text for the model.
func (a *Agent) start(ctx context.Context, t *turn, c llm.ToolCall) (*started, string) {
	tool := t.byName[c.Function.Name]
	if tool == nil {
		return nil, jsonText(map[string]string{"error": "unknown_tool", "message": "没有这个工具：" + c.Function.Name})
	}
	args, err := parseArgs(c.Function.Arguments)
	if err != nil {
		return nil, jsonText(map[string]string{"error": "bad_arguments", "message": err.Error()})
	}
	st := &started{call: c, tool: tool, args: args, at: a.now()}
	st.view = ToolView{CallID: c.ID, Tool: tool.Name, Label: tool.LabelFor(args), Kind: tool.Kind, Status: "running"}
	argsJSON, _ := json.Marshal(args)
	st.rowID, err = a.Store.InsertAgentToolCall(context.WithoutCancel(ctx), store.AgentToolCall{SessionID: t.sess.ID, Turn: t.n, CallID: c.ID, Tool: tool.Name,
		Label: st.view.Label, Kind: tool.Kind, Args: string(argsJSON), Status: "running", StartedAt: st.at.UnixMilli()})
	if err != nil {
		return nil, jsonText(map[string]string{"error": "failed", "message": err.Error()})
	}
	a.emit(t, "tool_started", map[string]any{"tool": st.view})
	return st, ""
}

// finish runs a started call, stores its outcome and returns the text for the model.
func (a *Agent) finish(ctx context.Context, t *turn, st *started) string {
	var res *Result
	var err error
	if st.tool.Paid && t.sess.SpendMode == ModeFreeOnly && !t.approved[st.call.ID] {
		err = errFreeOnly
	} else {
		cctx, cancel := context.WithTimeout(ctx, callTimeout)
		res, err = st.tool.Run(cctx, t.env, st.args)
		cancel()
	}
	v := st.view
	text := ""
	if err != nil {
		v.Status = "error"
		msg, forModel := describeError(err)
		if stopped(ctx) {
			msg, forModel = "已停止", jsonText(map[string]string{"error": "stopped", "message": "用户停止了这一轮。"})
		}
		v.Error, text = msg, forModel
	} else {
		v.Status, v.Card, v.Credits, v.Cached, text = "ok", res.Card, res.Credits, res.Cached, res.Text
		if v.Card != nil && v.Card.Title == "" {
			v.Card.Title = v.Label
		}
		if res.Credits > 0 {
			t.spent += res.Credits // paid calls run one at a time
			if err := a.Store.AddAgentCredits(context.WithoutCancel(ctx), t.sess.ID, res.Credits); err != nil {
				v.Error = err.Error()
			}
		}
	}
	end := a.now()
	v.Ms = end.Sub(st.at).Milliseconds()
	card := ""
	if v.Card != nil {
		raw, _ := json.Marshal(v.Card)
		card = string(raw)
	}
	_ = a.Store.FinishAgentToolCall(context.WithoutCancel(ctx), store.AgentToolCall{ID: st.rowID, Status: v.Status, Label: v.Label, Cached: v.Cached,
		Credits: v.Credits, Card: card, Error: v.Error, FinishedAt: end.UnixMilli()})
	a.emit(t, "tool_finished", map[string]any{"tool": v})
	return capText(text, maxToolText)
}

func (a *Agent) addToolMessage(ctx context.Context, t *turn, callID, text string) {
	// Kept after a stop: every stored tool call needs its result in the next transcript.
	_, _ = a.Store.AddAgentMessage(context.WithoutCancel(ctx), store.AgentMessage{SessionID: t.sess.ID, Turn: t.n, Role: "tool", Content: text, ToolCallID: callID, CreatedAt: a.now().Unix()})
}

func (a *Agent) answer(ctx context.Context, t *turn, r *llm.Reply) {
	content := strings.TrimSpace(r.Content)
	if content == "" {
		content = "（模型没有返回内容，请重试。）"
	}
	if _, err := a.Store.AddAgentMessage(ctx, store.AgentMessage{SessionID: t.sess.ID, Turn: t.n, Role: "assistant", Content: content,
		Native: string(r.Native), NativeOf: r.NativeOf, CreatedAt: a.now().Unix()}); err != nil {
		a.fail(ctx, t, err)
		return
	}
	a.emit(t, "message", map[string]any{"content": content})
	a.release(ctx, t, store.AgentIdle, "")
}

func stopped(ctx context.Context) bool { return errors.Is(context.Cause(ctx), ErrStopped) }

func (a *Agent) fail(ctx context.Context, t *turn, err error) {
	if stopped(ctx) {
		a.stop(ctx, t)
		return
	}
	var le *llm.Error
	msg := truncateRunes(err.Error(), 300)
	switch {
	case errors.As(err, &le) && le.Code == llm.CodeCreditsExhausted:
		msg = "本月 SiteMoment 额度已用完。可以在「设置 → AI 模型」里改用自己的模型 key，或等下个月额度重置。"
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(context.Cause(ctx), context.DeadlineExceeded):
		msg = "这一轮超时了，请重试或缩小问题范围"
	}
	a.emit(t, "error", map[string]any{"message": msg})
	a.release(ctx, t, store.AgentIdle, "")
}

// stop ends a stopped turn. What already ran is kept; a short assistant note
// closes the turn, so the next one starts after a complete exchange.
func (a *Agent) stop(ctx context.Context, t *turn) {
	ctx = context.WithoutCancel(ctx)
	_, _ = a.Store.AddAgentMessage(ctx, store.AgentMessage{SessionID: t.sess.ID, Turn: t.n, Role: "assistant", Content: stopNote, CreatedAt: a.now().Unix()})
	a.emit(t, "stopped", map[string]any{"content": stopNote})
	a.release(ctx, t, store.AgentIdle, "")
}

func (a *Agent) release(ctx context.Context, t *turn, status, pending string) {
	ctx = context.WithoutCancel(ctx)
	_ = a.Store.ReleaseAgentTurn(ctx, t.sess.ID, status, pending, a.now().Unix())
	a.mu.Lock()
	delete(a.cancels, t.sess.ID)
	a.mu.Unlock()
	a.emit(t, "turn_finished", map[string]any{"status": status, "credits": t.spent})
	t.cancel(nil) // frees the turn's timer
}

func (a *Agent) emit(t *turn, typ string, payload map[string]any) {
	if a.Emit == nil {
		return
	}
	ev := map[string]any{"sessionId": t.sess.ID, "turn": t.n, "type": typ}
	for k, v := range payload {
		ev[k] = v
	}
	a.Emit(EventName, ev)
}

func toolDefs(tools []*Tool) []llm.Tool {
	out := make([]llm.Tool, len(tools))
	for i, t := range tools {
		desc := t.Description
		if t.Paid {
			desc = "[付费] " + desc
		}
		out[i] = llm.Tool{Name: t.Name, Description: desc, Parameters: t.Params}
	}
	return out
}

func (a *Agent) siteContext(ctx context.Context, siteID int64) siteContext {
	if siteID == 0 {
		return siteContext{}
	}
	site, err := a.Store.Site(ctx, siteID)
	if err != nil {
		return siteContext{}
	}
	sc := siteContext{Host: site.Host, GSC: site.GSCProperty != "", GA: site.GAProperty != "", Bing: site.BingSite != ""}
	sc.GSCDay, _ = a.Store.LatestDay(ctx, siteID, "gsc", "date")
	sc.GADay, _ = a.Store.LatestDay(ctx, siteID, "ga", "date")
	sc.BingDay, _ = a.Store.LatestDay(ctx, siteID, "bing", "traffic")
	if kws, err := a.Store.Keywords(ctx, siteID, "", "", ""); err == nil {
		sc.Keywords = len(kws)
	}
	return sc
}

// transcript is the system prompt plus the stored conversation. Tool
// results from turns before the previous one are shortened, and a tool call
// whose result never got stored (the app quit mid-call) gets a stand-in.
func (a *Agent) transcript(ctx context.Context, t *turn) ([]llm.Message, error) {
	rows, err := a.Store.AgentMessages(ctx, t.sess.ID)
	if err != nil {
		return nil, err
	}
	in := promptInput{Now: a.now(), Site: a.siteContext(ctx, t.sess.SiteID), Mode: t.sess.SpendMode, Paid: t.paid,
		SignedIn: a.Deps != nil && a.Deps.Cloud != nil && a.Deps.Cloud.SignedIn()}
	msgs := []llm.Message{{Role: "system", Content: systemPrompt(in)}}
	answered := map[string]bool{}
	for _, r := range rows {
		if r.Role == "tool" {
			answered[r.ToolCallID] = true
		}
	}
	for _, r := range rows {
		m := llm.Message{Role: r.Role, Content: r.Content, ToolCallID: r.ToolCallID, NativeOf: r.NativeOf}
		if r.Native != "" {
			m.Native = json.RawMessage(r.Native)
		}
		if r.ToolCalls != "" {
			_ = json.Unmarshal([]byte(r.ToolCalls), &m.ToolCalls)
		}
		if r.Role == "tool" && r.Turn < t.n-1 {
			m.Content = capText(m.Content, oldToolText)
		}
		msgs = append(msgs, m)
		for _, c := range m.ToolCalls {
			if !answered[c.ID] {
				msgs = append(msgs, llm.Message{Role: "tool", ToolCallID: c.ID, Content: `{"error":"interrupted","message":"这个调用没有完成（应用退出或出错）。"}`})
			}
		}
	}
	return msgs, nil
}
