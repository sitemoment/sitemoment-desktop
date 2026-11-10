// SPDX-License-Identifier: AGPL-3.0-or-later

// Package cloud talks to the SiteMoment backend: desktop sign-in (PKCE +
// loopback, see backend/internal/api/desktop.go) and the signed-in account.
package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/secret"
)

// ErrSignedOut means there is no usable SiteMoment sign-in.
var ErrSignedOut = errors.New("cloud: not signed in")

var errInvalidGrant = errors.New("cloud: invalid grant")

const refreshKey = "sitemoment.refresh"

// Client is the SiteMoment API client. Base is the API root, e.g.
// "https://sitemoment.com/api".
type Client struct {
	Base    string
	HTTP    *http.Client
	Secrets secret.Store

	mu      sync.Mutex
	access  string
	expires time.Time
}

// Me is the signed-in account (subset of /v1/desktop/me).
type Me struct {
	User struct {
		Email     string `json:"email"`
		Name      string `json:"name"`
		AvatarURL string `json:"avatarUrl"`
	} `json:"user"`
	Plan struct {
		Name string `json:"name"`
	} `json:"plan"`
}

type tokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

// base is c.Base without a trailing slash, so callers can set it either way.
func (c *Client) base() string { return strings.TrimSuffix(c.Base, "/") }

// LoginURL is the browser address that starts desktop sign-in.
func (c *Client) LoginURL(redirectURI, state, challenge string) string {
	q := url.Values{"redirect_uri": {redirectURI}, "state": {state}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}}
	return c.base() + "/auth/desktop/start?" + q.Encode()
}

// Exchange trades the code from the loopback redirect for tokens and keeps
// the refresh token in the keychain.
func (c *Client) Exchange(ctx context.Context, code, verifier, redirectURI string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, err := c.token(ctx, map[string]string{"grant_type": "authorization_code", "code": code, "code_verifier": verifier, "redirect_uri": redirectURI})
	if err != nil {
		return err
	}
	return c.keep(t)
}

// keep stores t; the caller holds c.mu. The server has already rotated the
// refresh token by the time keep is called, so a failure to write it to the
// keychain is retried a few times rather than silently dropping the only
// copy of a token the server will no longer honor as "old".
func (c *Client) keep(t tokens) error {
	const retries = 3
	var err error
	for attempt := 0; attempt < retries; attempt++ {
		if err = c.Secrets.Set(refreshKey, t.RefreshToken); err == nil {
			break
		}
		if attempt < retries-1 {
			time.Sleep(50 * time.Millisecond)
		}
	}
	if err != nil {
		return err
	}
	c.access = t.AccessToken
	c.expires = time.Now().Add(time.Duration(t.ExpiresIn)*time.Second - time.Minute)
	return nil
}

// AccessToken returns a valid access token, refreshing it when needed. The
// lock is held for the duration of any refresh HTTP request, so c.HTTP must
// carry a request timeout (main.go uses a 60s client) or a stalled network
// call will block every other caller of this Client indefinitely.
func (c *Client) AccessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.access != "" && time.Now().Before(c.expires) {
		return c.access, nil
	}
	rt, err := c.Secrets.Get(refreshKey)
	if errors.Is(err, secret.ErrNotFound) {
		return "", ErrSignedOut
	}
	if err != nil {
		return "", err
	}
	t, err := c.token(ctx, map[string]string{"grant_type": "refresh_token", "refresh_token": rt})
	if errors.Is(err, errInvalidGrant) {
		c.Secrets.Delete(refreshKey)
		return "", ErrSignedOut
	}
	if err != nil {
		return "", err
	}
	if err := c.keep(t); err != nil {
		return "", err
	}
	return c.access, nil
}

// Me returns the signed-in account, or ErrSignedOut.
func (c *Client) Me(ctx context.Context) (Me, error) {
	var me Me
	for attempt := 0; attempt < 2; attempt++ {
		tok, err := c.AccessToken(ctx)
		if err != nil {
			return me, err
		}
		status, err := c.do(ctx, http.MethodGet, "/v1/desktop/me", tok, nil, &me)
		if status == http.StatusUnauthorized {
			c.mu.Lock()
			c.access = "" // revoked or expired early: refresh once
			c.mu.Unlock()
			continue
		}
		return me, err
	}
	return me, ErrSignedOut
}

// Logout revokes the sign-in on the server (best effort) and forgets it.
func (c *Client) Logout(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if rt, err := c.Secrets.Get(refreshKey); err == nil {
		c.do(ctx, http.MethodPost, "/v1/desktop/logout", "", map[string]string{"refresh_token": rt}, nil)
	}
	c.access = ""
	return c.Secrets.Delete(refreshKey)
}

func (c *Client) token(ctx context.Context, body map[string]string) (tokens, error) {
	var t tokens
	status, err := c.do(ctx, http.MethodPost, "/v1/desktop/token", "", body, &t)
	if status == http.StatusBadRequest {
		if ae, ok := err.(*apiError); ok && ae.Code == "invalid_grant" {
			return t, errInvalidGrant
		}
	}
	if err == nil && (t.AccessToken == "" || t.RefreshToken == "") {
		err = errors.New("cloud: token response without tokens")
	}
	return t, err
}

// apiError is a decoded {"error":{"code","message"}} response. Its fields
// and Error() string come only from the server's response body, never from
// the request we sent, so it is safe to log or return as-is: it cannot
// contain a refresh token, access token or other request credential.
type apiError struct {
	Status  int
	Code    string
	Message string
}

func (e *apiError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("cloud: HTTP %d %s: %s", e.Status, e.Code, e.Message)
	}
	return fmt.Sprintf("cloud: HTTP %d", e.Status)
}

// do sends a JSON request and decodes a JSON response into out. It returns
// the HTTP status alongside any error; an error status yields an *apiError.
func (c *Client) do(ctx context.Context, method, path, bearer string, body, out any) (int, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base()+path, rd)
	if err != nil {
		return 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return res.StatusCode, err
	}
	if res.StatusCode >= 400 {
		var e struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		json.Unmarshal(data, &e) // best effort; apiError is still returned on decode failure
		return res.StatusCode, &apiError{Status: res.StatusCode, Code: e.Error.Code, Message: e.Error.Message}
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return res.StatusCode, fmt.Errorf("cloud: %s %s: %w", method, path, err)
		}
	}
	return res.StatusCode, nil
}
