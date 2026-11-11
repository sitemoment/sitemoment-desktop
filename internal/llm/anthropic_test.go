// SPDX-License-Identifier: AGPL-3.0-or-later
package llm

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// anthropicSSE writes Messages API stream events (event name from "type").
func anthropicSSE(events ...string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, e := range events {
			var v struct {
				Type string `json:"type"`
			}
			json.Unmarshal([]byte(e), &v)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", v.Type, e)
		}
	}
}

// anthropicFake serves POST /v1/messages with the next step and records bodies.
func anthropicFake(t *testing.T, steps ...func(w http.ResponseWriter)) (*[]map[string]any, *[]http.Header, string) {
	var bodies []map[string]any
	var headers []http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path %s", r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		var b map[string]any
		json.Unmarshal(raw, &b)
		bodies = append(bodies, b)
		headers = append(headers, r.Header.Clone())
		if len(bodies) > len(steps) {
			t.Errorf("unexpected request %d", len(bodies))
			w.WriteHeader(500)
			return
		}
		steps[len(bodies)-1](w)
	}))
	t.Cleanup(srv.Close)
	return &bodies, &headers, srv.URL
}

const msgStart = `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":100,"output_tokens":1,"cache_read_input_tokens":20}}}`

func toolTurn() func(w http.ResponseWriter) {
	return anthropicSSE(
		msgStart,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-123"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"先查 "}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"GSC。"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_1","name":"gsc_query","input":{}}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"dimension\":"}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"\"query\"}"}}`,
		`{"type":"content_block_stop","index":2}`,
		`{"type":"content_block_start","index":3,"content_block":{"type":"tool_use","id":"toolu_2","name":"fetch_page","input":{}}}`,
		`{"type":"content_block_delta","index":3,"delta":{"type":"input_json_delta","partial_json":"{\"url\":\"https://a.com\"}"}}`,
		`{"type":"content_block_stop","index":3}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":30}}`,
		`{"type":"message_stop"}`,
	)
}

func answerTurn(stop string) func(w http.ResponseWriter) {
	return anthropicSSE(
		msgStart,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"**结论**：可以做。"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"`+stop+`","stop_sequence":null},"usage":{"output_tokens":12}}`,
		`{"type":"message_stop"}`,
	)
}

func TestAnthropicToolLoop(t *testing.T) {
	bodies, headers, base := anthropicFake(t, toolTurn(), answerTurn("end_turn"))
	p := &Anthropic{Key: "sk-ant-test", BaseURL: base}
	msgs := []Message{{Role: "system", Content: "你是 SEO 顾问"}, {Role: "user", Content: "这个词怎么样？"}}
	var deltas []string
	r, err := p.Chat(ctx, Request{Messages: msgs, Tools: weatherTool}, func(s string) { deltas = append(deltas, s) })
	if err != nil {
		t.Fatal(err)
	}
	if r.Content != "先查 GSC。" || strings.Join(deltas, "|") != "先查 |GSC。" || r.FinishReason != "tool_calls" || r.Usage != (Usage{120, 30}) {
		t.Fatalf("reply %+v deltas %q", r, deltas)
	}
	if len(r.ToolCalls) != 2 || r.ToolCalls[0].ID != "toolu_1" || r.ToolCalls[0].Function.Name != "gsc_query" ||
		r.ToolCalls[0].Function.Arguments != `{"dimension":"query"}` || r.ToolCalls[1].Function.Arguments != `{"url":"https://a.com"}` {
		t.Fatalf("calls %+v", r.ToolCalls)
	}
	if r.NativeOf != "anthropic" || !strings.Contains(string(r.Native), "sig-123") {
		t.Fatalf("native %s", r.Native)
	}
	first := (*bodies)[0]
	h := (*headers)[0]
	if h.Get("X-Api-Key") != "sk-ant-test" || h.Get("Anthropic-Version") == "" || first["model"] != AnthropicDefaultModel || first["max_tokens"] != 16000.0 || first["stream"] != true {
		t.Fatalf("request %v headers %v", first, h)
	}
	if sys := first["system"].([]any)[0].(map[string]any)["text"]; sys != "你是 SEO 顾问" {
		t.Fatalf("system %v", first["system"])
	}
	tool := first["tools"].([]any)[0].(map[string]any)
	if tool["name"] != "gsc_query" || tool["eager_input_streaming"] != true || tool["input_schema"].(map[string]any)["type"] != "object" {
		t.Fatalf("tool %v", tool)
	}

	// Next step: the assistant turn goes back as it was (thinking block and
	// its signature included) and both results share one user message,
	// followed by the system's note.
	msgs = append(msgs, Message{Role: "assistant", Content: r.Content, ToolCalls: r.ToolCalls, Native: r.Native, NativeOf: r.NativeOf},
		Message{Role: "tool", ToolCallID: "toolu_1", Content: `{"rows":[]}`},
		Message{Role: "tool", ToolCallID: "toolu_2", Content: `{"status":200}`},
		Message{Role: "user", Content: "请直接作答"})
	r, err = p.Chat(ctx, Request{Messages: msgs, Tools: weatherTool, NoToolUse: true}, nil)
	if err != nil || r.Content != "**结论**：可以做。" || r.FinishReason != "stop" {
		t.Fatalf("answer %+v, %v", r, err)
	}
	second := (*bodies)[1]
	if tc := second["tool_choice"].(map[string]any); tc["type"] != "none" {
		t.Fatalf("tool_choice %v", tc)
	}
	ms := second["messages"].([]any)
	if len(ms) != 3 {
		t.Fatalf("messages %v", ms)
	}
	asst := ms[1].(map[string]any)["content"].([]any)
	if b := asst[0].(map[string]any); b["type"] != "thinking" || b["signature"] != "sig-123" {
		t.Fatalf("assistant blocks %v", asst)
	}
	if b := asst[2].(map[string]any); b["type"] != "tool_use" || b["input"].(map[string]any)["dimension"] != "query" {
		t.Fatalf("tool_use block %v", b)
	}
	user := ms[2].(map[string]any)
	blocks := user["content"].([]any)
	if user["role"] != "user" || len(blocks) != 3 || blocks[0].(map[string]any)["tool_use_id"] != "toolu_1" ||
		blocks[1].(map[string]any)["tool_use_id"] != "toolu_2" || blocks[2].(map[string]any)["type"] != "text" {
		t.Fatalf("user message %v", user)
	}
}

func TestAnthropicHistoryWithoutNative(t *testing.T) {
	bodies, _, base := anthropicFake(t, answerTurn("end_turn"))
	p := &Anthropic{Key: "k", BaseURL: base, Model: "claude-sonnet-5", MaxTokens: 2000}
	msgs := []Message{
		{Role: "user", Content: "q"},
		// A turn answered by another provider: rebuilt from text + calls.
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Function: FunctionCall{Name: "gsc_query", Arguments: "not json"}}}},
		{Role: "tool", ToolCallID: "call_1", Content: "{}"},
	}
	if _, err := p.Chat(ctx, Request{Messages: msgs, Tools: weatherTool}, nil); err != nil {
		t.Fatal(err)
	}
	b := (*bodies)[0]
	if b["model"] != "claude-sonnet-5" || b["max_tokens"] != 2000.0 {
		t.Fatalf("body %v", b)
	}
	use := b["messages"].([]any)[1].(map[string]any)["content"].([]any)[0].(map[string]any)
	if use["type"] != "tool_use" || use["id"] != "call_1" || len(use["input"].(map[string]any)) != 0 {
		t.Fatalf("tool_use %v", use)
	}
}

func TestAnthropicRefusalAndErrors(t *testing.T) {
	_, _, base := anthropicFake(t, answerTurn("refusal"),
		func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(401)
			w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key sk-ant-bad"}}`))
		})
	p := &Anthropic{Key: "sk-ant-bad", BaseURL: base}
	req := Request{Messages: []Message{{Role: "user", Content: "q"}}}
	var le *Error
	if _, err := p.Chat(ctx, req, nil); !errors.As(err, &le) || le.Code != "refusal" {
		t.Fatalf("refusal err = %v", err)
	}
	_, err := p.Chat(ctx, req, nil)
	if !errors.As(err, &le) || le.Status != 401 || le.Code != "authentication_error" || strings.Contains(err.Error(), "sk-ant-bad") {
		t.Fatalf("auth err = %v", err)
	}
}
