// SPDX-License-Identifier: AGPL-3.0-or-later
package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/sitemoment/sitemoment-desktop/internal/secret"
)

var ctx = context.Background()

// fakeServer mimics /v1/desktop/token, /v1/desktop/me and /v1/desktop/logout.
type fakeServer struct {
	mu        sync.Mutex
	access    string
	refresh   string
	n         int
	meCalls   int
	loggedOut bool
}

func (f *fakeServer) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.URL.Path {
		case "/v1/desktop/token":
			var req map[string]string
			json.NewDecoder(r.Body).Decode(&req)
			ok := (req["grant_type"] == "authorization_code" && req["code"] == "c1" && req["code_verifier"] == "v1") ||
				(req["grant_type"] == "refresh_token" && req["refresh_token"] == f.refresh && f.refresh != "")
			if !ok {
				w.WriteHeader(400)
				w.Write([]byte(`{"error":{"code":"invalid_grant","message":"x"}}`))
				return
			}
			f.n++
			f.access, f.refresh = "a"+string(rune('0'+f.n)), "r"+string(rune('0'+f.n))
			json.NewEncoder(w).Encode(map[string]any{"access_token": f.access, "refresh_token": f.refresh, "token_type": "Bearer", "expires_in": 3600})
		case "/v1/desktop/me":
			f.meCalls++
			if r.Header.Get("Authorization") != "Bearer "+f.access {
				w.WriteHeader(401)
				w.Write([]byte(`{"error":{"code":"unauthorized","message":"x"}}`))
				return
			}
			w.Write([]byte(`{"user":{"email":"me@example.com","name":"Me","avatarUrl":""},"plan":{"name":"pro"}}`))
		case "/v1/desktop/logout":
			f.loggedOut = true
			w.Write([]byte(`{"ok":true}`))
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	})
}

func setup(t *testing.T) (*Client, *fakeServer, *secret.Memory) {
	f := &fakeServer{}
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	sec := secret.NewMemory()
	return &Client{Base: srv.URL, HTTP: srv.Client(), Secrets: sec}, f, sec
}

func TestLoginURL(t *testing.T) {
	c := &Client{Base: "https://sitemoment.com/api"}
	u, err := url.Parse(c.LoginURL("http://127.0.0.1:5000/callback", "st", "ch"))
	if err != nil || u.Path != "/api/auth/desktop/start" {
		t.Fatalf("LoginURL = %v %v", u, err)
	}
	q := u.Query()
	if q.Get("redirect_uri") != "http://127.0.0.1:5000/callback" || q.Get("state") != "st" || q.Get("code_challenge") != "ch" || q.Get("code_challenge_method") != "S256" {
		t.Fatalf("query = %v", q)
	}
}

func TestSignInMeAndRefresh(t *testing.T) {
	c, f, sec := setup(t)
	if _, err := c.Me(ctx); !errors.Is(err, ErrSignedOut) {
		t.Fatalf("Me before sign-in: %v", err)
	}
	if err := c.Exchange(ctx, "c1", "v1", "http://127.0.0.1:5000/callback"); err != nil {
		t.Fatal(err)
	}
	if v, _ := sec.Get(refreshKey); v != "r1" {
		t.Fatalf("stored refresh = %q", v)
	}
	me, err := c.Me(ctx)
	if err != nil || me.User.Email != "me@example.com" || me.Plan.Name != "pro" {
		t.Fatalf("Me = %+v, %v", me, err)
	}
	// A fresh client (app restart) refreshes from the keychain.
	c2 := &Client{Base: c.Base, HTTP: c.HTTP, Secrets: sec}
	if me, err := c2.Me(ctx); err != nil || me.User.Email != "me@example.com" {
		t.Fatalf("Me after restart = %+v, %v", me, err)
	}
	if v, _ := sec.Get(refreshKey); v != "r2" {
		t.Fatalf("rotated refresh = %q", v)
	}
	if err := c2.Logout(ctx); err != nil || !f.loggedOut {
		t.Fatalf("Logout: %v %v", err, f.loggedOut)
	}
	if _, err := sec.Get(refreshKey); !errors.Is(err, secret.ErrNotFound) {
		t.Fatal("refresh token kept after logout")
	}
}

func TestRevokedRefreshSignsOut(t *testing.T) {
	c, _, sec := setup(t)
	sec.Set(refreshKey, "stale")
	if _, err := c.Me(ctx); !errors.Is(err, ErrSignedOut) {
		t.Fatalf("Me with revoked refresh: %v", err)
	}
	if _, err := sec.Get(refreshKey); !errors.Is(err, secret.ErrNotFound) {
		t.Fatal("revoked refresh token not cleared")
	}
}
