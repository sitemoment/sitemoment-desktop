// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"context"
	"sync"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/cloud"
	"github.com/sitemoment/sitemoment-desktop/internal/google"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

// Settings keys for the cached Google OAuth client (owner decision
// 2026-11-11: central configuration in the admin console, because other
// people use the app and may connect multiple Search Console accounts). The
// secret is kept here rather than in internal/secret because, per Google's
// own guidance, a desktop client's secret is not confidential; it is still
// kept out of logs like any other credential.
const (
	settingGoogleClientID     = "google.clientId"
	settingGoogleClientSecret = "google.clientSecret"
)

// googleConfigPeekTimeout bounds the on-demand remote fetch made by
// Configured/Connect/Properties/GAProperties and token refresh when no
// client is known yet, so a cold start without connectivity fails fast
// instead of hanging the caller; a var so tests can shorten it.
var googleConfigPeekTimeout = 3 * time.Second

// GoogleConfig resolves the Google OAuth client: an env/ldflags override
// set at construction (google.OAuth.ClientID) always wins and is never
// touched here; otherwise the client comes from the SiteMoment backend's
// central configuration (GET /v1/desktop/config), fetched in the
// background at startup and cached in the local settings table so the app
// still works offline. If the remote client id changes, GoogleConfig keeps
// using the new one: a refresh token issued to the old client then fails
// against Google as invalid_grant (google.ErrReauth), which the existing
// sync/health error messages already turn into "Google 授权已失效，请在设置里
// 重新连接" - no separate "needs reconnect" bookkeeping is needed.
type GoogleConfig struct {
	OAuth *google.OAuth
	Cloud *cloud.Client
	Store *store.Store

	mu sync.Mutex // serializes Refresh so concurrent callers share one fetch's cost
}

// Load applies the last cached remote config, if any, without touching the
// network. Call once at startup, after OAuth is constructed with any
// env/ldflags override already set, and before the background Refresh.
func (c *GoogleConfig) Load(ctx context.Context) {
	if c.OAuth.ClientID != "" {
		return // env/ldflags override: never consult the backend
	}
	id, err := c.Store.Setting(ctx, settingGoogleClientID)
	if err != nil || id == "" {
		return
	}
	secret, _ := c.Store.Setting(ctx, settingGoogleClientSecret)
	c.OAuth.SetClient(id, secret)
}

// Refresh fetches the backend's current configuration and applies it,
// caching the result locally; a no-op returning nil when an env/ldflags
// override is set. Concurrent calls share one in-flight fetch's cost
// (serialized by mu) rather than hitting the backend once per caller.
func (c *GoogleConfig) Refresh(ctx context.Context) error {
	if c.OAuth.ClientID != "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	cfg, err := c.Cloud.DesktopConfig(ctx)
	if err != nil {
		return err
	}
	c.OAuth.SetClient(cfg.Google.ClientID, cfg.Google.ClientSecret)
	if err := c.Store.SetSetting(ctx, settingGoogleClientID, cfg.Google.ClientID); err != nil {
		return err
	}
	return c.Store.SetSetting(ctx, settingGoogleClientSecret, cfg.Google.ClientSecret)
}

// peek makes a short, best-effort attempt to resolve the client when none
// is known yet; its error is deliberately dropped by every caller (they
// each have their own, more informative error for "still not configured"),
// so a timeout or an offline backend just falls through to that.
func (c *GoogleConfig) peek(ctx context.Context) {
	if c.OAuth.Configured() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, googleConfigPeekTimeout)
	defer cancel()
	c.Refresh(ctx)
}
