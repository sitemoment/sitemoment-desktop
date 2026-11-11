// SPDX-License-Identifier: AGPL-3.0-or-later
package updates

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestSignDigestIsWhatTheUpdaterVerifies(t *testing.T) {
	pub, seed, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("artifact"))
	sig, err := SignDigest(seed, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	pk, err := ParsePublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := ParseSignature([]byte(sig + "\n"))
	if err != nil || !ed25519.Verify(pk, digest[:], raw) {
		t.Fatalf("verify: %v", err)
	}
	if _, err := SignDigest(base64.StdEncoding.EncodeToString([]byte("short")), digest[:]); err == nil {
		t.Fatal("accepted a bad seed")
	}
	if _, err := ParseSignature([]byte("abc")); err == nil {
		t.Fatal("accepted a bad signature")
	}
}
