// SPDX-License-Identifier: AGPL-3.0-or-later

// Package secret keeps tokens and API keys in the OS keychain (macOS
// Keychain, Windows Credential Manager), never in the database or logs.
package secret

import (
	"errors"
	"sync"

	"github.com/zalando/go-keyring"
)

// ErrNotFound means no secret is stored under the key.
var ErrNotFound = errors.New("secret: not found")

// Store reads and writes secrets by key.
type Store interface {
	Get(key string) (string, error)
	Set(key, value string) error
	Delete(key string) error
}

// Keyring stores secrets in the OS keychain under Service.
type Keyring struct{ Service string }

func (k Keyring) Get(key string) (string, error) {
	v, err := keyring.Get(k.Service, key)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	return v, err
}

func (k Keyring) Set(key, value string) error { return keyring.Set(k.Service, key, value) }

func (k Keyring) Delete(key string) error {
	if err := keyring.Delete(k.Service, key); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return err
	}
	return nil
}

// Memory is an in-process Store for tests.
type Memory struct {
	mu sync.Mutex
	m  map[string]string
}

func NewMemory() *Memory { return &Memory{m: map[string]string{}} }

func (m *Memory) Get(key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.m[key]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (m *Memory) Set(key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.m[key] = value
	return nil
}

func (m *Memory) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.m, key)
	return nil
}
