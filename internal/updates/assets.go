// SPDX-License-Identifier: AGPL-3.0-or-later

// Package updates keeps SiteMoment Desktop up to date from GitHub Releases
// with Wails' updater (pkg/updater; this package and cmd/* are the only
// places that import it): the release asset names, the checksum and
// signature checks around the GitHub provider, and the update service the
// Settings page and the update bar use. It never opens a window of its own.
package updates

import (
	"github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/github"
)

// Repo is where releases are published.
const Repo = "sitemoment/sitemoment-desktop"

// Release asset names. They carry no version, so
// https://github.com/<Repo>/releases/latest/download/<name> always points at
// the newest release (the download page links there). The release workflow
// uses the same names.
const (
	MacInstaller     = "SiteMoment-macos-universal.dmg"
	MacUpdate        = "SiteMoment-macos-universal.zip" // SiteMoment.app, the only top-level entry
	WindowsInstaller = "SiteMoment-windows-amd64-setup.exe"
	WindowsUpdate    = "SiteMoment-windows-amd64.zip" // sitemoment-desktop.exe, the only entry
	Checksums        = "SHA256SUMS"                   // "<sha256>  <name>" per asset (shasum -a 256)
)

// UpdateAsset is the asset the app replaces itself with on goos/goarch, or
// "" where there is no self-update.
func UpdateAsset(goos, goarch string) string {
	switch {
	case goos == "darwin":
		return MacUpdate
	case goos == "windows" && goarch == "amd64":
		return WindowsUpdate
	}
	return ""
}

// Matcher picks UpdateAsset by exact name. The provider's default matcher
// takes the first asset whose name merely contains the platform and arch.
func Matcher(req updater.CheckRequest, assets []github.ReleaseAsset) int {
	want := UpdateAsset(req.Platform, req.Arch)
	for i, a := range assets {
		if want != "" && a.Name == want {
			return i
		}
	}
	return -1
}
