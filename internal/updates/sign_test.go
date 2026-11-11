// SPDX-License-Identifier: AGPL-3.0-or-later
package updates

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestSignReleaseBindsTagAndAssetName(t *testing.T) {
	pub, seed, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("artifact"))
	sig, err := SignRelease(seed, "v1.2.3", "SiteMoment-macos-universal.zip", digest[:])
	if err != nil {
		t.Fatal(err)
	}
	pk, err := ParsePublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := ParseSignature([]byte(sig + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyRelease(pk, "v1.2.3", "SiteMoment-macos-universal.zip", digest[:], raw) {
		t.Fatal("a correctly-signed release did not verify")
	}
	// The signature is bound to the exact tag, asset name and digest: any of
	// the three changing must invalidate it (replay / asset-swap protection).
	if VerifyRelease(pk, "v1.2.4", "SiteMoment-macos-universal.zip", digest[:], raw) {
		t.Fatal("verified under a different tag")
	}
	if VerifyRelease(pk, "v1.2.3", "SiteMoment-windows-amd64.zip", digest[:], raw) {
		t.Fatal("verified under a different asset name")
	}
	other := sha256.Sum256([]byte("different"))
	if VerifyRelease(pk, "v1.2.3", "SiteMoment-macos-universal.zip", other[:], raw) {
		t.Fatal("verified under a different digest")
	}
	if _, err := SignRelease(base64.StdEncoding.EncodeToString([]byte("short")), "v1.2.3", "a.zip", digest[:]); err == nil {
		t.Fatal("accepted a bad seed")
	}
	if _, err := SignRelease(seed, "v1.2.3", "a.zip", []byte("short")); err == nil {
		t.Fatal("accepted a non-SHA-256-sized digest")
	}
	if _, err := ParseSignature([]byte("abc")); err == nil {
		t.Fatal("accepted a bad signature")
	}
}
