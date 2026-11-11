// SPDX-License-Identifier: AGPL-3.0-or-later
package collect

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

var ctx = context.Background()

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return st
}

func writeUser(t *testing.T, dir, name, manifest string) {
	t.Helper()
	d := filepath.Join(dir, name)
	if err := os.MkdirAll(d, 0o700); err != nil {
		t.Fatal(err)
	}
	for f, body := range map[string]string{"manifest.json": manifest, "schema.json": testSchema, "collector.js": "sm.done({});"} {
		if err := os.WriteFile(filepath.Join(d, f), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func builtinFS() fstest.MapFS {
	m := strings.NewReplacer(`"demo-serp"`, `"builtin-one"`, `"演示"`, `"内置"`).Replace(goodManifest)
	return fstest.MapFS{
		"builtin-one/manifest.json": {Data: []byte(m)},
		"builtin-one/schema.json":   {Data: []byte(testSchema)},
		"builtin-one/collector.js":  {Data: []byte("sm.done({});")},
		"embed.go":                  {Data: []byte("package collectors")},
	}
}

func TestCatalogBuiltinsAndApproval(t *testing.T) {
	st := openStore(t)
	dir := t.TempDir()
	writeUser(t, dir, "demo-serp", goodManifest)
	writeUser(t, dir, "wrong-dir", goodManifest)
	writeUser(t, dir, "taken", strings.NewReplacer(`"demo-serp"`, `"taken"`, `"name"`, `"tool":"builtin_one","name"`).Replace(goodManifest))
	writeUser(t, dir, "reserved", strings.NewReplacer(`"demo-serp"`, `"reserved"`, `"name"`, `"tool":"gsc_query","name"`).Replace(goodManifest))
	os.MkdirAll(filepath.Join(dir, "broken"), 0o700)
	c := &Catalog{Builtin: builtinFS(), Dir: dir, Store: st, Reserved: map[string]bool{"gsc_query": true}}
	if err := c.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	status := map[string]string{}
	for _, v := range c.List() {
		status[v.ID] = v.Status
	}
	want := map[string]string{"builtin-one": StatusEnabled, "demo-serp": StatusNeedsApproval, "wrong-dir": StatusInvalid,
		"taken": StatusInvalid, "reserved": StatusInvalid, "broken": StatusInvalid}
	for id, s := range want {
		if status[id] != s {
			t.Errorf("%s: %q, want %q (all %v)", id, status[id], s, status)
		}
	}
	if _, ok := c.Get("demo-serp"); ok || len(c.Enabled()) != 1 {
		t.Fatal("unapproved collector is runnable")
	}
	view := func(id string) CollectorView {
		for _, v := range c.List() {
			if v.ID == id {
				return v
			}
		}
		t.Fatalf("%s not in the list", id)
		return CollectorView{}
	}
	pending := view("demo-serp")
	if pending.SHA == "" {
		t.Fatal("no sha on the pending view")
	}
	// What the Collectors page showed no longer matches (e.g. a reload
	// raced the click): Approve must not apply the stale click to
	// whatever is pending now.
	if err := c.Approve(ctx, "demo-serp", pending.Version, pending.Hosts, "not-the-real-sha"); err == nil {
		t.Fatal("approved with a sha that does not match what is pending")
	}
	if _, ok := c.Get("demo-serp"); ok {
		t.Fatal("a rejected approval still enabled the collector")
	}
	if err := c.Approve(ctx, "demo-serp", pending.Version, pending.Hosts, pending.SHA); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get("demo-serp"); !ok {
		t.Fatal("approved collector not enabled")
	}
	if en := c.Enabled(); len(en) != 2 || en[0].ID != "builtin-one" {
		t.Fatalf("enabled order = %v", en)
	}
	// A new version that reads another host needs approval again.
	writeUser(t, dir, "demo-serp", strings.NewReplacer(`"1.0.0"`, `"1.1.0"`, `["www.example.com"]`, `["www.example.com","api.example.com"]`).Replace(goodManifest))
	if err := c.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get("demo-serp"); ok {
		t.Fatal("changed collector kept its old approval")
	}
	if err := c.Approve(ctx, "builtin-one", "1.0.0", []string{"www.example.com"}, "x"); err == nil {
		t.Fatal("approved a built-in")
	}
}

func TestCatalogBrokenBuiltinFails(t *testing.T) {
	fsys := builtinFS()
	fsys["builtin-one/manifest.json"] = &fstest.MapFile{Data: []byte(`{}`)}
	if err := (&Catalog{Builtin: fsys, Store: openStore(t)}).Reload(ctx); err == nil {
		t.Fatal("broken built-in loaded")
	}
}

func TestCatalogRejectsSymlinkedOrOversizedUserFiles(t *testing.T) {
	st := openStore(t)
	dir := t.TempDir()
	writeUser(t, dir, "symlinked", goodManifest)
	outside := filepath.Join(dir, "outside.js")
	if err := os.WriteFile(outside, []byte("sm.done({});"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "symlinked", "collector.js")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "symlinked", "collector.js")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	writeUser(t, dir, "oversized", goodManifest)
	if err := os.WriteFile(filepath.Join(dir, "oversized", "collector.js"), make([]byte, maxCollectorFileBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	c := &Catalog{Store: st, Dir: dir}
	if err := c.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	status := map[string]string{}
	for _, v := range c.List() {
		status[v.ID] = v.Status
	}
	if status["symlinked"] != StatusInvalid {
		t.Fatalf("symlinked collector.js: %q", status["symlinked"])
	}
	if status["oversized"] != StatusInvalid {
		t.Fatalf("oversized collector.js: %q", status["oversized"])
	}
}
