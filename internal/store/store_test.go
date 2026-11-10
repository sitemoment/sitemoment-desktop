// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"path/filepath"
	"testing"
)

var ctx = context.Background()

// open returns a migrated store in a temp dir.
func open(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestMigrateIsIdempotent(t *testing.T) {
	st := open(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	var n int
	if err := st.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&n); err != nil {
		t.Fatal(err)
	}
	files, _ := migrationFiles()
	if n != len(files) || n == 0 {
		t.Fatalf("schema_migrations has %d rows, want %d", n, len(files))
	}
	if _, err := st.db.ExecContext(ctx, "SELECT id, host FROM sites LIMIT 1"); err != nil {
		t.Fatalf("sites table missing: %v", err)
	}
}
