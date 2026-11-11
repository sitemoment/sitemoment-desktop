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

// ErrGoogleNotConfigured means the build has no Google OAuth client ID.
var ErrGoogleNotConfigured = errors.New("Google 连接还没有配置：缺少 OAuth client ID")

// GoogleTokens turns a connected account into access tokens. It is used by
// the sync and is deliberately not a frontend service.
type GoogleTokens struct {
	OAuth   *google.OAuth
	Secrets secret.Store
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
}

// Configured reports whether Google connection is available in this build.
func (g *GoogleService) Configured() bool { return g.OAuth.Configured() }

// Connect runs the Google consent flow in the browser and stores the account.
func (g *GoogleService) Connect(ctx context.Context) (store.Connection, error) {
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
	if err := g.Open(g.OAuth.AuthCodeURL(r.RedirectURI(), state, challenge)); err != nil {
		return store.Connection{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, loginTimeout)
	defer cancel()
	code, err := r.Wait(ctx, state)
	if err != nil {
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
