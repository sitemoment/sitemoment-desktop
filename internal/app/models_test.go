// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sitemoment/sitemoment-desktop/internal/cloud"
	"github.com/sitemoment/sitemoment-desktop/internal/llm"
	"github.com/sitemoment/sitemoment-desktop/internal/secret"
)

// llmServer is SiteMoment (token, models, hosted chat) and an OpenAI-compatible server in one.
func llmServer(t *testing.T) (*httptest.Server, *[]map[string]any) {
	var chats []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/desktop/token":
			w.Write([]byte(`{"access_token":"acc","refresh_token":"r2","expires_in":3600}`))
		case "/v1/desktop/llm/models":
			w.Write([]byte(`{"object":"list","data":[{"id":"gpt-5.6-sol"}]}`))
		case "/v1/desktop/llm/chat/completions", "/byok/v1/chat/completions":
			raw, _ := io.ReadAll(r.Body)
			var b map[string]any
			json.Unmarshal(raw, &b)
			b["_auth"] = r.Header.Get("Authorization")
			b["_path"] = r.URL.Path
			chats = append(chats, b)
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"收到\"}}]}\n\ndata: [DONE]\n\n")
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &chats
}

func newModels(t *testing.T, srv *httptest.Server) (*ModelService, *secret.Memory) {
	sec := secret.NewMemory()
	m := &Models{Store: openStore(t), Secrets: sec, Cloud: &cloud.Client{Base: srv.URL, HTTP: srv.Client(), Secrets: sec}, HTTP: srv.Client()}
	return &ModelService{Models: m}, sec
}

func TestModelSettingsDefaultsAndSave(t *testing.T) {
	srv, _ := llmServer(t)
	svc, _ := newModels(t, srv)
	s, err := svc.Settings(ctx)
	if err != nil || s.Provider != ProviderHosted || s.OpenAIBaseURL != "https://api.openai.com/v1" || s.AnthropicModel != "claude-opus-5" ||
		s.OpenAIKeySet || s.Label != "SiteMoment 托管" {
		t.Fatalf("defaults %+v, %v", s, err)
	}
	for _, bad := range []ModelSettings{
		{Provider: "gemini"},
		{Provider: ProviderOpenAI, OpenAIBaseURL: "http://api.example.com/v1"},
		{Provider: ProviderOpenAI, OpenAIBaseURL: "ftp://x"},
	} {
		if _, err := svc.Save(ctx, bad); err == nil {
			t.Errorf("saved %+v", bad)
		}
	}
	s, err = svc.Save(ctx, ModelSettings{Provider: ProviderOpenAI, OpenAIBaseURL: " http://127.0.0.1:11434/v1/ ", OpenAIModel: " qwen3 ", AnthropicModel: "claude-sonnet-5"})
	if err != nil || s.OpenAIBaseURL != "http://127.0.0.1:11434/v1" || s.OpenAIModel != "qwen3" || s.AnthropicModel != "claude-sonnet-5" || s.Label != "OpenAI 兼容 · qwen3" {
		t.Fatalf("saved %+v, %v", s, err)
	}
}

func TestModelKeysStayInKeychain(t *testing.T) {
	srv, _ := llmServer(t)
	svc, sec := newModels(t, srv)
	s, err := svc.SetKey(ctx, ProviderAnthropic, " sk-ant-1 ")
	if err != nil || !s.AnthropicKeySet || s.OpenAIKeySet {
		t.Fatalf("set %+v, %v", s, err)
	}
	if v, _ := sec.Get("llm:anthropic"); v != "sk-ant-1" {
		t.Fatalf("keychain %q", v)
	}
	if raw, _ := json.Marshal(s); strings.Contains(string(raw), "sk-ant-1") {
		t.Fatal("key in settings JSON")
	}
	if s, err = svc.SetKey(ctx, ProviderAnthropic, ""); err != nil || s.AnthropicKeySet {
		t.Fatalf("delete %+v, %v", s, err)
	}
	if _, err := svc.SetKey(ctx, ProviderHosted, "x"); err == nil {
		t.Fatal("hosted key accepted")
	}
}

func TestCurrentModel(t *testing.T) {
	srv, chats := llmServer(t)
	svc, sec := newModels(t, srv)
	m := svc.Models
	if _, err := m.Current(ctx); !errors.Is(err, ErrNoModel) {
		t.Fatalf("signed out: %v", err)
	}
	if ids, err := svc.HostedModels(ctx); err != nil || len(ids) != 0 {
		t.Fatalf("hosted models signed out: %v %v", ids, err)
	}

	// Signed in: the hosted model, with the SiteMoment access token.
	sec.Set("sitemoment.refresh", "r1")
	if ids, err := svc.HostedModels(ctx); err != nil || len(ids) != 1 || ids[0] != "gpt-5.6-sol" {
		t.Fatalf("hosted models: %v %v", ids, err)
	}
	if _, err := svc.Save(ctx, ModelSettings{Provider: ProviderHosted, HostedModel: "gpt-5.6-sol"}); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Test(ctx)
	if err != nil || got != "SiteMoment 托管 · gpt-5.6-sol：收到" {
		t.Fatalf("test %q, %v", got, err)
	}
	if c := (*chats)[0]; c["_path"] != "/v1/desktop/llm/chat/completions" || c["_auth"] != "Bearer acc" || c["model"] != "gpt-5.6-sol" {
		t.Fatalf("hosted call %v", c)
	}
	a, _ := m.Current(ctx)
	b, _ := m.Current(ctx)
	if a != b {
		t.Fatal("same settings built a new provider")
	}

	// Own OpenAI-compatible key.
	if _, err := svc.Save(ctx, ModelSettings{Provider: ProviderOpenAI, OpenAIBaseURL: srv.URL + "/byok/v1", OpenAIModel: "gpt-4.1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Current(ctx); err == nil || !strings.Contains(err.Error(), "key") {
		t.Fatalf("no key: %v", err)
	}
	svc.SetKey(ctx, ProviderOpenAI, "sk-own")
	mod, err := m.Current(ctx)
	if err != nil || mod.Label() != "OpenAI 兼容 · gpt-4.1" {
		t.Fatalf("own model %v, %v", mod, err)
	}
	if _, err := mod.Chat(ctx, llm.Request{Messages: []llm.Message{{Role: "user", Content: "hi"}}}, nil); err != nil {
		t.Fatal(err)
	}
	if c := (*chats)[1]; c["_path"] != "/byok/v1/chat/completions" || c["_auth"] != "Bearer sk-own" {
		t.Fatalf("own call %v", c)
	}

	// Anthropic needs its own key.
	svc.Save(ctx, ModelSettings{Provider: ProviderAnthropic})
	if _, err := m.Current(ctx); err == nil {
		t.Fatal("anthropic without key")
	}
	svc.SetKey(ctx, ProviderAnthropic, "sk-ant")
	if mod, err := m.Current(ctx); err != nil || mod.Label() != "Anthropic · claude-opus-5" {
		t.Fatalf("anthropic %v, %v", mod, err)
	}
}
