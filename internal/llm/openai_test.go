// SPDX-License-Identifier: AGPL-3.0-or-later
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

var ctx = context.Background()

// fake is an OpenAI-compatible server: each request gets the next handler.
type fake struct {
	t      *testing.T
	mu     sync.Mutex
	bodies []map[string]any
	auths  []string
	steps  []func(w http.ResponseWriter)
}

func newFake(t *testing.T, steps ...func(w http.ResponseWriter)) (*fake, string) {
	f := &fake{t: t, steps: steps}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path %s", r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		json.Unmarshal(raw, &body)
		f.mu.Lock()
		n := len(f.bodies)
		f.bodies = append(f.bodies, body)
		f.auths = append(f.auths, r.Header.Get("Authorization"))
		f.mu.Unlock()
		if n >= len(f.steps) {
			t.Errorf("unexpected request %d", n+1)
			w.WriteHeader(500)
			return
		}
		f.steps[n](w)
	}))
	t.Cleanup(srv.Close)
	return f, srv.URL
}

// sse writes chunks as server-sent events, then [DONE].
func sse(chunks ...string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}
}

// sseNoDone writes chunks as server-sent events and then just closes the
// connection, the way some servers do instead of sending [DONE].
func sseNoDone(chunks ...string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
		}
	}
}

func status(code int, body string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		w.Write([]byte(body))
	}
}

var weatherTool = []Tool{{Name: "gsc_query", Description: "Search Console", Parameters: json.RawMessage(`{"type":"object","properties":{"dimension":{"type":"string"}}}`)}}

func TestOpenAIStreamsText(t *testing.T) {
	f, base := newFake(t, sse(
		`{"choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"结论："}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"值得做"}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":120,"completion_tokens":8,"total_tokens":128}}`,
	))
	p := &OpenAI{BaseURL: base, Key: "sk-test", Model: "m1"}
	var got []string
	r, err := p.Chat(ctx, Request{Messages: []Message{{Role: "user", Content: "hi"}}}, func(s string) { got = append(got, s) })
	if err != nil {
		t.Fatal(err)
	}
	if r.Content != "结论：值得做" || strings.Join(got, "|") != "结论：|值得做" || r.FinishReason != "stop" || r.Usage != (Usage{120, 8}) {
		t.Fatalf("reply %+v, deltas %q", r, got)
	}
	b := f.bodies[0]
	if b["stream"] != true || b["model"] != "m1" || b["max_tokens"] != 4096.0 || f.auths[0] != "Bearer sk-test" {
		t.Fatalf("request %v auth %q", b, f.auths[0])
	}
	if so, _ := b["stream_options"].(map[string]any); so["include_usage"] != true {
		t.Fatalf("stream_options %v", b["stream_options"])
	}
	if _, ok := b["tools"]; ok {
		t.Fatal("tools sent without tools")
	}
}

func TestOpenAIToolCallDeltas(t *testing.T) {
	_, base := newFake(t, sse(
		// Two parallel calls start in one chunk; arguments arrive in pieces.
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"gsc_query","arguments":""}},{"index":1,"id":"call_b","type":"function","function":{"name":"fetch_page","arguments":""}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"dimen"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"{\"url\":\"https://a.com\"}"}},{"index":0,"function":{"arguments":"sion\":\"query\"}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	))
	p := &OpenAI{BaseURL: base + "/v1", Model: "m"}
	r, err := p.Chat(ctx, Request{Messages: []Message{{Role: "user", Content: "x"}}, Tools: weatherTool}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.ToolCalls) != 2 || r.FinishReason != "tool_calls" {
		t.Fatalf("reply %+v", r)
	}
	a, b := r.ToolCalls[0], r.ToolCalls[1]
	if a.ID != "call_a" || a.Function.Name != "gsc_query" || a.Function.Arguments != `{"dimension":"query"}` || a.Type != "function" {
		t.Fatalf("call a %+v", a)
	}
	if b.ID != "call_b" || b.Function.Name != "fetch_page" || b.Function.Arguments != `{"url":"https://a.com"}` {
		t.Fatalf("call b %+v", b)
	}
}

func TestOpenAIToolCallsWithoutDistinctIndex(t *testing.T) {
	_, base := newFake(t, sse(
		// Some compatible servers send index 0 for every call, or no index at all.
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"a","arguments":"{}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c2","function":{"name":"b","arguments":"{\"x\":"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"function":{"name":"c","arguments":""}}]}}]}`,
	))
	p := &OpenAI{BaseURL: base, Model: "m"}
	r, err := p.Chat(ctx, Request{Messages: []Message{{Role: "user", Content: "x"}}, Tools: weatherTool}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.ToolCalls) != 3 || r.ToolCalls[1].Function.Arguments != `{"x":1}` || r.ToolCalls[2].Function.Name != "c" ||
		r.ToolCalls[2].ID != "call_3" || r.ToolCalls[2].Function.Arguments != "{}" {
		t.Fatalf("calls %+v", r.ToolCalls)
	}
}

func TestOpenAINoToolUse(t *testing.T) {
	f, base := newFake(t, sse(`{"choices":[{"index":0,"delta":{"content":"ok"}}]}`))
	p := &OpenAI{BaseURL: base, Model: "m"}
	if _, err := p.Chat(ctx, Request{Messages: []Message{{Role: "user", Content: "x"}}, Tools: weatherTool, NoToolUse: true}, nil); err != nil {
		t.Fatal(err)
	}
	if f.bodies[0]["tool_choice"] != "none" || f.bodies[0]["tools"] == nil {
		t.Fatalf("body %v", f.bodies[0])
	}
}

func TestOpenAIAdaptsToServerQuirks(t *testing.T) {
	f, base := newFake(t,
		status(400, `{"error":{"message":"Unrecognized request argument supplied: stream_options"}}`),
		status(400, `{"error":{"message":"Unsupported parameter: 'max_tokens'. Use 'max_completion_tokens' instead."}}`),
		sse(`{"choices":[{"index":0,"delta":{"content":"ok"}}]}`),
		sse(`{"choices":[{"index":0,"delta":{"content":"again"}}]}`),
	)
	p := &OpenAI{BaseURL: base, Model: "m"}
	for _, want := range []string{"ok", "again"} {
		r, err := p.Chat(ctx, Request{Messages: []Message{{Role: "user", Content: "x"}}}, nil)
		if err != nil || r.Content != want {
			t.Fatalf("reply %+v, %v", r, err)
		}
	}
	last := f.bodies[3]
	if _, ok := last["stream_options"]; ok {
		t.Fatal("stream_options sent again")
	}
	if _, ok := last["max_tokens"]; ok || last["max_completion_tokens"] != 4096.0 {
		t.Fatalf("tokens %v", last)
	}
}

func TestOpenAIFallsBackToJSONProtocol(t *testing.T) {
	f, base := newFake(t,
		status(400, `{"error":{"message":"tools is not supported"}}`),
		sse(`{"choices":[{"index":0,"delta":{"content":"{\"tool_calls\":[{\"name\":\"gsc_query\",\"arguments\":{\"dimension\":\"page\"}}]}"}}]}`),
		sse(`{"choices":[{"index":0,"delta":{"content":"`+"```json\\n{\\\"answer\\\":\\\"**结论**：好\\\"}\\n```"+`"}}]}`),
	)
	p := &OpenAI{BaseURL: base, Model: "m"}
	msgs := []Message{{Role: "system", Content: "sys"}, {Role: "user", Content: "q"}}
	r, err := p.Chat(ctx, Request{Messages: msgs, Tools: weatherTool}, func(string) { t.Error("JSON replies must not stream") })
	if err != nil || len(r.ToolCalls) != 1 || r.ToolCalls[0].Function.Name != "gsc_query" || r.ToolCalls[0].Function.Arguments != `{"dimension":"page"}` {
		t.Fatalf("reply %+v, %v", r, err)
	}
	if p.NativeTools() {
		t.Fatal("still native")
	}
	sys := f.bodies[1]["messages"].([]any)[0].(map[string]any)["content"].(string)
	if !strings.Contains(sys, "Tool protocol") || !strings.Contains(sys, "gsc_query") || f.bodies[1]["tools"] != nil {
		t.Fatalf("json request %v", f.bodies[1])
	}
	msgs = append(msgs, Message{Role: "assistant", ToolCalls: r.ToolCalls}, Message{Role: "tool", ToolCallID: "call_1", Content: `{"rows":[]}`})
	var streamed string
	r, err = p.Chat(ctx, Request{Messages: msgs, Tools: weatherTool}, func(s string) { streamed += s })
	if err != nil || r.Content != "**结论**：好" || streamed != r.Content {
		t.Fatalf("answer %+v %q, %v", r, streamed, err)
	}
	flat := f.bodies[2]["messages"].([]any)
	if flat[2].(map[string]any)["role"] != "assistant" || !strings.HasPrefix(flat[3].(map[string]any)["content"].(string), "TOOL RESULT (call_1)") {
		t.Fatalf("flattened %v", flat)
	}
}

func TestOpenAIRefreshesTokenOnce(t *testing.T) {
	f, base := newFake(t,
		status(401, `{"error":{"code":"unauthorized","message":"Sign in required"}}`),
		sse(`{"choices":[{"index":0,"delta":{"content":"ok"}}]}`),
	)
	var refreshes []bool
	p := &OpenAI{URL: base + "/v1/chat/completions", Model: "m", Auth: func(_ context.Context, refresh bool) (string, error) {
		refreshes = append(refreshes, refresh)
		if refresh {
			return "new", nil
		}
		return "old", nil
	}}
	if _, err := p.Chat(ctx, Request{Messages: []Message{{Role: "user", Content: "x"}}}, nil); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(refreshes) != "[false true]" || f.auths[0] != "Bearer old" || f.auths[1] != "Bearer new" {
		t.Fatalf("refreshes %v auths %v", refreshes, f.auths)
	}
}

func TestOpenAICreditsExhausted(t *testing.T) {
	_, base := newFake(t, status(402, `{"error":{"code":"credits_exhausted","message":"This month's SiteMoment credits cannot cover this call","need":5,"used":199,"limit":200,"month":"2026-11"}}`))
	p := &OpenAI{URL: base + "/v1/chat/completions", Model: "m"}
	_, err := p.Chat(ctx, Request{Messages: []Message{{Role: "user", Content: "x"}}}, nil)
	var le *Error
	if !errors.As(err, &le) || le.Status != 402 || le.Code != CodeCreditsExhausted {
		t.Fatalf("err = %#v", err)
	}
}

func TestOpenAIRedactsKey(t *testing.T) {
	_, base := newFake(t, status(500, `{"error":{"message":"bad key sk-secret-123"}}`))
	p := &OpenAI{BaseURL: base, Key: "sk-secret-123", Model: "m"}
	_, err := p.Chat(ctx, Request{Messages: []Message{{Role: "user", Content: "x"}}}, nil)
	if err == nil || strings.Contains(err.Error(), "sk-secret-123") || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("err = %v", err)
	}
	p = &OpenAI{BaseURL: "http://127.0.0.1:1", Key: "sk-secret-123", Model: "m"}
	if _, err := p.Chat(ctx, Request{Messages: []Message{{Role: "user", Content: "x"}}}, nil); err == nil || strings.Contains(err.Error(), "127.0.0.1:1/v1") {
		t.Fatalf("transport err = %v", err)
	}
}

func TestOpenAIStreamError(t *testing.T) {
	_, base := newFake(t, sse(`{"choices":[{"index":0,"delta":{"content":"par"}}]}`, `{"error":{"message":"upstream overloaded"}}`))
	p := &OpenAI{BaseURL: base, Model: "m"}
	if _, err := p.Chat(ctx, Request{Messages: []Message{{Role: "user", Content: "x"}}}, nil); err == nil || !strings.Contains(err.Error(), "upstream overloaded") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenAICutStreamWithoutFinishReasonIsError(t *testing.T) {
	_, base := newFake(t, sseNoDone(`{"choices":[{"index":0,"delta":{"content":"部分"}}]}`))
	p := &OpenAI{BaseURL: base, Model: "m"}
	_, err := p.Chat(ctx, Request{Messages: []Message{{Role: "user", Content: "x"}}}, nil)
	if err == nil || !strings.Contains(err.Error(), "中断") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenAICutStreamWithFinishReasonIsComplete(t *testing.T) {
	_, base := newFake(t, sseNoDone(
		`{"choices":[{"index":0,"delta":{"content":"完整回答"}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	))
	p := &OpenAI{BaseURL: base, Model: "m"}
	r, err := p.Chat(ctx, Request{Messages: []Message{{Role: "user", Content: "x"}}}, nil)
	if err != nil || r.Content != "完整回答" || r.FinishReason != "stop" {
		t.Fatalf("reply %+v, %v", r, err)
	}
}

func TestOpenAIRejectsEndlessLine(t *testing.T) {
	_, base := newFake(t, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: ")
		// Never terminated with \n: a single line over the 1 MiB cap.
		io.WriteString(w, strings.Repeat("x", (1<<20)+10))
	})
	p := &OpenAI{BaseURL: base, Model: "m"}
	_, err := p.Chat(ctx, Request{Messages: []Message{{Role: "user", Content: "x"}}}, nil)
	if err == nil || !strings.Contains(err.Error(), "太长") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenAIRejectsHugeAccumulatedReply(t *testing.T) {
	_, base := newFake(t, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		chunk := strings.Repeat("a", 900<<10) // 900 KiB of content, under the 1 MiB line cap
		for i := 0; i < 10; i++ {             // 10 * 900 KiB ≈ 8.8 MiB, over the 8 MiB accumulated cap
			b, _ := json.Marshal(map[string]any{"choices": []map[string]any{{"index": 0, "delta": map[string]any{"content": chunk}}}})
			fmt.Fprintf(w, "data: %s\n\n", b)
		}
	})
	p := &OpenAI{BaseURL: base, Model: "m"}
	_, err := p.Chat(ctx, Request{Messages: []Message{{Role: "user", Content: "x"}}}, nil)
	if err == nil || !strings.Contains(err.Error(), "太长") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenAIRejectsHugeRawStream(t *testing.T) {
	_, base := newFake(t, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		pad := strings.Repeat("p", 60<<10) // padding keeps each line well under the 1 MiB line cap
		for i := 0; i < 300; i++ {         // 300 * ~60 KiB ≈ 17.6 MiB, over the 16 MiB whole-stream cap
			fmt.Fprintf(w, "data: {\"_pad\":\"%s\",\"choices\":[{\"index\":0,\"delta\":{}}]}\n\n", pad)
		}
	})
	p := &OpenAI{BaseURL: base, Model: "m"}
	_, err := p.Chat(ctx, Request{Messages: []Message{{Role: "user", Content: "x"}}}, nil)
	if err == nil || !strings.Contains(err.Error(), "太长") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenAIRedactsHostedToken(t *testing.T) {
	_, base := newFake(t, status(500, `{"error":{"message":"bad token acc-secret-xyz"}}`))
	p := &OpenAI{URL: base + "/v1/chat/completions", Model: "m", Auth: func(_ context.Context, _ bool) (string, error) { return "acc-secret-xyz", nil }}
	_, err := p.Chat(ctx, Request{Messages: []Message{{Role: "user", Content: "x"}}}, nil)
	if err == nil || strings.Contains(err.Error(), "acc-secret-xyz") || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenAIDecodesWholeReplyWithWrongContentType(t *testing.T) {
	_, base := newFake(t, func(w http.ResponseWriter) {
		// No Content-Type set: Go sniffs this as text/plain, but the server
		// ignored "stream" and answered with a whole completion anyway.
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`)
	})
	p := &OpenAI{BaseURL: base, Model: "m"}
	r, err := p.Chat(ctx, Request{Messages: []Message{{Role: "user", Content: "x"}}}, nil)
	if err != nil || r.Content != "ok" || r.FinishReason != "stop" || r.Usage != (Usage{5, 2}) {
		t.Fatalf("reply %+v, %v", r, err)
	}
}

func TestCompletionsURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://api.openai.com/v1":                 "https://api.openai.com/v1/chat/completions",
		"https://x.test/":                           "https://x.test/v1/chat/completions",
		"https://x.test/openai/v4/chat/completions": "https://x.test/openai/v4/chat/completions",
		"https://openrouter.ai/api/v1/":             "https://openrouter.ai/api/v1/chat/completions",
	} {
		if got := completionsURL(in); got != want {
			t.Errorf("%s → %s, want %s", in, got, want)
		}
	}
}
