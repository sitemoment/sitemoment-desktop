// SPDX-License-Identifier: AGPL-3.0-or-later
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
)

// defaultHTTP has no overall timeout: an answer streams for as long as it
// takes, and the caller bounds a call with its context.
var defaultHTTP = &http.Client{}

// OpenAI is an OpenAI-compatible chat completions server: SiteMoment's
// hosted model (URL + Auth) or one the user brings (BaseURL + Key).
type OpenAI struct {
	// URL is the full chat/completions endpoint; empty means BaseURL's.
	URL     string
	BaseURL string
	Key     string
	// Auth returns the bearer token (hosted model); it overrides Key. After
	// a 401 it is called once more with refresh set.
	Auth      func(ctx context.Context, refresh bool) (string, error)
	Model     string
	MaxTokens int // default 4096
	HTTP      *http.Client
	Name      string

	// jsonProtocol is set once the server has rejected native tools.
	jsonProtocol atomic.Bool
	// noStreamOptions is set once the server has rejected stream_options.
	noStreamOptions atomic.Bool
	// completionTokens is set once the server asked for max_completion_tokens.
	completionTokens atomic.Bool
}

// Label names the model for the UI.
func (p *OpenAI) Label() string {
	if p.Name != "" {
		return p.Name
	}
	return p.Model
}

// NativeTools reports whether the server accepted the `tools` field so far.
func (p *OpenAI) NativeTools() bool { return !p.jsonProtocol.Load() }

func completionsURL(base string) string {
	b := strings.TrimRight(base, "/")
	switch {
	case strings.HasSuffix(b, "/chat/completions"):
		return b
	case regexp.MustCompile(`/v\d+$`).MatchString(b):
		return b + "/chat/completions"
	default:
		return b + "/v1/chat/completions"
	}
}

func (p *OpenAI) endpoint() string {
	if p.URL != "" {
		return p.URL
	}
	return completionsURL(p.BaseURL)
}

var toolsRejected = regexp.MustCompile(`(?i)tool|function`)

// Chat streams one completion. A server that rejects native tools is
// switched (once, for the life of p) to a JSON protocol described in the
// system prompt.
func (p *OpenAI) Chat(ctx context.Context, req Request, onText func(string)) (*Reply, error) {
	if p.jsonProtocol.Load() && len(req.Tools) > 0 {
		return p.viaJSON(ctx, req, onText)
	}
	r, err := p.native(ctx, req, onText)
	var le *Error
	if err == nil || len(req.Tools) == 0 || !errors.As(err, &le) || le.Status != http.StatusBadRequest || !toolsRejected.MatchString(le.Msg) {
		return r, err
	}
	p.jsonProtocol.Store(true)
	return p.viaJSON(ctx, req, onText)
}

type wireTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

func (p *OpenAI) native(ctx context.Context, req Request, onText func(string)) (*Reply, error) {
	body := map[string]any{"model": p.Model, "messages": req.Messages}
	if len(req.Tools) > 0 {
		wt := make([]wireTool, len(req.Tools))
		for i, t := range req.Tools {
			wt[i].Type = "function"
			wt[i].Function.Name, wt[i].Function.Description, wt[i].Function.Parameters = t.Name, t.Description, t.Parameters
		}
		body["tools"], body["tool_choice"] = wt, "auto"
		if req.NoToolUse {
			body["tool_choice"] = "none"
		}
	}
	return p.stream(ctx, body, onText)
}

func (p *OpenAI) maxTokens() int {
	if p.MaxTokens > 0 {
		return p.MaxTokens
	}
	return 4096
}

func (p *OpenAI) token(ctx context.Context, refresh bool) (string, error) {
	if p.Auth != nil {
		return p.Auth(ctx, refresh)
	}
	return p.Key, nil
}

// stream posts body with stream=true and reads the server-sent events. It
// adapts once to servers that reject stream_options or max_tokens, and
// refreshes the bearer token once after a 401.
func (p *OpenAI) stream(ctx context.Context, body map[string]any, onText func(string)) (*Reply, error) {
	refreshed := false
	for attempt := 0; attempt < 4; attempt++ {
		body["stream"] = true
		delete(body, "stream_options")
		if !p.noStreamOptions.Load() {
			body["stream_options"] = map[string]bool{"include_usage": true}
		}
		delete(body, "max_tokens")
		delete(body, "max_completion_tokens")
		if p.completionTokens.Load() {
			body["max_completion_tokens"] = p.maxTokens()
		} else {
			body["max_tokens"] = p.maxTokens()
		}
		tok, err := p.token(ctx, refreshed)
		if err != nil {
			return nil, err
		}
		res, err := p.post(ctx, body, tok)
		if err != nil {
			return nil, err
		}
		if res.StatusCode >= 200 && res.StatusCode < 300 {
			defer res.Body.Close()
			if strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") {
				return p.decodeWhole(res)
			}
			return p.readEvents(ctx, res.Body, onText)
		}
		e := p.errorOf(res)
		switch {
		case res.StatusCode == http.StatusUnauthorized && p.Auth != nil && !refreshed:
			refreshed = true
			continue
		case res.StatusCode == http.StatusBadRequest && !p.noStreamOptions.Load() && strings.Contains(e.Msg, "stream_options"):
			p.noStreamOptions.Store(true)
			continue
		case res.StatusCode == http.StatusBadRequest && !p.completionTokens.Load() && strings.Contains(e.Msg, "max_completion_tokens"):
			p.completionTokens.Store(true)
			continue
		}
		return nil, e
	}
	return nil, &Error{Msg: "too many retries"}
}

func (p *OpenAI) post(ctx context.Context, body map[string]any, token string) (*http.Response, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, &Error{Msg: err.Error()}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint(), bytes.NewReader(raw))
	if err != nil {
		return nil, &Error{Msg: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	h := p.HTTP
	if h == nil {
		h = defaultHTTP
	}
	res, err := h.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // drop the URL that *url.Error prepends
		}
		return nil, &Error{Msg: "request failed: " + p.redact(err.Error())}
	}
	return res, nil
}

// errorOf reads an error response: {"error":{"code","message"}} (OpenAI and
// SiteMoment) or {"error":"..."}; anything else is shown truncated.
func (p *OpenAI) errorOf(res *http.Response) *Error {
	defer res.Body.Close()
	text, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	var obj struct {
		Error json.RawMessage `json:"error"`
	}
	msg, code := "", ""
	if json.Unmarshal(text, &obj) == nil && len(obj.Error) > 0 {
		var e struct {
			Code    any    `json:"code"`
			Message string `json:"message"`
		}
		if json.Unmarshal(obj.Error, &e) == nil {
			msg = e.Message
			if s, ok := e.Code.(string); ok {
				code = s
			}
		} else {
			json.Unmarshal(obj.Error, &msg)
		}
	}
	if msg == "" {
		msg = truncate(string(text), 300)
	}
	return &Error{Status: res.StatusCode, Code: code, Msg: fmt.Sprintf("HTTP %d: %s", res.StatusCode, p.redact(truncate(msg, 300)))}
}

type wireDelta struct {
	Index    *int   `json:"index"`
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type wireChunk struct {
	Choices []struct {
		Delta struct {
			Content   *string     `json:"content"`
			ToolCalls []wireDelta `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// calls accumulates streamed tool calls. OpenAI keys each delta by index;
// some compatible servers repeat index 0 for every call or leave it out, so
// a new id (or, without an index, a new name) starts a new call.
type calls struct {
	list    []*ToolCall
	byIndex map[int]int
}

func (c *calls) add(d wireDelta) {
	pos := -1
	if d.Index != nil {
		if p, ok := c.byIndex[*d.Index]; ok && (d.ID == "" || d.ID == c.list[p].ID) {
			pos = p
		}
	} else if n := len(c.list); n > 0 && d.ID == "" && d.Function.Name == "" {
		pos = n - 1
	}
	if pos < 0 {
		c.list = append(c.list, &ToolCall{ID: d.ID, Type: "function"})
		pos = len(c.list) - 1
		if d.Index != nil {
			if c.byIndex == nil {
				c.byIndex = map[int]int{}
			}
			c.byIndex[*d.Index] = pos
		}
	}
	tc := c.list[pos]
	if tc.Function.Name == "" {
		tc.Function.Name = d.Function.Name
	}
	tc.Function.Arguments += d.Function.Arguments
}

// readEvents reads `data: {chunk}` lines until `data: [DONE]` or EOF.
func (p *OpenAI) readEvents(ctx context.Context, body io.Reader, onText func(string)) (*Reply, error) {
	rd := bufio.NewReaderSize(body, 64<<10)
	var text strings.Builder
	var acc calls
	r := &Reply{}
	for {
		line, err := rd.ReadString('\n')
		if data, ok := strings.CutPrefix(strings.TrimSpace(line), "data:"); ok {
			data = strings.TrimSpace(data)
			if data == "[DONE]" {
				break
			}
			var ch wireChunk
			if json.Unmarshal([]byte(data), &ch) == nil {
				if ch.Error != nil {
					return nil, &Error{Msg: "stream error: " + p.redact(truncate(ch.Error.Message, 300))}
				}
				if ch.Usage != nil {
					r.Usage = Usage{InputTokens: ch.Usage.PromptTokens, OutputTokens: ch.Usage.CompletionTokens}
				}
				if len(ch.Choices) > 0 {
					c := ch.Choices[0]
					if c.Delta.Content != nil && *c.Delta.Content != "" {
						text.WriteString(*c.Delta.Content)
						if onText != nil {
							onText(*c.Delta.Content)
						}
					}
					for _, d := range c.Delta.ToolCalls {
						acc.add(d)
					}
					if c.FinishReason != nil && *c.FinishReason != "" {
						r.FinishReason = *c.FinishReason
					}
				}
			}
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, &Error{Msg: "stream interrupted: " + err.Error()}
		}
	}
	r.Content = text.String()
	for _, tc := range acc.list {
		r.ToolCalls = append(r.ToolCalls, *tc)
	}
	return finish(r)
}

// decodeWhole reads a non-streamed completion (a server that ignored stream).
func (p *OpenAI) decodeWhole(res *http.Response) (*Reply, error) {
	text, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	var out struct {
		Choices []struct {
			Message struct {
				Content   *string    `json:"content"`
				ToolCalls []ToolCall `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(text, &out); err != nil || len(out.Choices) == 0 {
		return nil, &Error{Status: res.StatusCode, Msg: "response has no choices"}
	}
	ch := out.Choices[0]
	r := &Reply{ToolCalls: ch.Message.ToolCalls, FinishReason: ch.FinishReason,
		Usage: Usage{InputTokens: out.Usage.PromptTokens, OutputTokens: out.Usage.CompletionTokens}}
	if ch.Message.Content != nil {
		r.Content = *ch.Message.Content
	}
	return finish(r)
}

// finish fills missing call ids and empty arguments, and rejects an empty reply.
func finish(r *Reply) (*Reply, error) {
	for i := range r.ToolCalls {
		if r.ToolCalls[i].ID == "" {
			r.ToolCalls[i].ID = fmt.Sprintf("call_%d", i+1)
		}
		r.ToolCalls[i].Type = "function"
		if strings.TrimSpace(r.ToolCalls[i].Function.Arguments) == "" {
			r.ToolCalls[i].Function.Arguments = "{}"
		}
	}
	if r.Content == "" && len(r.ToolCalls) == 0 {
		return nil, &Error{Msg: "empty reply (finish_reason " + r.FinishReason + ")"}
	}
	return r, nil
}

// viaJSON describes the tools in the system prompt, flattens tool traffic into
// plain messages and parses {"tool_calls":[…]} or {"answer":"…"} replies. The
// reply is JSON, so nothing streams; a final answer goes to onText at once.
func (p *OpenAI) viaJSON(ctx context.Context, req Request, onText func(string)) (*Reply, error) {
	var b strings.Builder
	if req.NoToolUse {
		b.WriteString("\n\n## Tool protocol\nDo not call any tool now. Reply with ONLY:\n" + `{"answer":"<your final answer in Markdown>"}` + "\n")
	} else {
		b.WriteString("\n\n## Tool protocol\nYou cannot call functions natively. To call tools, reply with ONLY a JSON object:\n")
		b.WriteString(`{"tool_calls":[{"name":"<tool>","arguments":{...}}]}` + "\nWhen you are done, reply with ONLY:\n" + `{"answer":"<your final answer in Markdown>"}` + "\nTools:\n")
		for _, t := range req.Tools {
			fmt.Fprintf(&b, "- %s: %s Parameters schema: %s\n", t.Name, t.Description, string(t.Parameters))
		}
	}
	flat := make([]Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		switch {
		case m.Role == "system":
			flat = append(flat, Message{Role: "system", Content: m.Content + b.String()})
		case m.Role == "assistant" && len(m.ToolCalls) > 0:
			list := []map[string]any{}
			for _, tc := range m.ToolCalls {
				var args any
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
				list = append(list, map[string]any{"name": tc.Function.Name, "arguments": args})
			}
			j, _ := json.Marshal(map[string]any{"tool_calls": list})
			flat = append(flat, Message{Role: "assistant", Content: string(j)})
		case m.Role == "tool":
			flat = append(flat, Message{Role: "user", Content: "TOOL RESULT (" + m.ToolCallID + "):\n" + m.Content})
		default:
			flat = append(flat, Message{Role: m.Role, Content: m.Content})
		}
	}
	r, err := p.stream(ctx, map[string]any{"model": p.Model, "messages": flat}, nil)
	if err != nil {
		return nil, err
	}
	out := parseJSONReply(r.Content)
	out.Usage = r.Usage
	if req.NoToolUse {
		out.ToolCalls = nil
	}
	if len(out.ToolCalls) == 0 && onText != nil && out.Content != "" {
		onText(out.Content)
	}
	return out, nil
}

// parseJSONReply reads a JSON-protocol reply; anything unparseable is taken as the final answer.
func parseJSONReply(text string) *Reply {
	obj, ok := extractJSON(text)
	if !ok {
		return &Reply{Content: text, FinishReason: "stop"}
	}
	var v struct {
		ToolCalls []struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"tool_calls"`
		Answer *string `json:"answer"`
	}
	if err := json.Unmarshal([]byte(obj), &v); err != nil {
		return &Reply{Content: text, FinishReason: "stop"}
	}
	if len(v.ToolCalls) > 0 {
		r := &Reply{FinishReason: "tool_calls"}
		for i, tc := range v.ToolCalls {
			args := string(tc.Arguments)
			if args == "" || args == "null" {
				args = "{}"
			}
			r.ToolCalls = append(r.ToolCalls, ToolCall{ID: fmt.Sprintf("call_%d", i+1), Type: "function", Function: FunctionCall{Name: tc.Name, Arguments: args}})
		}
		return r
	}
	if v.Answer != nil {
		return &Reply{Content: *v.Answer, FinishReason: "stop"}
	}
	return &Reply{Content: text, FinishReason: "stop"}
}

// extractJSON finds the whole text, a ```json fence, or the first balanced {...} that parses.
func extractJSON(text string) (string, bool) {
	t := strings.TrimSpace(text)
	if json.Valid([]byte(t)) && strings.HasPrefix(t, "{") {
		return t, true
	}
	if m := regexp.MustCompile("(?s)```(?:json)?\\s*\\n(.*?)\\n```").FindStringSubmatch(t); m != nil && json.Valid([]byte(m[1])) {
		return m[1], true
	}
	start := strings.IndexByte(t, '{')
	if start < 0 {
		return "", false
	}
	depth, inStr, esc := 0, false, false
	for i := start; i < len(t); i++ {
		ch := t[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case ch == '\\':
				esc = true
			case ch == '"':
				inStr = false
			}
			continue
		}
		switch ch {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				if s := t[start : i+1]; json.Valid([]byte(s)) {
					return s, true
				}
				return "", false
			}
		}
	}
	return "", false
}

func (p *OpenAI) redact(s string) string {
	if p.Key == "" {
		return s
	}
	return strings.ReplaceAll(s, p.Key, "***")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut]
}
