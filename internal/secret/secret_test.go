// SPDX-License-Identifier: AGPL-3.0-or-later
package secret

import (
	"errors"
	"testing"

	"github.com/zalando/go-keyring"
)

func exercise(t *testing.T, s Store) {
	t.Helper()
	if _, err := s.Get("k"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get missing: %v", err)
	}
	if err := s.Set("k", "v1"); err != nil {
		t.Fatal(err)
	}
	if v, err := s.Get("k"); err != nil || v != "v1" {
		t.Fatalf("Get = %q, %v", v, err)
	}
	if err := s.Delete("k"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("k"); err != nil {
		t.Fatalf("Delete missing should be a no-op: %v", err)
	}
	if _, err := s.Get("k"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get deleted: %v", err)
	}
}

func TestMemory(t *testing.T) { exercise(t, NewMemory()) }

func TestKeyring(t *testing.T) {
	keyring.MockInit()
	exercise(t, Keyring{Service: "SiteMoment-test"})
}
