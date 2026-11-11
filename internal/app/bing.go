// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"context"
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

// AddKey checks the key with Bing, keeps it in the keychain under the name
// "bing-<last 4 characters>" and records the connection. Only the name is
// ever stored in the database.
func (b *BingService) AddKey(ctx context.Context, key string) (store.Connection, error) {
	key = strings.TrimSpace(key)
	if len(key) < 8 || strings.ContainsAny(key, " \t\r\n") {
		return store.Connection{}, fmt.Errorf("这不像是 Bing Webmaster 的 API key: %w", bing.ErrInvalidKey)
	}
	if _, err := b.Bing.Sites(ctx, key); err != nil {
		return store.Connection{}, err
	}
	name := "bing-" + key[len(key)-4:]
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

// RemoveKey forgets a key.
func (b *BingService) RemoveKey(ctx context.Context, account string) error {
	if err := b.Secrets.Delete(bingKeyPrefix + account); err != nil {
		return err
	}
	return b.Store.DeleteConnection(ctx, "bing", account)
}

// Sites lists the sites the saved key can read.
func (b *BingService) Sites(ctx context.Context, account string) ([]bing.Site, error) {
	key, err := (&BingKeys{Secrets: b.Secrets}).BingKey(account)
	if err != nil {
		return nil, err
	}
	return b.Bing.Sites(ctx, key)
}
