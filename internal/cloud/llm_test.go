// SPDX-License-Identifier: AGPL-3.0-or-later
package cloud

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sitemoment/sitemoment-desktop/internal/secret"
)

func TestLLMModelsAndSignIn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/desktop/token":
			w.Write([]byte(`{"access_token":"a1","refresh_token":"r2","expires_in":3600}`))
		case "/v1/desktop/llm/models":
			if r.Header.Get("Authorization") != "Bearer a1" {
				w.WriteHeader(401)
				return
			}
			w.Write([]byte(`{"object":"list","data":[{"id":"gpt-5.6-sol","object":"model"},{"id":"","object":"model"}]}`))
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	sec := secret.NewMemory()
	c := &Client{Base: srv.URL + "/", HTTP: srv.Client(), Secrets: sec}
	if c.SignedIn() {
		t.Fatal("signed in without a refresh token")
	}
	if _, err := c.LLMModels(ctx); !errors.Is(err, ErrSignedOut) {
		t.Fatalf("signed out: %v", err)
	}
	sec.Set(refreshKey, "r1")
	if !c.SignedIn() {
		t.Fatal("not signed in")
	}
	ids, err := c.LLMModels(ctx)
	if err != nil || len(ids) != 1 || ids[0] != "gpt-5.6-sol" {
		t.Fatalf("models %v, %v", ids, err)
	}
	if c.LLMURL() != srv.URL+"/v1/desktop/llm/chat/completions" {
		t.Fatalf("LLMURL %s", c.LLMURL())
	}
	c.ResetAccess()
	if c.access != "" {
		t.Fatal("access token kept")
	}
}
