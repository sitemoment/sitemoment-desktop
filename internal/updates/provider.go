// SPDX-License-Identifier: AGPL-3.0-or-later
package updates

import (
	"context"
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
	// Signed requires every release to ship <asset>.sig (an Ed25519
	// signature over the asset's SHA-256, see SignDigest). Set it exactly
	// when the app has a public key: the updater then refuses unsigned
	// releases.
	Signed bool
	HTTP   *http.Client
	// APIBase and DownloadBase replace https://api.github.com and
	// https://github.com (tests).
	APIBase, DownloadBase string
}

// NewProvider returns the GitHub Releases provider with the checks the
// updater relies on: a release must list the update asset's SHA-256 in
// SHA256SUMS (no digest, no update), and with Signed its .sig as well.
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
	if !p.cfg.Signed {
		return rel, nil
	}
	tag, _ := rel.Metadata["github.release.tag"].(string)
	url := fmt.Sprintf("%s/%s/releases/download/%s/%s.sig", strings.TrimRight(p.cfg.DownloadBase, "/"), p.cfg.Repo, tag, rel.Artifact.Filename)
	sig, err := p.signature(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("updates: signature of %s %s: %w", rel.Version, rel.Artifact.Filename, err)
	}
	rel.Verification.SignatureAlgo = "ed25519"
	rel.Verification.Signature = sig
	return rel, nil
}

func (p *checked) signature(ctx context.Context, url string) ([]byte, error) {
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
