// SPDX-License-Identifier: AGPL-3.0-or-later
package updates

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// sigDomain separates release signatures from any other use of the same
// Ed25519 key and versions the signed message's format.
const sigDomain = "sitemoment-update/v1"

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

// ReleaseMessage is the domain-separated message a release signature covers.
// Binding the tag and the asset's exact file name alongside its SHA-256
// digest means a signature cannot be moved to a different release: a
// correctly-signed old update zip cannot be re-served under a newer tag (a
// downgrade/replay dressed up as an update), and a signature made for one
// asset cannot be copied onto a same-named asset of a different release.
func ReleaseMessage(tag, name string, digest []byte) []byte {
	return []byte(sigDomain + "\n" + tag + "\n" + name + "\n" + hex.EncodeToString(digest))
}

// SignRelease signs tag, name and digest (see ReleaseMessage) with the
// base64 seed. It returns the base64 signature (the <asset>.sig file's
// contents).
func SignRelease(seed, tag, name string, digest []byte) (string, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(seed))
	if err != nil || len(b) != ed25519.SeedSize {
		return "", errors.New("updates: the signing key is not a base64 Ed25519 seed")
	}
	if len(digest) != 32 {
		return "", fmt.Errorf("updates: want a 32-byte SHA-256 digest, got %d bytes", len(digest))
	}
	sig := ed25519.Sign(ed25519.NewKeyFromSeed(b), ReleaseMessage(tag, name, digest))
	return base64.StdEncoding.EncodeToString(sig), nil
}

// VerifyRelease reports whether sig is pub's signature of tag, name and
// digest (see ReleaseMessage).
func VerifyRelease(pub ed25519.PublicKey, tag, name string, digest, sig []byte) bool {
	return ed25519.Verify(pub, ReleaseMessage(tag, name, digest), sig)
}

// ParseSignature decodes a .sig file (base64, surrounding space ignored).
func ParseSignature(file []byte) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(file)))
	if err != nil || len(b) != ed25519.SignatureSize {
		return nil, errors.New("updates: the .sig file is not a base64 Ed25519 signature")
	}
	return b, nil
}
