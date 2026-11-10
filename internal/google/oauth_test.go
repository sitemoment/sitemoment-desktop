// SPDX-License-Identifier: AGPL-3.0-or-later
package google

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestAuthCodeURL(t *testing.T) {
	o := &OAuth{ClientID: "cid"}
	u, err := url.Parse(o.AuthCodeURL("http://127.0.0.1:5000/callback", "st", "ch"))
	if err != nil || u.Host != "accounts.google.com" {
		t.Fatalf("url %v %v", u, err)
	}
	q := u.Query()
	for k, want := range map[string]string{
		"client_id": "cid", "redirect_uri": "http://127.0.0.1:5000/callback", "response_type": "code", "state": "st",
		"code_challenge": "ch", "code_challenge_method": "S256", "access_type": "offline", "prompt": "consent",
	} {
		if q.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, q.Get(k), want)
		}
	}
	if !strings.Contains(q.Get("scope"), "webmasters.readonly") || !strings.Contains(q.Get("scope"), "email") {
		t.Errorf("scope = %q", q.Get("scope"))
	}
}

func TestExchangeAndCachedAccess(t *testing.T) {
	calls := 0
	var form url.Values
	o := &OAuth{ClientID: "cid", ClientSecret: "sec", HTTP: client(func(r *http.Request) (*http.Response, error) {
		calls++
		r.ParseForm()
		form = r.PostForm
		return reply(200, `{"access_token":"at1","refresh_token":"rt1","expires_in":3600,"scope":"openid email https://www.googleapis.com/auth/webmasters.readonly","id_token":"`+idToken("me@example.com")+`"}`)
	})}
	g, err := o.Exchange(ctx, "code1", "ver1", "http://127.0.0.1:5000/callback")
	if err != nil || g.Email != "me@example.com" || g.RefreshToken != "rt1" {
		t.Fatalf("Exchange = %+v, %v", g, err)
	}
	for k, want := range map[string]string{"code": "code1", "code_verifier": "ver1", "grant_type": "authorization_code", "client_id": "cid", "client_secret": "sec"} {
		if form.Get(k) != want {
			t.Errorf("form %s = %q", k, form.Get(k))
		}
	}
	tok, err := o.AccessToken(ctx, "rt1")
	if err != nil || tok != "at1" || calls != 1 {
		t.Fatalf("AccessToken = %q, %v after %d calls", tok, err, calls)
	}
}

func TestExchangeNeedsSearchConsoleScope(t *testing.T) {
	o := &OAuth{ClientID: "cid", HTTP: client(func(*http.Request) (*http.Response, error) {
		return reply(200, `{"access_token":"at","refresh_token":"rt","expires_in":3600,"scope":"openid email","id_token":"`+idToken("me@example.com")+`"}`)
	})}
	if _, err := o.Exchange(ctx, "c", "v", "http://127.0.0.1:5000/callback"); !errors.Is(err, ErrScopes) {
		t.Fatalf("err = %v, want ErrScopes", err)
	}
}

func TestAccessTokenRefreshAndReauth(t *testing.T) {
	calls := 0
	o := &OAuth{ClientID: "cid", HTTP: client(func(r *http.Request) (*http.Response, error) {
		calls++
		r.ParseForm()
		if r.PostForm.Get("refresh_token") == "dead" {
			return reply(400, `{"error":"invalid_grant"}`)
		}
		return reply(200, `{"access_token":"at2","expires_in":3600}`)
	})}
	for i := 0; i < 2; i++ {
		if tok, err := o.AccessToken(ctx, "rt"); err != nil || tok != "at2" {
			t.Fatalf("AccessToken = %q, %v", tok, err)
		}
	}
	if calls != 1 {
		t.Fatalf("refresh calls = %d, want 1 (cached)", calls)
	}
	if _, err := o.AccessToken(ctx, "dead"); !errors.Is(err, ErrReauth) {
		t.Fatalf("dead refresh: %v", err)
	}
}
