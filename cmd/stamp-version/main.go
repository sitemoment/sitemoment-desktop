// SPDX-License-Identifier: AGPL-3.0-or-later

// Command stamp-version writes a release version into the build files and
// prints its numeric part. The release workflow runs it before packaging:
//
//	go run ./cmd/stamp-version "${GITHUB_REF_NAME#v}"
//
// -check validates a VERSION the same way, without touching any files or
// requiring -root: the per-platform Taskfiles use it as a precondition
// before a production build embeds VERSION via -ldflags, so a malformed
// value fails the build loudly instead of silently landing in the binary.
//
//	go run ./cmd/stamp-version -check "$VERSION"
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/sitemoment/sitemoment-desktop/internal/release"
)

func main() {
	root := flag.String("root", ".", "repository root")
	check := flag.Bool("check", false, "validate the version only; do not touch any files (ignores -root)")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: stamp-version [-root dir] <version, e.g. 1.2.3>\n       stamp-version -check <version>")
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	if *check {
		num, err := release.Numeric(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(num)
		return
	}
	if err := release.Stamp(*root, flag.Arg(0)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	num, _ := release.Numeric(flag.Arg(0))
	fmt.Println(num)
}
