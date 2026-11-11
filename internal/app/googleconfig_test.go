// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/cloud"
	"github.com/sitemoment/sitemoment-desktop/internal/google"
)

// configServer serves GET /v1/desktop/config with whatever is stored in id/secret.
type configServer struct {
	id, secret atomic.Value // string
	calls      atomic.Int64
	block      chan struct{} // if non-nil, handler waits on it
}

func newConfigServer(id, secret string) *configServer {
	s := &configServer{}
	s.id.Store(id)
	s.secret.Store(secret)
	return s
}

func (s *configServer) set(id, secret string) { s.id.Store(id); s.secret.Store(secret) }

func (s *configServer) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.calls.Add(1)
		if s.block != nil {
			<-s.block
		}
		if r.URL.Path != "/v1/desktop/config" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{"google": map[string]string{
			"clientId": s.id.Load().(string), "clientSecret": s.secret.Load().(string),
		}})
	})
}

func TestGoogleConfigRefreshAppliesAndCaches(t *testing.T) {
	st := openStore(t)
	srv := httptest.NewServer(newConfigServer("remote-cid", "remote-sec").handler(t))
	defer srv.Close()
	oauth := &google.OAuth{}
	cfg := &GoogleConfig{OAuth: oauth, Cloud: &cloud.Client{Base: srv.URL, HTTP: srv.Client()}, Store: st}

	if oauth.Configured() {
		t.Fatal("Configured before Refresh")
	}
	if err := cfg.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if !oauth.Configured() {
		t.Fatal("Configured after Refresh = false")
	}
	if id, _ := st.Setting(ctx, settingGoogleClientID); id != "remote-cid" {
		t.Fatalf("cached client id = %q", id)
	}
	if secret, _ := st.Setting(ctx, settingGoogleClientSecret); secret != "remote-sec" {
		t.Fatalf("cached secret = %q", secret)
	}

	// A fresh OAuth (app restart) picks up the cached config via Load,
	// without touching the network.
	oauth2 := &google.OAuth{}
	cfg2 := &GoogleConfig{OAuth: oauth2, Cloud: &cloud.Client{Base: "http://unreachable.invalid"}, Store: st}
	cfg2.Load(ctx)
	if !oauth2.Configured() {
		t.Fatal("Configured after Load from cache = false")
	}
}

// TestGoogleConfigEnvOverrideWins: when OAuth already has a client id (the
// env/ldflags override), Refresh and Load must never consult the backend.
func TestGoogleConfigEnvOverrideWins(t *testing.T) {
	st := openStore(t)
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Write([]byte(`{"google":{"clientId":"remote-cid","clientSecret":"remote-sec"}}`))
	}))
	defer srv.Close()
	oauth := &google.OAuth{ClientID: "env-cid", ClientSecret: "env-sec"}
	cfg := &GoogleConfig{OAuth: oauth, Cloud: &cloud.Client{Base: srv.URL, HTTP: srv.Client()}, Store: st}

	cfg.Load(ctx)
	if err := cfg.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("backend called despite an env/ldflags override")
	}
	id, _ := oauthClientForTest(oauth)
	if id != "env-cid" {
		t.Fatalf("client id = %q, want the env override unchanged", id)
	}
}

// TestGoogleConfigRemoteChangeIsPickedUp: when the backend's client id
// changes between two Refresh calls, the new one wins and is cached; a
// connection's refresh token, issued to the old client, is expected to fail
// as google.ErrReauth the next time it is used (see oauth_test.go's
// TestSetClientChangeInvalidatesCache for the cache-invalidation mechanism
// and sources_test.go for the end-to-end message).
func TestGoogleConfigRemoteChangeIsPickedUp(t *testing.T) {
	st := openStore(t)
	cs := newConfigServer("cid-a", "sec-a")
	srv := httptest.NewServer(cs.handler(t))
	defer srv.Close()
	oauth := &google.OAuth{}
	cfg := &GoogleConfig{OAuth: oauth, Cloud: &cloud.Client{Base: srv.URL, HTTP: srv.Client()}, Store: st}

	if err := cfg.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	cs.set("cid-b", "sec-b")
	if err := cfg.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	id, _ := oauthClientForTest(oauth)
	if id != "cid-b" {
		t.Fatalf("client id after remote change = %q, want cid-b", id)
	}
	if cachedID, _ := st.Setting(ctx, settingGoogleClientID); cachedID != "cid-b" {
		t.Fatalf("cached client id after remote change = %q", cachedID)
	}
}

// TestGoogleConfigPeekTimesOutFast: peek() must not block its caller for
// longer than googleConfigPeekTimeout even if the backend never answers.
func TestGoogleConfigPeekTimesOutFast(t *testing.T) {
	cs := newConfigServer("cid", "sec")
	cs.block = make(chan struct{})
	srv := httptest.NewServer(cs.handler(t))
	defer srv.Close()
	defer close(cs.block)
	old := googleConfigPeekTimeout
	googleConfigPeekTimeout = 50 * time.Millisecond
	defer func() { googleConfigPeekTimeout = old }()

	st := openStore(t)
	oauth := &google.OAuth{}
	cfg := &GoogleConfig{OAuth: oauth, Cloud: &cloud.Client{Base: srv.URL, HTTP: srv.Client()}, Store: st}
	start := time.Now()
	cfg.peek(ctx)
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("peek took %v, want it bounded by googleConfigPeekTimeout", d)
	}
	if oauth.Configured() {
		t.Fatal("Configured after a timed-out peek")
	}
}

// TestGoogleConfigRefreshEmptyKeepsCachedClient: a transient backend blip
// (or a config GET that races a save) that comes back with empty fields
// must not wipe a client that is already cached and working - that would
// break everyone's Google sign-in on a hiccup.
func TestGoogleConfigRefreshEmptyKeepsCachedClient(t *testing.T) {
	st := openStore(t)
	cs := newConfigServer("cid-a", "sec-a")
	srv := httptest.NewServer(cs.handler(t))
	defer srv.Close()
	oauth := &google.OAuth{}
	cfg := &GoogleConfig{OAuth: oauth, Cloud: &cloud.Client{Base: srv.URL, HTTP: srv.Client()}, Store: st}

	if err := cfg.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	cs.set("", "") // backend now reports nothing configured (blip, or a race with a save)
	if err := cfg.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	id, _ := oauthClientForTest(oauth)
	if id != "cid-a" {
		t.Fatalf("client id after an empty backend response = %q, want the cached cid-a kept", id)
	}
	if cachedID, _ := st.Setting(ctx, settingGoogleClientID); cachedID != "cid-a" {
		t.Fatalf("cached client id after an empty backend response = %q", cachedID)
	}
	if cachedSecret, _ := st.Setting(ctx, settingGoogleClientSecret); cachedSecret != "sec-a" {
		t.Fatalf("cached secret after an empty backend response = %q", cachedSecret)
	}
	if !oauth.Configured() {
		t.Fatal("Configured after an empty backend response = false")
	}
}

// TestGoogleConfigPeekRespectsOwnTimeoutDuringSlowRefresh: peek's short
// timeout must hold even while a different, slow Refresh call (no deadline
// of its own) is already in flight against the same GoogleConfig. Before
// Refresh was made single-flight, a second caller blocked on the mutex
// itself - ignoring its own context - until the first fetch finished.
func TestGoogleConfigPeekRespectsOwnTimeoutDuringSlowRefresh(t *testing.T) {
	cs := newConfigServer("cid", "sec")
	cs.block = make(chan struct{})
	srv := httptest.NewServer(cs.handler(t))
	defer srv.Close()
	old := googleConfigPeekTimeout
	googleConfigPeekTimeout = 200 * time.Millisecond
	defer func() { googleConfigPeekTimeout = old }()

	st := openStore(t)
	oauth := &google.OAuth{}
	cfg := &GoogleConfig{OAuth: oauth, Cloud: &cloud.Client{Base: srv.URL, HTTP: srv.Client()}, Store: st}

	// A slow Refresh already in flight, with no deadline of its own: it
	// only returns once the server is unblocked, below.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		cfg.Refresh(context.Background())
	}()
	for cs.calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}

	start := time.Now()
	cfg.peek(ctx)
	if d := time.Since(start); d > time.Second {
		t.Fatalf("peek took %v while a slow Refresh was in flight, want it bounded by googleConfigPeekTimeout (%v)", d, googleConfigPeekTimeout)
	}
	if oauth.Configured() {
		t.Fatal("Configured after a timed-out peek, while the slow Refresh had not applied anything yet")
	}

	close(cs.block)
	wg.Wait()
}

// oauthClientForTest reads the client id OAuth currently resolves to,
// through its public surface (AuthCodeURL encodes client_id).
func oauthClientForTest(o *google.OAuth) (string, string) {
	raw := o.AuthCodeURL("http://127.0.0.1:1/callback", "s", "c")
	u, err := url.Parse(raw)
	if err != nil {
		return "", ""
	}
	return u.Query().Get("client_id"), ""
}
