// SPDX-License-Identifier: AGPL-3.0-or-later

// Package paths locates SiteMoment's data directory.
package paths

import (
	"os"
	"path/filepath"
)

// DataDir returns (and creates) the data directory: $SITEMOMENT_DATA_DIR, or
// "SiteMoment" under the OS config dir (~/Library/Application Support on
// macOS, %AppData% on Windows).
func DataDir() (string, error) {
	dir := os.Getenv("SITEMOMENT_DATA_DIR")
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(base, "SiteMoment")
	}
	return dir, os.MkdirAll(dir, 0o700)
}
