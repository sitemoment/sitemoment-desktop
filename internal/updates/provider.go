// SPDX-License-Identifier: AGPL-3.0-or-later
package updates

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/github"
)

// ProviderConfig configures NewProvider.
type ProviderConfig struct {
	Repo string // "owner/repo"; Repo when empty
	// PublicKey, when set, makes the provider fail closed: every release
	// must ship a valid <asset>.sig (SignRelease), or Check refuses it (no
	// update offered). Verification of that signature happens entirely in
	// this package — the Release this provider returns never carries a
	// Signature, only a digest-only Verification (DigestAlgo/Digest), so
	// Wails' own updater.Config.PublicKey must stay empty (see service.go):
	// the whole point of checking the signature here, against a message
	// bound to the release's tag and asset name, is that Wails re-verifying
	// a bare digest-signature pair would not catch a replay under a
	// different tag or a signature copied onto a different asset.
	//
	// When nil, releases are still required to carry a SHA-256 in
	// SHA256SUMS, but nothing authenticates that the response came from
	// this project's release workflow: integrity-only, not authenticity
	// (see RELEASING.md).
	PublicKey ed25519.PublicKey
	HTTP      *http.Client
	// APIBase and DownloadBase replace https://api.github.com and
	// https://github.com (tests).
	APIBase, DownloadBase string
}

// NewProvider returns the GitHub Releases provider with the checks the
// updater relies on: a release must list the update asset's SHA-256 in
// SHA256SUMS (no digest, no update), and with a PublicKey a valid .sig too.
func NewProvider(c ProviderConfig) (updater.Provider, error) {
	if c.Repo == "" {
		c.Repo = Repo
	}
	if c.HTTP == nil {
		c.HTTP = &http.Client{}
	}
	if c.DownloadBase == "" {
		c.DownloadBase = "https://github.com"
	}
	inner, err := github.New(github.Config{Repository: c.Repo, BaseURL: c.APIBase, AssetMatcher: Matcher, ChecksumAsset: Checksums, HTTPClient: c.HTTP})
	if err != nil {
		return nil, err
	}
	return &checked{inner: inner, cfg: c}, nil
}

type checked struct {
	inner updater.Provider
	cfg   ProviderConfig
}

// Name keeps the inner provider's name: the updater routes Download by it.
func (p *checked) Name() string { return p.inner.Name() }

func (p *checked) Check(ctx context.Context, req updater.CheckRequest) (*updater.Release, error) {
	rel, err := p.inner.Check(ctx, req)
	if err != nil || rel == nil {
		return rel, err
	}
	if rel.Verification == nil || len(rel.Verification.Digest) == 0 {
		return nil, fmt.Errorf("updates: release %s has no SHA-256 for %s in %s", rel.Version, rel.Artifact.Filename, Checksums)
	}
	if p.cfg.PublicKey == nil {
		// Integrity-only: no key compiled in, nothing to authenticate.
		return rel, nil
	}
	tag, _ := rel.Metadata["github.release.tag"].(string)
	url := fmt.Sprintf("%s/%s/releases/download/%s/%s.sig", strings.TrimRight(p.cfg.DownloadBase, "/"), p.cfg.Repo, tag, rel.Artifact.Filename)
	sig, err := p.fetchSignature(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("updates: fetching the signature of %s %s: %w", rel.Version, rel.Artifact.Filename, err)
	}
	if !VerifyRelease(p.cfg.PublicKey, tag, rel.Artifact.Filename, rel.Verification.Digest, sig) {
		return nil, fmt.Errorf("updates: the signature of %s %s does not verify", rel.Version, rel.Artifact.Filename)
	}
	// Verified above; Wails gets the digest-only Verification the inner
	// provider already produced, nothing more.
	return rel, nil
}

func (p *checked) fetchSignature(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	res, err := p.cfg.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 1024))
	if err != nil {
		return nil, err
	}
	return ParseSignature(b)
}

func (p *checked) Download(ctx context.Context, rel *updater.Release, dst io.Writer, onProgress func(written, total int64)) error {
	return p.inner.Download(ctx, rel, dst, onProgress)
}
