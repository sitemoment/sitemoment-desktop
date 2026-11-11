// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/cloud"
	"github.com/sitemoment/sitemoment-desktop/internal/llm"
	"github.com/sitemoment/sitemoment-desktop/internal/secret"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

// Model providers.
const (
	ProviderHosted    = "hosted"
	ProviderOpenAI    = "openai"
	ProviderAnthropic = "anthropic"
)

// Settings keys (values in the settings table) and keychain keys.
const (
	setProvider       = "llm.provider"
	setHostedModel    = "llm.hosted.model"
	setOpenAIBaseURL  = "llm.openai.baseUrl"
	setOpenAIModel    = "llm.openai.model"
	setAnthropicModel = "llm.anthropic.model"
	keyOpenAI         = "llm:openai"
	keyAnthropic      = "llm:anthropic"

	defaultOpenAIBaseURL = "https://api.openai.com/v1"
)

// ErrNoModel means no model can be used: signed out of SiteMoment and no key of one's own.
var ErrNoModel = errors.New("请先登录 SiteMoment，或在设置 → AI 模型里填自己的 key")

// ModelSettings is the model configuration shown in Settings. The keys
// themselves never leave the keychain; KeySet says whether one is saved.
type ModelSettings struct {
	Provider        string `json:"provider"` // hosted | openai | anthropic
	HostedModel     string `json:"hostedModel"`
	OpenAIBaseURL   string `json:"openaiBaseUrl"`
	OpenAIModel     string `json:"openaiModel"`
	AnthropicModel  string `json:"anthropicModel"`
	OpenAIKeySet    bool   `json:"openaiKeySet"`
	AnthropicKeySet bool   `json:"anthropicKeySet"`
	Label           string `json:"label"` // the current model, as the agent panel shows it
}

// Models builds the configured chat model. Providers are kept per
// configuration so what one learns about its server (e.g. that it needs the
// JSON tool protocol) lasts while the settings stay the same.
type Models struct {
	Store   *store.Store
	Secrets secret.Store
	Cloud   *cloud.Client
	HTTP    *http.Client // streaming: no overall timeout

	mu    sync.Mutex
	cache map[string]llm.Model
}

func (m *Models) settings(ctx context.Context) (ModelSettings, error) {
	var s ModelSettings
	for _, f := range []struct {
		key string
		dst *string
		def string
	}{
		{setProvider, &s.Provider, ProviderHosted},
		{setHostedModel, &s.HostedModel, ""},
		{setOpenAIBaseURL, &s.OpenAIBaseURL, defaultOpenAIBaseURL},
		{setOpenAIModel, &s.OpenAIModel, ""},
		{setAnthropicModel, &s.AnthropicModel, llm.AnthropicDefaultModel},
	} {
		v, err := m.Store.Setting(ctx, f.key)
		if err != nil {
			return s, err
		}
		if v == "" {
			v = f.def
		}
		*f.dst = v
	}
	s.OpenAIKeySet = m.hasKey(keyOpenAI)
	s.AnthropicKeySet = m.hasKey(keyAnthropic)
	s.Label = label(s)
	return s, nil
}

func (m *Models) hasKey(k string) bool {
	v, err := m.Secrets.Get(k)
	return err == nil && v != ""
}

func label(s ModelSettings) string {
	switch s.Provider {
	case ProviderOpenAI:
		return "OpenAI 兼容 · " + s.OpenAIModel
	case ProviderAnthropic:
		return "Anthropic · " + s.AnthropicModel
	}
	if s.HostedModel == "" {
		return "SiteMoment 托管"
	}
	return "SiteMoment 托管 · " + s.HostedModel
}

// Current returns the model the agent should use now.
func (m *Models) Current(ctx context.Context) (llm.Model, error) {
	s, err := m.settings(ctx)
	if err != nil {
		return nil, err
	}
	var key string
	switch s.Provider {
	case ProviderOpenAI:
		if key, err = m.Secrets.Get(keyOpenAI); err != nil || key == "" {
			return nil, errors.New("还没有填 OpenAI 兼容接口的 key（设置 → AI 模型）")
		}
		if s.OpenAIModel == "" {
			return nil, errors.New("还没有填 OpenAI 兼容接口的模型名（设置 → AI 模型）")
		}
	case ProviderAnthropic:
		if key, err = m.Secrets.Get(keyAnthropic); err != nil || key == "" {
			return nil, errors.New("还没有填 Anthropic 的 key（设置 → AI 模型）")
		}
	default:
		if !m.Cloud.SignedIn() {
			return nil, ErrNoModel
		}
	}
	sum := sha256.Sum256([]byte(key))
	id := strings.Join([]string{s.Provider, s.HostedModel, s.OpenAIBaseURL, s.OpenAIModel, s.AnthropicModel, hex.EncodeToString(sum[:])}, "\x00")
	m.mu.Lock()
	defer m.mu.Unlock()
	if mod, ok := m.cache[id]; ok {
		return mod, nil
	}
	var mod llm.Model
	switch s.Provider {
	case ProviderOpenAI:
		mod = &llm.OpenAI{BaseURL: s.OpenAIBaseURL, Key: key, Model: s.OpenAIModel, HTTP: m.HTTP, Name: s.Label}
	case ProviderAnthropic:
		mod = &llm.Anthropic{Key: key, Model: s.AnthropicModel, HTTP: m.HTTP, Name: s.Label}
	default:
		mod = &llm.OpenAI{URL: m.Cloud.LLMURL(), Model: s.HostedModel, HTTP: m.HTTP, Name: s.Label, Auth: m.hostedToken}
	}
	if m.cache == nil {
		m.cache = map[string]llm.Model{}
	}
	m.cache[id] = mod
	return mod, nil
}

func (m *Models) hostedToken(ctx context.Context, refresh bool) (string, error) {
	if refresh {
		m.Cloud.ResetAccess()
	}
	tok, err := m.Cloud.AccessToken(ctx)
	if errors.Is(err, cloud.ErrSignedOut) {
		return "", ErrNoModel
	}
	return tok, err
}

// ModelService is the Settings page's "AI 模型" section.
type ModelService struct{ Models *Models }

// Settings returns the model settings.
func (s *ModelService) Settings(ctx context.Context) (ModelSettings, error) {
	return s.Models.settings(ctx)
}

// Save stores the provider, base URL and model names (keys use SetKey).
func (s *ModelService) Save(ctx context.Context, in ModelSettings) (ModelSettings, error) {
	switch in.Provider {
	case ProviderHosted, ProviderOpenAI, ProviderAnthropic:
	default:
		return ModelSettings{}, errors.New("未知的模型来源")
	}
	base := strings.TrimRight(strings.TrimSpace(in.OpenAIBaseURL), "/")
	if base == "" {
		base = defaultOpenAIBaseURL
	}
	if err := checkBaseURL(base); err != nil {
		return ModelSettings{}, err
	}
	vals := map[string]string{
		setProvider:       in.Provider,
		setHostedModel:    strings.TrimSpace(in.HostedModel),
		setOpenAIBaseURL:  base,
		setOpenAIModel:    strings.TrimSpace(in.OpenAIModel),
		setAnthropicModel: strings.TrimSpace(in.AnthropicModel),
	}
	for k, v := range vals {
		if len(v) > 200 {
			return ModelSettings{}, errors.New("模型名或地址太长")
		}
		if err := s.Models.Store.SetSetting(ctx, k, v); err != nil {
			return ModelSettings{}, err
		}
	}
	return s.Models.settings(ctx)
}

// checkBaseURL allows https anywhere and plain http only on this computer
// (local servers such as Ollama): the key must not cross the network in clear.
func checkBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return errors.New("接口地址不是有效的网址")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		h := u.Hostname()
		if h == "localhost" || net.ParseIP(h) != nil && net.ParseIP(h).IsLoopback() {
			return nil
		}
		return errors.New("接口地址必须是 https（本机地址除外）")
	}
	return errors.New("接口地址必须以 https:// 开头")
}

// SetKey saves (or, when key is empty, deletes) a provider's API key in the keychain.
func (s *ModelService) SetKey(ctx context.Context, provider, key string) (ModelSettings, error) {
	k := map[string]string{ProviderOpenAI: keyOpenAI, ProviderAnthropic: keyAnthropic}[provider]
	if k == "" {
		return ModelSettings{}, errors.New("只有 OpenAI 兼容接口和 Anthropic 需要 key")
	}
	key = strings.TrimSpace(key)
	var err error
	if key == "" {
		err = s.Models.Secrets.Delete(k)
	} else {
		err = s.Models.Secrets.Set(k, key)
	}
	if err != nil {
		return ModelSettings{}, err
	}
	return s.Models.settings(ctx)
}

// HostedModels lists SiteMoment's hosted models (none when signed out).
func (s *ModelService) HostedModels(ctx context.Context) ([]string, error) {
	if !s.Models.Cloud.SignedIn() {
		return []string{}, nil
	}
	ids, err := s.Models.Cloud.LLMModels(ctx)
	if errors.Is(err, cloud.ErrSignedOut) {
		return []string{}, nil
	}
	return ids, err
}

// Test sends a tiny message to the current model.
func (s *ModelService) Test(ctx context.Context) (string, error) {
	mod, err := s.Models.Current(ctx)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	r, err := mod.Chat(ctx, llm.Request{Messages: []llm.Message{{Role: "user", Content: "只回复两个字：收到"}}}, nil)
	if err != nil {
		return "", err
	}
	reply := []rune(strings.TrimSpace(r.Content))
	if len(reply) > 80 {
		reply = reply[:80]
	}
	return fmt.Sprintf("%s：%s", mod.Label(), string(reply)), nil
}
