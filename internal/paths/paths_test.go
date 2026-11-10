// SPDX-License-Identifier: AGPL-3.0-or-later
package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDataDirFromEnv(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	t.Setenv("SITEMOMENT_DATA_DIR", dir)
	got, err := DataDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Fatalf("DataDir = %q, want %q", got, dir)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Fatalf("dir not created: %v", err)
	}
}
