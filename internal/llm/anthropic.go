// SPDX-License-Identifier: AGPL-3.0-or-later
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// AnthropicDefaultModel is used when the user has not picked one.
const AnthropicDefaultModel = "claude-opus-5"

// Anthropic is Anthropic's Messages API with the user's own key, through the
// official Go SDK. Every call streams.
type Anthropic struct {
	Key       string
	Model     string // default AnthropicDefaultModel
	BaseURL   string // tests only
	MaxTokens int    // default 16000
	HTTP      *http.Client
	Name      string
}

// Label names the model for the UI.
func (p *Anthropic) Label() string {
	if p.Name != "" {
		return p.Name
	}
	return p.model()
}

func (p *Anthropic) model() string {
	if p.Model != "" {
		return p.Model
	}
	return AnthropicDefaultModel
}

func (p *Anthropic) client() anthropic.Client {
	h := p.HTTP
	if h == nil {
		h = defaultHTTP
	}
	// Only the key the user entered: no ANTHROPIC_* environment variables or
	// CLI profiles from this machine.
	opts := []option.RequestOption{option.WithoutEnvironmentDefaults(), option.WithAPIKey(p.Key), option.WithHTTPClient(h), option.WithMaxRetries(2)}
	if p.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(p.BaseURL))
	}
	return anthropic.NewClient(opts...)
}

// Chat streams one message. The whole assistant message comes back as
// Native so that the next request can send it unchanged (thinking blocks
// must be returned as they were).
func (p *Anthropic) Chat(ctx context.Context, req Request, onText func(string)) (*Reply, error) {
	params, err := p.params(req)
	if err != nil {
		return nil, err
	}
	cl := p.client()
	stream := cl.Messages.NewStreaming(ctx, params)
	defer stream.Close()
	msg := anthropic.Message{}
	for stream.Next() {
		ev := stream.Current()
		if err := msg.Accumulate(ev); err != nil {
			return nil, &Error{Msg: "stream: " + err.Error()}
		}
		if ev.Type == "content_block_delta" && ev.Delta.Type == "text_delta" && onText != nil && ev.Delta.Text != "" {
			onText(ev.Delta.Text)
		}
	}
	if err := stream.Err(); err != nil {
		return nil, p.wrap(ctx, err)
	}
	r := &Reply{
		Usage: Usage{
			InputTokens:  int(msg.Usage.InputTokens + msg.Usage.CacheCreationInputTokens + msg.Usage.CacheReadInputTokens),
			OutputTokens: int(msg.Usage.OutputTokens),
		},
		Native: json.RawMessage(msg.RawJSON()), NativeOf: "anthropic",
	}
	var text strings.Builder
	for _, b := range msg.Content {
		switch b.Type {
		case "text":
			text.WriteString(b.Text)
		case "tool_use":
			args := string(b.Input)
			if !json.Valid(b.Input) || strings.TrimSpace(args) == "" {
				args = "{}"
			}
			r.ToolCalls = append(r.ToolCalls, ToolCall{ID: b.ID, Type: "function", Function: FunctionCall{Name: b.Name, Arguments: args}})
		}
	}
	r.Content = text.String()
	switch msg.StopReason {
	case anthropic.StopReasonRefusal:
		return nil, &Error{Status: http.StatusOK, Code: "refusal", Msg: "模型拒绝回答这个请求"}
	case anthropic.StopReasonMaxTokens:
		r.FinishReason = "length"
		if len(r.ToolCalls) > 0 {
			return nil, &Error{Status: http.StatusOK, Code: "max_tokens", Msg: "模型输出达到长度上限，工具调用不完整"}
		}
	case anthropic.StopReasonToolUse:
		r.FinishReason = "tool_calls"
	default:
		r.FinishReason = "stop"
	}
	if r.Content == "" && len(r.ToolCalls) == 0 {
		return nil, &Error{Msg: "empty reply (stop_reason " + string(msg.StopReason) + ")"}
	}
	return r, nil
}

func (p *Anthropic) params(req Request) (anthropic.MessageNewParams, error) {
	maxTokens := p.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 16000
	}
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(p.model()),
		MaxTokens: int64(maxTokens),
		// Cache the stable prefix (tools, system, earlier turns): an agent
		// turn resends the whole conversation on every step.
		CacheControl: anthropic.NewCacheControlEphemeralParam(),
	}
	var system []string
	var msgs []anthropic.MessageParam
	// Consecutive user content (tool results, then maybe a note) goes into
	// one user message, with every tool result of a step together.
	addUser := func(b anthropic.ContentBlockParamUnion) {
		if n := len(msgs); n > 0 && msgs[n-1].Role == anthropic.MessageParamRoleUser {
			msgs[n-1].Content = append(msgs[n-1].Content, b)
			return
		}
		msgs = append(msgs, anthropic.NewUserMessage(b))
	}
	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			system = append(system, m.Content)
		case "user":
			if strings.TrimSpace(m.Content) != "" {
				addUser(anthropic.NewTextBlock(m.Content))
			}
		case "tool":
			addUser(anthropic.NewToolResultBlock(m.ToolCallID, m.Content, false))
		case "assistant":
			if m.NativeOf == "anthropic" && len(m.Native) > 0 {
				var am anthropic.Message
				if err := json.Unmarshal(m.Native, &am); err == nil && len(am.Content) > 0 {
					msgs = append(msgs, am.ToParam())
					continue
				}
			}
			var blocks []anthropic.ContentBlockParamUnion
			if strings.TrimSpace(m.Content) != "" {
				blocks = append(blocks, anthropic.NewTextBlock(m.Content))
			}
			for _, tc := range m.ToolCalls {
				input := json.RawMessage(tc.Function.Arguments)
				if !json.Valid(input) {
					input = json.RawMessage(`{}`)
				}
				blocks = append(blocks, anthropic.NewToolUseBlock(tc.ID, input, tc.Function.Name))
			}
			if len(blocks) > 0 {
				msgs = append(msgs, anthropic.NewAssistantMessage(blocks...))
			}
		default:
			return params, fmt.Errorf("llm: unknown message role %q", m.Role)
		}
	}
	if len(system) > 0 {
		params.System = []anthropic.TextBlockParam{{Text: strings.Join(system, "\n\n")}}
	}
	params.Messages = msgs
	for _, t := range req.Tools {
		schema, err := inputSchema(t.Parameters)
		if err != nil {
			return params, fmt.Errorf("llm: tool %s: %w", t.Name, err)
		}
		tp := anthropic.ToolParam{Name: t.Name, Description: anthropic.String(t.Description), InputSchema: schema,
			EagerInputStreaming: anthropic.Bool(true)}
		params.Tools = append(params.Tools, anthropic.ToolUnionParam{OfTool: &tp})
	}
	if len(req.Tools) > 0 && req.NoToolUse {
		params.ToolChoice = anthropic.ToolChoiceUnionParam{OfNone: &anthropic.ToolChoiceNoneParam{}}
	}
	return params, nil
}

// inputSchema turns a JSON Schema object into the SDK's input schema.
func inputSchema(raw json.RawMessage) (anthropic.ToolInputSchemaParam, error) {
	var s anthropic.ToolInputSchemaParam
	if len(raw) == 0 {
		return s, nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return s, err
	}
	s.Properties = m["properties"]
	if req, ok := m["required"].([]any); ok {
		for _, r := range req {
			if name, ok := r.(string); ok {
				s.Required = append(s.Required, name)
			}
		}
	}
	for k, v := range m {
		if k != "type" && k != "properties" && k != "required" {
			if s.ExtraFields == nil {
				s.ExtraFields = map[string]any{}
			}
			s.ExtraFields[k] = v
		}
	}
	return s, nil
}

// wrap turns an SDK error into *Error with the API's own message (the SDK's
// error string includes the request URL and full body).
func (p *Anthropic) wrap(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var ae *anthropic.Error
	if !errors.As(err, &ae) {
		return &Error{Msg: "request failed: " + err.Error()}
	}
	var body struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	json.Unmarshal([]byte(ae.RawJSON()), &body)
	msg := body.Error.Message
	if msg == "" {
		msg = http.StatusText(ae.StatusCode)
	}
	if p.Key != "" {
		msg = strings.ReplaceAll(msg, p.Key, "***")
	}
	return &Error{Status: ae.StatusCode, Code: body.Error.Type, Msg: fmt.Sprintf("HTTP %d: %s", ae.StatusCode, truncate(msg, 300))}
}
