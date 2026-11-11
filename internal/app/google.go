// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"context"
	"errors"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/google"
	"github.com/sitemoment/sitemoment-desktop/internal/oauthx"
	"github.com/sitemoment/sitemoment-desktop/internal/secret"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

const googleKeyPrefix = "google:"

// ErrGoogleNotConfigured means no Google OAuth client is available: no
// env/ldflags override, and the backend's central configuration (or a
// locally cached copy of it) has not resolved one either.
var ErrGoogleNotConfigured = errors.New("SiteMoment 还没有配置 Google 登录。请联系站长在后台（sitemoment.com/admx → 桌面客户端）设置。")

// ErrGoogleAuthTimeout: the browser never came back within the login
// timeout. The usual causes are a blocked consent page (the app is in
// testing and the account is not a test user) or no route to Google.
var ErrGoogleAuthTimeout = errors.New("浏览器里没有完成 Google 授权（5 分钟超时）。如果页面提示「访问被阻止」，请让站长把你的 Google 账号加进测试用户；如果打不开 Google，请检查 设置 → 网络代理。")

// ErrGoogleAccessDenied is Google's error=access_denied: the user said no,
// or the account may not use an app that is still in testing.
var ErrGoogleAccessDenied = errors.New("你在浏览器里取消了授权，或这个 Google 账号不在测试用户名单里")

// googleAuthURLEvent carries the consent URL (a string) to the main window
// while Connect waits, so it can be copied into another browser.
const googleAuthURLEvent = "google:auth-url"

// GoogleTokens turns a connected account into access tokens. It is used by
// the sync and is deliberately not a frontend service.
type GoogleTokens struct {
	OAuth   *google.OAuth
	Secrets secret.Store
	// Config, when set, is tried (with a short timeout) before giving up on
	// a token refresh that finds no OAuth client configured yet.
	Config *GoogleConfig
}

// AccessToken implements gscsync.TokenSource.
func (g *GoogleTokens) AccessToken(ctx context.Context, account string) (string, error) {
	rt, err := g.Secrets.Get(googleKeyPrefix + account)
	if errors.Is(err, secret.ErrNotFound) {
		return "", google.ErrReauth
	}
	if err != nil {
		return "", err
	}
	if g.Config != nil {
		g.Config.peek(ctx)
	}
	return g.OAuth.AccessToken(ctx, rt)
}

// GoogleService connects Google accounts and lists their properties.
type GoogleService struct {
	OAuth   *google.OAuth
	GSC     *google.GSC
	GA      *google.GA
	Tokens  *GoogleTokens
	Store   *store.Store
	Secrets secret.Store
	Open    Opener
	// Emit, when set, sends events to the main window (googleAuthURLEvent).
	Emit func(name string, data any)
	// Timeout is how long Connect waits for the browser; 0 means
	// loginTimeout.
	Timeout time.Duration
	// Config, when set, resolves the OAuth client from the backend: a
	// background fetch at startup (main.go), and an on-demand short-timeout
	// attempt here whenever nothing is configured yet.
	Config *GoogleConfig
}

// Configured reports whether a Google OAuth client is available: the
// env/ldflags override, or the backend's central configuration (fetched in
// the background at startup, cached locally). When neither is known yet it
// makes one short on-demand attempt to fetch the backend's config first.
func (g *GoogleService) Configured(ctx context.Context) bool {
	if g.Config != nil {
		g.Config.peek(ctx)
	}
	return g.OAuth.Configured()
}

// RefreshConfig re-fetches the backend's configuration right away (the
// Settings page's "重新获取" button) and reports whether a client is
// configured afterwards.
func (g *GoogleService) RefreshConfig(ctx context.Context) (bool, error) {
	if g.Config == nil {
		return g.OAuth.Configured(), nil
	}
	err := g.Config.Refresh(ctx)
	return g.OAuth.Configured(), err
}

// Connect runs the Google consent flow in the browser and stores the account.
func (g *GoogleService) Connect(ctx context.Context) (store.Connection, error) {
	if g.Config != nil {
		g.Config.peek(ctx)
	}
	if !g.OAuth.Configured() {
		return store.Connection{}, ErrGoogleNotConfigured
	}
	r, err := oauthx.Listen()
	if err != nil {
		return store.Connection{}, err
	}
	defer r.Close()
	verifier, challenge := oauthx.NewPKCE()
	state := oauthx.RandomState()
	authURL := g.OAuth.AuthCodeURL(r.RedirectURI(), state, challenge)
	if g.Emit != nil {
		g.Emit(googleAuthURLEvent, authURL)
	}
	if err := g.Open(authURL); err != nil {
		return store.Connection{}, err
	}
	timeout := g.Timeout
	if timeout <= 0 {
		timeout = loginTimeout
	}
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	code, err := r.Wait(ctx, state)
	var authErr *oauthx.AuthError
	switch {
	case err == nil:
	case errors.As(err, &authErr) && authErr.Code == "access_denied":
		return store.Connection{}, ErrGoogleAccessDenied
	case errors.Is(err, context.DeadlineExceeded) && parent.Err() == nil:
		return store.Connection{}, ErrGoogleAuthTimeout
	default:
		return store.Connection{}, err
	}
	grant, err := g.OAuth.Exchange(ctx, code, verifier, r.RedirectURI())
	if err != nil {
		return store.Connection{}, err
	}
	if err := g.Secrets.Set(googleKeyPrefix+grant.Email, grant.RefreshToken); err != nil {
		return store.Connection{}, err
	}
	now := time.Now().Unix()
	if err := g.Store.UpsertConnection(ctx, "google", grant.Email, now); err != nil {
		return store.Connection{}, err
	}
	return store.Connection{Kind: "google", Account: grant.Email, CreatedAt: now}, nil
}

// Connections lists connected Google accounts.
func (g *GoogleService) Connections(ctx context.Context) ([]store.Connection, error) {
	return g.Store.Connections(ctx, "google")
}

// Disconnect forgets a Google account and its token.
func (g *GoogleService) Disconnect(ctx context.Context, account string) error {
	if err := g.Secrets.Delete(googleKeyPrefix + account); err != nil {
		return err
	}
	return g.Store.DeleteConnection(ctx, "google", account)
}

// Properties lists the Search Console properties the account can read.
func (g *GoogleService) Properties(ctx context.Context, account string) ([]google.Property, error) {
	tok, err := g.Tokens.AccessToken(ctx, account)
	if err != nil {
		return nil, err
	}
	return g.GSC.Properties(ctx, tok)
}

// GAProperties lists the GA4 properties the account can read.
func (g *GoogleService) GAProperties(ctx context.Context, account string) ([]google.GAProperty, error) {
	tok, err := g.Tokens.AccessToken(ctx, account)
	if err != nil {
		return nil, err
	}
	return g.GA.Properties(ctx, tok)
}
