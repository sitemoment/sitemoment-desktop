// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"context"
	"fmt"
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

	mu     sync.Mutex
	flight *googleConfigFlight // set while a fetch is in progress; nil otherwise
}

// googleConfigFlight is the one fetch shared by every concurrent Refresh
// call: done closes when it finishes, with err holding its result.
type googleConfigFlight struct {
	done chan struct{}
	err  error
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
// override is set.
//
// Refresh is single-flight: when a fetch is already in progress, a second
// caller does not start another one or block on a mutex (which would make
// it miss its own ctx deadline) - it just waits for the in-flight fetch's
// result, or its own ctx, whichever comes first. The in-flight fetch keeps
// running regardless (bounded by its own initiator's ctx and, in any case,
// by cloud.Client.DesktopConfig's internal timeout), so a caller who gave
// up early never cancels it out from under anyone still waiting.
func (c *GoogleConfig) Refresh(ctx context.Context) error {
	if c.OAuth.ClientID != "" {
		return nil
	}
	c.mu.Lock()
	if f := c.flight; f != nil {
		c.mu.Unlock()
		select {
		case <-f.done:
			return f.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f := &googleConfigFlight{done: make(chan struct{})}
	c.flight = f
	c.mu.Unlock()

	f.err = c.fetch(ctx)
	close(f.done)

	c.mu.Lock()
	if c.flight == f {
		c.flight = nil
	}
	c.mu.Unlock()
	return f.err
}

// fetch does the actual network call and applies the result; only the
// goroutine that started a flight (see Refresh) runs it.
func (c *GoogleConfig) fetch(ctx context.Context) error {
	cfg, err := c.Cloud.DesktopConfig(ctx)
	if err != nil {
		return err
	}
	return c.apply(ctx, cfg.Google.ClientID, cfg.Google.ClientSecret)
}

// apply caches id/secret and switches OAuth to them, unless id is empty: an
// empty response (the backend has nothing configured, or a transient blip)
// must never wipe a client that is already cached and working.
//
// The two settings are written as a pair - secret first, then id - because
// the local settings store has no transaction helper; if the second write
// fails, the first is rolled back so a crash or a write error between the
// two can never leave a cached id and secret that do not belong together.
func (c *GoogleConfig) apply(ctx context.Context, id, secret string) error {
	if id == "" {
		return nil
	}
	oldSecret, err := c.Store.Setting(ctx, settingGoogleClientSecret)
	if err != nil {
		return err
	}
	if err := c.Store.SetSetting(ctx, settingGoogleClientSecret, secret); err != nil {
		return err
	}
	if err := c.Store.SetSetting(ctx, settingGoogleClientID, id); err != nil {
		if restoreErr := c.Store.SetSetting(ctx, settingGoogleClientSecret, oldSecret); restoreErr != nil {
			return fmt.Errorf("%w (and could not restore the previous cached secret: %v)", err, restoreErr)
		}
		return err
	}
	c.OAuth.SetClient(id, secret)
	return nil
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
