// SPDX-License-Identifier: AGPL-3.0-or-later

// Package google talks to Google: OAuth for a desktop app (PKCE + loopback)
// and the Search Console API.
package google

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	defaultAuthURL  = "https://accounts.google.com/o/oauth2/v2/auth"
	defaultTokenURL = "https://oauth2.googleapis.com/token"
	// Scopes requested when connecting an account.
	Scopes = "openid email https://www.googleapis.com/auth/webmasters.readonly https://www.googleapis.com/auth/analytics.readonly"
)

var (
	// ErrReauth means the stored authorization no longer works.
	ErrReauth = errors.New("google: authorization expired, connect the account again")
	// ErrScopes means the user did not grant Search Console access.
	ErrScopes = errors.New("google: Search Console access was not granted")
)

// OAuth is a Google OAuth client of type "Desktop app". ClientID and
// ClientSecret are the env/ldflags override: set once at construction (see
// main.go) and never written again, so reading them needs no lock. When
// they are empty, the dynamic client set by SetClient is used instead (the
// desktop app's central configuration, fetched from the SiteMoment backend
// and cached locally; see internal/app.GoogleConfig). SetClient may be
// called at any time from a different goroutine than the one reading the
// client via AccessToken/AuthCodeURL/Exchange/Configured, so that pair is
// guarded by mu.
type OAuth struct {
	ClientID, ClientSecret string
	AuthURL, TokenURL      string // empty: Google's
	HTTP                   *http.Client

	mu                             sync.Mutex
	dynamicClientID, dynamicSecret string
	cache                          map[string]cachedToken // by sha256 of (client id, refresh token)
}

// SetClient sets the OAuth client used when ClientID is not set at
// construction (the backend's central configuration, or a cached copy of
// it). Safe to call concurrently with any other OAuth method.
func (o *OAuth) SetClient(id, secret string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.dynamicClientID, o.dynamicSecret = id, secret
}

// client resolves the OAuth client to use: the env/ldflags override from
// construction, else the dynamic one set by SetClient.
func (o *OAuth) client() (id, secret string) {
	if o.ClientID != "" {
		return o.ClientID, o.ClientSecret
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.dynamicClientID, o.dynamicSecret
}

type cachedToken struct {
	token   string
	expires time.Time
}

// Grant is a connected account.
type Grant struct {
	Email        string
	RefreshToken string
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	IDToken      string `json:"id_token"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
}

// Configured reports whether a client ID is set (the env/ldflags override,
// or one set by SetClient).
func (o *OAuth) Configured() bool { id, _ := o.client(); return id != "" }

// AuthCodeURL is the consent page address.
func (o *OAuth) AuthCodeURL(redirectURI, state, challenge string) string {
	id, _ := o.client()
	q := url.Values{
		"client_id": {id}, "redirect_uri": {redirectURI}, "response_type": {"code"}, "scope": {Scopes},
		"state": {state}, "code_challenge": {challenge}, "code_challenge_method": {"S256"},
		"access_type": {"offline"}, "prompt": {"consent"}, "include_granted_scopes": {"true"},
	}
	base := o.AuthURL
	if base == "" {
		base = defaultAuthURL
	}
	return base + "?" + q.Encode()
}

// Exchange trades the code for a refresh token and the account email.
func (o *OAuth) Exchange(ctx context.Context, code, verifier, redirectURI string) (Grant, error) {
	id, secret := o.client()
	tr, err := o.post(ctx, url.Values{
		"code": {code}, "code_verifier": {verifier}, "redirect_uri": {redirectURI},
		"client_id": {id}, "client_secret": {secret}, "grant_type": {"authorization_code"},
	})
	if err != nil {
		return Grant{}, err
	}
	if !hasWebmastersScope(tr.Scope) {
		return Grant{}, ErrScopes
	}
	if tr.RefreshToken == "" {
		return Grant{}, errors.New("google: no refresh token returned")
	}
	email, err := idTokenEmail(tr.IDToken)
	if err != nil {
		return Grant{}, err
	}
	o.remember(id, tr.RefreshToken, tr)
	return Grant{Email: email, RefreshToken: tr.RefreshToken}, nil
}

// AccessToken returns an access token for the refresh token, cached until a
// minute before it expires. The cache is keyed by the client id as well as
// the refresh token: if the client changes (the backend's configuration
// changed), a cached token minted for the old client is never served back,
// and the refresh token is sent to Google again under the new client id
// (where, having been issued to a different client, it is expected to come
// back ErrReauth).
func (o *OAuth) AccessToken(ctx context.Context, refreshToken string) (string, error) {
	id, secret := o.client()
	key := cacheKey(id, refreshToken)
	o.mu.Lock()
	if c, ok := o.cache[key]; ok && time.Now().Before(c.expires) {
		o.mu.Unlock()
		return c.token, nil
	}
	o.mu.Unlock()
	tr, err := o.post(ctx, url.Values{
		"refresh_token": {refreshToken}, "client_id": {id}, "client_secret": {secret}, "grant_type": {"refresh_token"},
	})
	if err != nil {
		return "", err
	}
	o.remember(id, refreshToken, tr)
	return tr.AccessToken, nil
}

// hasWebmastersScope reports whether the granted scopes include the
// Search Console scope, checked as exact space-separated token membership
// so a lookalike scope (e.g. "...webmasters.fake") does not pass.
func hasWebmastersScope(scope string) bool {
	for _, s := range strings.Fields(scope) {
		if s == "https://www.googleapis.com/auth/webmasters.readonly" || s == "https://www.googleapis.com/auth/webmasters" {
			return true
		}
	}
	return false
}

// cacheKey mixes the client id into the hash so a changed client never
// shares a cache entry with the old one.
func cacheKey(clientID, refreshToken string) string {
	sum := sha256.Sum256([]byte(clientID + "\x00" + refreshToken))
	return hex.EncodeToString(sum[:])
}

func (o *OAuth) remember(clientID, refreshToken string, tr tokenResponse) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.cache == nil {
		o.cache = map[string]cachedToken{}
	}
	o.cache[cacheKey(clientID, refreshToken)] = cachedToken{token: tr.AccessToken, expires: time.Now().Add(time.Duration(tr.ExpiresIn)*time.Second - time.Minute)}
}

func (o *OAuth) post(ctx context.Context, form url.Values) (tokenResponse, error) {
	var tr tokenResponse
	endpoint := o.TokenURL
	if endpoint == "" {
		endpoint = defaultTokenURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return tr, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := o.HTTP.Do(req)
	if err != nil {
		return tr, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 8192))
	json.Unmarshal(body, &tr)
	if res.StatusCode >= 400 {
		if tr.Error == "invalid_grant" {
			return tr, ErrReauth
		}
		return tr, fmt.Errorf("google: token HTTP %d %s", res.StatusCode, tr.Error)
	}
	if tr.AccessToken == "" {
		return tr, errors.New("google: token response without access_token")
	}
	return tr, nil
}

// idTokenEmail reads the email claim. The token came straight from Google's
// token endpoint over TLS, so its signature need not be checked (OpenID
// Connect Core 3.1.3.7).
func idTokenEmail(idToken string) (string, error) {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return "", errors.New("google: no id_token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", err
	}
	var claims struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Email == "" {
		return "", errors.New("google: id_token without email")
	}
	return claims.Email, nil
}
