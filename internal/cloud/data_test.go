// SPDX-License-Identifier: AGPL-3.0-or-later
package cloud

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sitemoment/sitemoment-desktop/internal/secret"
)

// dataClient is a signed-in client (refresh token "r1") against a server
// that grants access token "a1" and hands the rest to h.
func dataClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/desktop/token" {
			w.Write([]byte(`{"access_token":"a1","refresh_token":"r2","expires_in":3600}`))
			return
		}
		if r.Header.Get("Authorization") != "Bearer a1" {
			w.WriteHeader(401)
			w.Write([]byte(`{"error":{"code":"unauthorized","message":"x"}}`))
			return
		}
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	sec := secret.NewMemory()
	sec.Set(refreshKey, "r1")
	return &Client{Base: srv.URL, HTTP: srv.Client(), Secrets: sec}
}

func TestMeHasCredits(t *testing.T) {
	c := dataClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"user":{"email":"me@example.com"},"plan":{"name":"free"},"credits":{"month":"2026-11","used":12.5,"limit":200}}`))
	})
	me, err := c.Me(ctx)
	if err != nil || me.Credits == nil || me.Credits.Used != 12.5 || me.Credits.Limit != 200 || me.Credits.Month != "2026-11" {
		t.Fatalf("Me = %+v, %v", me, err)
	}
}

func TestCatalogQuoteAndData(t *testing.T) {
	c := dataClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/desktop/data/catalog" && r.Method == http.MethodGet:
			w.Write([]byte(`{"tools":[{"name":"lookup_keywords","label":"关键词库存数据","maxCredits":3,"ttlDays":30,"required":["keywords"],"listParam":"keywords","maxItems":100,"enabled":true}]}`))
		case r.URL.Path == "/v1/desktop/data/lookup_keywords" && r.Method == http.MethodPost:
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if kws, _ := body["keywords"].([]any); len(kws) != 2 || body["gl"] != "us" {
				t.Errorf("body = %v", body)
			}
			if r.URL.Query().Get("quote") == "1" {
				w.Write([]byte(`{"tool":"lookup_keywords","label":"关键词库存数据","maxCredits":1.5,"cached":false}`))
				return
			}
			w.Write([]byte(`{"data":{"items":[{"keyword":"a","volume":10}]},"credits":1.5,"cached":false,"source":"SiteMoment · 关键词库存数据","fetched_at":1790000000}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
		}
	})
	tools, err := c.Catalog(ctx)
	if err != nil || len(tools) != 1 || tools[0].Name != "lookup_keywords" || tools[0].MaxCredits != 3 || !tools[0].Enabled {
		t.Fatalf("Catalog = %+v, %v", tools, err)
	}
	params := map[string]any{"keywords": []string{"a", "b"}, "gl": "us"}
	q, err := c.Quote(ctx, "lookup_keywords", params)
	if err != nil || q.MaxCredits != 1.5 || q.Cached || q.Label != "关键词库存数据" {
		t.Fatalf("Quote = %+v, %v", q, err)
	}
	res, err := c.Data(ctx, "lookup_keywords", params)
	if err != nil || res.Credits != 1.5 || res.Cached || res.FetchedAt != 1790000000 || !strings.Contains(string(res.Data), `"volume":10`) {
		t.Fatalf("Data = %+v, %v", res, err)
	}
}

func TestQuoteTooLarge(t *testing.T) {
	c := dataClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tool":"lookup_keywords","label":"关键词库存数据","maxCredits":999,"cached":false,"tooLarge":true,"message":"这次请求太大，请减少关键词数量或 limit"}`))
	})
	q, err := c.Quote(ctx, "lookup_keywords", map[string]any{"keywords": []string{"a"}})
	if err != nil || !q.TooLarge || q.Message != "这次请求太大，请减少关键词数量或 limit" {
		t.Fatalf("Quote tooLarge = %+v, %v", q, err)
	}
}

func TestDataErrors(t *testing.T) {
	c := dataClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/desktop/data/keyword_volume":
			w.WriteHeader(402)
			w.Write([]byte(`{"error":{"code":"credits_exhausted","message":"x","need":5,"used":198,"limit":200,"month":"2026-11"}}`))
		default:
			w.WriteHeader(503)
			w.Write([]byte(`{"error":{"code":"data_unavailable","message":"x"}}`))
		}
	})
	_, err := c.Data(ctx, "keyword_volume", map[string]any{"keywords": []string{"a"}})
	var ce *CreditsError
	if !errors.As(err, &ce) || ce.Need != 5 || ce.Used != 198 || ce.Limit != 200 || ce.Month != "2026-11" {
		t.Fatalf("402 = %v (%+v)", err, ce)
	}
	if _, err := c.Quote(ctx, "serp", map[string]any{"q": "x"}); !errors.Is(err, ErrDataUnavailable) {
		t.Fatalf("503 = %v", err)
	}
	if _, err := c.Data(ctx, "../token", nil); err == nil || errors.Is(err, ErrDataUnavailable) {
		t.Fatalf("bad tool name = %v", err)
	}
	out := &Client{Base: "http://127.0.0.1:1", HTTP: http.DefaultClient, Secrets: secret.NewMemory()}
	if _, err := out.Data(ctx, "serp", nil); !errors.Is(err, ErrSignedOut) {
		t.Fatalf("signed out = %v", err)
	}
}
