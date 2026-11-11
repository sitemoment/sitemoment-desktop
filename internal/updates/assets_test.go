// SPDX-License-Identifier: AGPL-3.0-or-later
package updates

import (
	"testing"

	"github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/github"
)

func TestMatcherTakesTheExactUpdateAsset(t *testing.T) {
	assets := []github.ReleaseAsset{{Name: MacInstaller}, {Name: WindowsInstaller}, {Name: "SiteMoment-darwin-arm64-debug.zip"}, {Name: MacUpdate}, {Name: WindowsUpdate}, {Name: Checksums}}
	for _, c := range []struct {
		os, arch string
		want     int
	}{{"darwin", "arm64", 3}, {"darwin", "amd64", 3}, {"windows", "amd64", 4}, {"windows", "arm64", -1}, {"linux", "amd64", -1}} {
		if got := Matcher(updater.CheckRequest{Platform: c.os, Arch: c.arch}, assets); got != c.want {
			t.Errorf("%s/%s = %d, want %d", c.os, c.arch, got, c.want)
		}
	}
}
