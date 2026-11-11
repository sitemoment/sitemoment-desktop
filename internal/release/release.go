// SPDX-License-Identifier: AGPL-3.0-or-later

// Package release has what a release build needs besides the compiler: the
// version rules and the build files that carry the version.
package release

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var versionRE = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z][0-9A-Za-z.-]*)?$`)

// Numeric checks a release version ("1.2.3" or "1.2.3-rc.1", the git tag
// without its "v") and returns the numeric part ("1.2.3"): the only form the
// macOS bundle version and the Windows version resource accept.
func Numeric(version string) (string, error) {
	m := versionRE.FindStringSubmatch(version)
	if m == nil {
		return "", fmt.Errorf("release: %q is not a version like 1.2.3 or 1.2.3-rc.1 (no leading v)", version)
	}
	return m[1] + "." + m[2] + "." + m[3], nil
}

// IsRelease reports whether version is a release version (what Numeric
// accepts). Development builds ("dev") are not, and do not self-update.
func IsRelease(version string) bool {
	_, err := Numeric(version)
	return err == nil
}

type edit struct {
	file string
	re   *regexp.Regexp
	to   string // replacement; ${1} and ${2} are the text around the old version
}

// Stamp writes version into the build files under root (the repository):
// build/config.yml, the macOS Info.plist, and the Windows version resource,
// manifest and NSIS defaults. Every pattern must match exactly once, so a
// regenerated template that moved a field fails here instead of shipping
// the old version.
func Stamp(root, version string) error {
	num, err := Numeric(version)
	if err != nil {
		return err
	}
	edits := []edit{
		{"build/config.yml", regexp.MustCompile(`(?m)^(  version: ")[^"]*(")`), "${1}" + version + "${2}"},
		{"build/darwin/Info.plist", regexp.MustCompile(`(<key>CFBundleVersion</key>\s*<string>)[^<]*(</string>)`), "${1}" + num + "${2}"},
		{"build/darwin/Info.plist", regexp.MustCompile(`(<key>CFBundleShortVersionString</key>\s*<string>)[^<]*(</string>)`), "${1}" + num + "${2}"},
		{"build/windows/info.json", regexp.MustCompile(`("file_version":\s*")[^"]*(")`), "${1}" + num + "${2}"},
		{"build/windows/info.json", regexp.MustCompile(`("product_version":\s*")[^"]*(")`), "${1}" + num + "${2}"},
		{"build/windows/info.json", regexp.MustCompile(`("FileVersion":\s*")[^"]*(")`), "${1}" + num + "${2}"},
		{"build/windows/info.json", regexp.MustCompile(`("ProductVersion":\s*")[^"]*(")`), "${1}" + version + "${2}"},
		{"build/windows/wails.exe.manifest", regexp.MustCompile(`(<assemblyIdentity type="win32" name="com\.sitemoment\.desktop" version=")[^"]*(")`), "${1}" + num + ".0${2}"},
		{"build/windows/nsis/wails_tools.nsh", regexp.MustCompile(`(!define INFO_PRODUCTVERSION ")[^"]*(")`), "${1}" + num + "${2}"},
	}
	files := map[string][]byte{}
	for _, e := range edits {
		b, ok := files[e.file]
		if !ok {
			if b, err = os.ReadFile(filepath.Join(root, e.file)); err != nil {
				return fmt.Errorf("release: %w", err)
			}
		}
		if n := len(e.re.FindAllIndex(b, -1)); n != 1 {
			return fmt.Errorf("release: %s: %s matches %d times, want 1", e.file, e.re, n)
		}
		files[e.file] = e.re.ReplaceAll(b, []byte(e.to))
	}
	for name, b := range files {
		if err := os.WriteFile(filepath.Join(root, name), b, 0o644); err != nil {
			return fmt.Errorf("release: %w", err)
		}
	}
	return nil
}
