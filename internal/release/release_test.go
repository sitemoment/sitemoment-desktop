// SPDX-License-Identifier: AGPL-3.0-or-later
package release

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNumeric(t *testing.T) {
	for in, want := range map[string]string{"1.2.3": "1.2.3", "0.5.0-rc.1": "0.5.0", "10.0.12-beta": "10.0.12"} {
		if got, err := Numeric(in); err != nil || got != want {
			t.Errorf("Numeric(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "dev", "v1.2.3", "1.2", "1.2.3.4", "01.2.3", "1.2.3-", "1.2.3+build",
		// The hand-written regexp alone accepts these (its pre-release
		// character class doesn't validate per-identifier rules); semver.IsValid
		// catches them: empty identifier, leading-zero numeric identifiers,
		// and a trailing empty identifier.
		"1.2.3-rc..1", "1.2.3-01", "1.2.3-rc.01", "1.2.3-rc.1."} {
		if _, err := Numeric(bad); err == nil {
			t.Errorf("Numeric(%q) accepted", bad)
		}
		if IsRelease(bad) {
			t.Errorf("IsRelease(%q)", bad)
		}
	}
}

// copyBuild copies the repository's real build files, so a template change
// that breaks a pattern fails this test.
func copyBuild(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, f := range []string{"build/config.yml", "build/darwin/Info.plist", "build/windows/info.json", "build/windows/wails.exe.manifest", "build/windows/nsis/wails_tools.nsh"} {
		b, err := os.ReadFile(filepath.Join("..", "..", f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, f)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, f), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func read(t *testing.T, root, f string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, f))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestStampWritesEveryVersionField(t *testing.T) {
	root := copyBuild(t)
	if err := Stamp(root, "1.4.2-rc.1"); err != nil {
		t.Fatal(err)
	}
	checks := map[string][]string{
		"build/config.yml":                   {`  version: "1.4.2-rc.1"`},
		"build/darwin/Info.plist":            {"<key>CFBundleVersion</key>\n            <string>1.4.2</string>", "<key>CFBundleShortVersionString</key>\n            <string>1.4.2</string>"},
		"build/windows/info.json":            {`"file_version": "1.4.2"`, `"product_version": "1.4.2"`, `"FileVersion": "1.4.2"`, `"ProductVersion": "1.4.2-rc.1"`},
		"build/windows/wails.exe.manifest":   {`version="1.4.2.0"`},
		"build/windows/nsis/wails_tools.nsh": {`!define INFO_PRODUCTVERSION "1.4.2"`},
	}
	for f, wants := range checks {
		got := read(t, root, f)
		for _, w := range wants {
			if !strings.Contains(got, w) {
				t.Errorf("%s lacks %q", f, w)
			}
		}
	}
	if strings.Contains(read(t, root, "build/config.yml"), "version: '3'") == false {
		t.Error("the Taskfile-style top-level version: '3' must stay untouched")
	}
}

func TestStampRefusesBadVersionsAndMovedFields(t *testing.T) {
	root := copyBuild(t)
	if err := Stamp(root, "v1.0.0"); err == nil {
		t.Fatal("accepted a v-prefixed version")
	}
	plist := filepath.Join(root, "build/darwin/Info.plist")
	b, _ := os.ReadFile(plist)
	os.WriteFile(plist, []byte(strings.Replace(string(b), "CFBundleShortVersionString", "Short", 1)), 0o644)
	before := read(t, root, "build/config.yml")
	if err := Stamp(root, "1.0.0"); err == nil || !strings.Contains(err.Error(), "Info.plist") {
		t.Fatalf("err = %v", err)
	}
	if read(t, root, "build/config.yml") != before {
		t.Fatal("a failed stamp must not write any file")
	}
}
