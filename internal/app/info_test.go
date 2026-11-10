// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInfoCountsWalAndShm(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "t.db")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.WriteFile(db+suffix, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	i := &InfoService{Version: "v", DataDir: dir, DBPath: db}
	info := i.Get()
	if info.DBBytes != 3 {
		t.Fatalf("DBBytes = %d, want 3 (db + wal + shm)", info.DBBytes)
	}
}
