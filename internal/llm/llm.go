// SPDX-License-Identifier: AGPL-3.0-or-later

// Package llm talks to chat models: SiteMoment's hosted model (an
// OpenAI-compatible proxy), any OpenAI-compatible server with the user's own
// key, and Anthropic's Messages API. Every provider streams the answer text
// and returns tool calls in the OpenAI shape, which is what the agent stores.
package llm

import (
	"context"
	"encoding/json"
	"fmt"
)

// Message is one chat message. Assistant messages may carry ToolCalls; tool
// messages answer one call by ToolCallID.
type Message struct {
	Role       string     `json:"role"` // system | user | assistant | tool
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	// Native is a provider's own encoding of an assistant turn, sent back
	// unchanged by the same provider (Anthropic needs its thinking blocks
	// returned as they were); NativeOf names that provider ("anthropic").
	// Other providers ignore it.
	Native   json.RawMessage `json:"-"`
	NativeOf string          `json:"-"`
}

// ToolCall is one function call requested by the model.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall names the function and its JSON-encoded arguments.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Tool is a function the model may call.
type Tool struct {
	Name        string
	Description string
	// Parameters is a JSON Schema object.
	Parameters json.RawMessage
}

// Request is one model call.
type Request struct {
	Messages []Message
	Tools    []Tool
	// NoToolUse keeps Tools defined (the history may contain tool calls) but
	// asks for a plain answer.
	NoToolUse bool
}

// Usage is what one call consumed.
type Usage struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
}

// Reply is the model's answer: text, tool calls, or both.
type Reply struct {
	Content      string
	ToolCalls    []ToolCall
	FinishReason string
	Usage        Usage
	Native       json.RawMessage
	NativeOf     string
}

// Model is one configured chat model. Safe for concurrent use.
type Model interface {
	// Chat sends req and passes answer text to onText (which may be nil) as
	// it streams in. Text that turns out to precede tool calls is streamed
	// too; the caller decides how to show it.
	Chat(ctx context.Context, req Request, onText func(string)) (*Reply, error)
	// Label names the model for the UI, e.g. "SiteMoment 托管 · gpt-5.6-sol".
	Label() string
}

// Error is a failed call. Status is the HTTP status (0 for transport
// errors); Code is the server's error code when it sent one (the hosted
// model answers "credits_exhausted" with 402 when the month's credits are
// used up).
type Error struct {
	Status int
	Code   string
	Msg    string
}

func (e *Error) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("llm: %s (%s)", e.Msg, e.Code)
	}
	return "llm: " + e.Msg
}

// CodeCreditsExhausted is Error.Code when SiteMoment's monthly credits are used up.
const CodeCreditsExhausted = "credits_exhausted"
