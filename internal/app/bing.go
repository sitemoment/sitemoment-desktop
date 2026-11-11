// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/bing"
	"github.com/sitemoment/sitemoment-desktop/internal/secret"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

const bingKeyPrefix = "bing:"

// BingKeys reads saved Bing API keys from the keychain by name. It is used
// by the sync and the keyword workspace and is not a frontend service.
type BingKeys struct{ Secrets secret.Store }

// BingKey implements bingsync.Keys.
func (b *BingKeys) BingKey(account string) (string, error) {
	k, err := b.Secrets.Get(bingKeyPrefix + account)
	if errors.Is(err, secret.ErrNotFound) {
		return "", bing.ErrInvalidKey
	}
	return k, err
}

// BingService manages Bing Webmaster API keys and lists their sites.
type BingService struct {
	Bing    *bing.Client
	Store   *store.Store
	Secrets secret.Store
}

// bingKeyName derives a keychain/connection name from a key: its last four
// characters (so a human can recognize it) plus the first six hex digits of
// its SHA-256, so two keys that happen to share the same last four
// characters still get distinct names instead of overwriting each other.
// Existing names already stored keep working either way, since the name is
// only ever used as an opaque lookup key.
func bingKeyName(key string) string {
	sum := sha256.Sum256([]byte(key))
	return "bing-" + key[len(key)-4:] + "-" + hex.EncodeToString(sum[:])[:6]
}

// AddKey checks the key with Bing, keeps it in the keychain under a name
// derived from the key (see bingKeyName) and records the connection. Only
// the name is ever stored in the database.
func (b *BingService) AddKey(ctx context.Context, key string) (store.Connection, error) {
	key = strings.TrimSpace(key)
	if len(key) < 8 || strings.ContainsAny(key, " \t\r\n") {
		return store.Connection{}, fmt.Errorf("这不像是 Bing Webmaster 的 API key: %w", bing.ErrInvalidKey)
	}
	if _, err := b.Bing.Sites(ctx, key); err != nil {
		return store.Connection{}, err
	}
	name := bingKeyName(key)
	if err := b.Secrets.Set(bingKeyPrefix+name, key); err != nil {
		return store.Connection{}, err
	}
	now := time.Now().Unix()
	if err := b.Store.UpsertConnection(ctx, "bing", name, now); err != nil {
		return store.Connection{}, err
	}
	return store.Connection{Kind: "bing", Account: name, CreatedAt: now}, nil
}

// Keys lists the saved keys by name.
func (b *BingService) Keys(ctx context.Context) ([]store.Connection, error) {
	return b.Store.Connections(ctx, "bing")
}

// RemoveKey forgets a key: the connection row goes first, so a failed
// keychain delete never leaves a connection pointing at a secret that
// cannot be looked up, let alone deleted, again.
func (b *BingService) RemoveKey(ctx context.Context, account string) error {
	if err := b.Store.DeleteConnection(ctx, "bing", account); err != nil {
		return err
	}
	return b.Secrets.Delete(bingKeyPrefix + account)
}

// Sites lists the sites the saved key can read.
func (b *BingService) Sites(ctx context.Context, account string) ([]bing.Site, error) {
	key, err := (&BingKeys{Secrets: b.Secrets}).BingKey(account)
	if err != nil {
		return nil, err
	}
	return b.Bing.Sites(ctx, key)
}
