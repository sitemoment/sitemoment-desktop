// SPDX-License-Identifier: AGPL-3.0-or-later

// Package oauthx holds the pieces shared by SiteMoment sign-in and Google
// authorization: PKCE (RFC 7636) and a loopback receiver for the code.
package oauthx

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

func random(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// NewPKCE returns a random verifier and its S256 challenge.
func NewPKCE() (verifier, challenge string) {
	verifier = random(32)
	return verifier, Challenge(verifier)
}

// Challenge is base64url(sha256(verifier)) without padding.
func Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// RandomState returns a random OAuth state value.
func RandomState() string { return random(24) }
