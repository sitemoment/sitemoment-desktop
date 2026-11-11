// SPDX-License-Identifier: AGPL-3.0-or-later
package updates

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// GenerateKey returns a new Ed25519 key pair for update signatures, both
// standard base64: the public key is built into the app (UPDATE_PUBLIC_KEY),
// the private key's 32-byte seed is the release workflow's secret
// (UPDATE_SIGNING_KEY).
func GenerateKey() (pub, seed string, err error) {
	pk, sk, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(pk), base64.StdEncoding.EncodeToString(sk.Seed()), nil
}

// ParsePublicKey decodes a base64 Ed25519 public key.
func ParsePublicKey(b64 string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, errors.New("updates: the public key is not a base64 Ed25519 key")
	}
	return ed25519.PublicKey(b), nil
}

// SignDigest signs an artifact's SHA-256 digest with the base64 seed: the
// raw 32-byte digest is the message, which is what Wails' updater checks for
// SignatureAlgo "ed25519". It returns the base64 signature (the .sig file).
func SignDigest(seed string, digest []byte) (string, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(seed))
	if err != nil || len(b) != ed25519.SeedSize {
		return "", errors.New("updates: the signing key is not a base64 Ed25519 seed")
	}
	if len(digest) != 32 {
		return "", fmt.Errorf("updates: want a 32-byte SHA-256 digest, got %d bytes", len(digest))
	}
	return base64.StdEncoding.EncodeToString(ed25519.Sign(ed25519.NewKeyFromSeed(b), digest)), nil
}

// ParseSignature decodes a .sig file (base64, surrounding space ignored).
func ParseSignature(file []byte) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(file)))
	if err != nil || len(b) != ed25519.SignatureSize {
		return nil, errors.New("updates: the .sig file is not a base64 Ed25519 signature")
	}
	return b, nil
}
