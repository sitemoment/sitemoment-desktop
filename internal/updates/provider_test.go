// SPDX-License-Identifier: AGPL-3.0-or-later
package updates

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/updater"
)

var ctx = context.Background()

// fakeGitHub serves one release: the API, the update asset (a zip with one
// top-level entry, like SiteMoment.app), SHA256SUMS and the .sig sidecar.
type fakeGitHub struct {
	srv      *httptest.Server
	tag      string
	payload  []byte
	sums     string // SHA256SUMS body; "" = the correct line
	sig      string // .sig body; "" = 404
	apiCalls atomic.Int64
}

func newFakeGitHub(t *testing.T, tag string) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{tag: tag, payload: appZip(t)}
	asset := UpdateAsset(runtime.GOOS, runtime.GOARCH)
	if asset == "" {
		t.Skip("no self-update on " + runtime.GOOS + "/" + runtime.GOARCH)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/"+Repo+"/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		f.apiCalls.Add(1)
		u := f.srv.URL
		json.NewEncoder(w).Encode(map[string]any{
			"tag_name": f.tag, "name": "SiteMoment " + f.tag, "body": "- 新功能", "published_at": "2026-11-20T08:00:00Z",
			"html_url": "https://github.com/" + Repo + "/releases/tag/" + f.tag,
			"assets": []map[string]any{
				{"id": 1, "name": WindowsInstaller, "size": 10, "url": u + "/asset/installer"},
				{"id": 2, "name": asset, "size": len(f.payload), "url": u + "/asset/update"},
				{"id": 3, "name": Checksums, "size": 100, "url": u + "/asset/sums"},
			},
		})
	})
	mux.HandleFunc("/asset/update", func(w http.ResponseWriter, r *http.Request) { w.Write(f.payload) })
	mux.HandleFunc("/asset/sums", func(w http.ResponseWriter, r *http.Request) {
		if f.sums != "" {
			fmt.Fprint(w, f.sums)
			return
		}
		sum := sha256.Sum256(f.payload)
		fmt.Fprintf(w, "%s  %s\n%s  %s\n", strings.Repeat("0", 64), WindowsInstaller, hex.EncodeToString(sum[:]), asset)
	})
	mux.HandleFunc("/"+Repo+"/releases/download/", func(w http.ResponseWriter, r *http.Request) {
		if f.sig == "" || r.URL.Path != "/"+Repo+"/releases/download/"+f.tag+"/"+asset+".sig" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintln(w, f.sig)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// appZip is a minimal SiteMoment.app in a zip, as the release workflow makes it.
func appZip(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{
		"SiteMoment.app/Contents/Info.plist":               `<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleExecutable</key><string>sitemoment-desktop</string></dict></plist>`,
		"SiteMoment.app/Contents/MacOS/sitemoment-desktop": "binary",
	} {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		h.SetMode(0o755)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(body))
	}
	zw.Close()
	return buf.Bytes()
}

// check runs a Check against the fake GitHub, optionally with a compiled-in
// public key (nil = no key: integrity-only, digest-checked but
// unauthenticated).
func check(t *testing.T, f *fakeGitHub, pub ed25519.PublicKey) (*updater.Release, error) {
	t.Helper()
	p, err := NewProvider(ProviderConfig{PublicKey: pub, APIBase: f.srv.URL, DownloadBase: f.srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	return p.Check(ctx, updater.CheckRequest{CurrentVersion: "1.0.0", Platform: runtime.GOOS, Arch: runtime.GOARCH})
}

// TestProviderTakesTheDigestFromSHA256SUMS is the "no key" mode (no
// UPDATE_PUBLIC_KEY compiled in): the provider still requires and reports
// the SHA-256 from SHA256SUMS, but nothing here authenticates that GitHub's
// response came from this project's release workflow rather than, say, a
// compromised or proxied distribution channel. Documented as integrity-only,
// not authenticity: see RELEASING.md.
func TestProviderTakesTheDigestFromSHA256SUMS(t *testing.T) {
	f := newFakeGitHub(t, "v1.1.0")
	rel, err := check(t, f, nil)
	sum := sha256.Sum256(f.payload)
	if err != nil || rel == nil || rel.Version != "1.1.0" || rel.Artifact.Filename != UpdateAsset(runtime.GOOS, runtime.GOARCH) ||
		rel.Verification == nil || !bytes.Equal(rel.Verification.Digest, sum[:]) || rel.Verification.Signature != nil {
		t.Fatalf("release = %+v, %v", rel, err)
	}
	f.tag = "v1.0.0"
	if rel, err := check(t, f, nil); err != nil || rel != nil {
		t.Fatalf("same version = %+v, %v", rel, err)
	}
}

func TestProviderRefusesAReleaseWithoutItsChecksum(t *testing.T) {
	f := newFakeGitHub(t, "v1.1.0")
	f.sums = strings.Repeat("0", 64) + "  other.zip\n"
	if _, err := check(t, f, nil); err == nil || !strings.Contains(err.Error(), "no SHA-256") {
		t.Fatalf("err = %v", err)
	}
}

// TestProviderAcceptsAValidSignature is the "key compiled in" mode: the
// provider itself verifies <asset>.sig (SignRelease) before Check returns,
// and never forwards a Signature to Wails — Wails only ever sees a
// digest-only Verification (service.go leaves its own Config.PublicKey
// empty), so the release this provider hands back has already been
// authenticated by the time the Wails updater sees it.
func TestProviderAcceptsAValidSignature(t *testing.T) {
	f := newFakeGitHub(t, "v1.1.0")
	pub, seed, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	pk, err := ParsePublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	name := UpdateAsset(runtime.GOOS, runtime.GOARCH)
	sum := sha256.Sum256(f.payload)
	if f.sig, err = SignRelease(seed, f.tag, name, sum[:]); err != nil {
		t.Fatal(err)
	}
	rel, err := check(t, f, pk)
	if err != nil || rel == nil || rel.Verification.Signature != nil || rel.Verification.SignatureAlgo != "" {
		t.Fatalf("release = %+v, %v", rel, err)
	}
}

// TestProviderRequiresTheSignatureWhenAKeyIsCompiledIn is the fail-closed
// half: missing or garbled .sig => no update offered.
func TestProviderRequiresTheSignatureWhenAKeyIsCompiledIn(t *testing.T) {
	f := newFakeGitHub(t, "v1.1.0")
	pub, _, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	pk, err := ParsePublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := check(t, f, pk); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("no .sig: %v", err)
	}
	f.sig = "not base64!"
	if _, err := check(t, f, pk); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("garbled .sig: %v", err)
	}
}

// TestProviderRejectsASignatureReplayedUnderADifferentTag: the signed
// message binds the exact tag, so a correctly-signed old release cannot be
// re-served as a newer one (a downgrade/replay dressed up as an update).
func TestProviderRejectsASignatureReplayedUnderADifferentTag(t *testing.T) {
	f := newFakeGitHub(t, "v1.1.0")
	pub, seed, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	pk, err := ParsePublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	name := UpdateAsset(runtime.GOOS, runtime.GOARCH)
	sum := sha256.Sum256(f.payload)
	// Signed for v1.0.0's release of this exact zip, but served as v1.1.0.
	if f.sig, err = SignRelease(seed, "v1.0.0", name, sum[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := check(t, f, pk); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("replay under a new tag: %v", err)
	}
}

// TestProviderRejectsASignatureForADifferentAssetName: a signature made for
// one asset cannot be copied onto a same-named asset of a different release.
func TestProviderRejectsASignatureForADifferentAssetName(t *testing.T) {
	f := newFakeGitHub(t, "v1.1.0")
	pub, seed, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	pk, err := ParsePublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(f.payload)
	if f.sig, err = SignRelease(seed, f.tag, "SiteMoment-windows-amd64.zip", sum[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := check(t, f, pk); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("wrong asset name: %v", err)
	}
}
