// SPDX-License-Identifier: AGPL-3.0-or-later
package collectors

import (
	"io/fs"
	"testing"

	"github.com/sitemoment/sitemoment-desktop/internal/collect"
)

func TestBuiltinsLoad(t *testing.T) {
	dirs, err := fs.ReadDir(FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	tools := map[string]bool{}
	for _, d := range dirs {
		c, err := collect.Load(FS, d.Name(), true)
		if err != nil {
			t.Fatalf("%s: %v", d.Name(), err)
		}
		tools[c.Tool] = true
	}
	if !tools["google_serp"] || !tools["google_trends"] || len(tools) != 2 {
		t.Fatalf("tools = %v", tools)
	}
}
