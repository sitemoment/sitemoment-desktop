// SPDX-License-Identifier: AGPL-3.0-or-later

// Command stamp-version writes a release version into the build files and
// prints its numeric part. The release workflow runs it before packaging:
//
//	go run ./cmd/stamp-version "${GITHUB_REF_NAME#v}"
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/sitemoment/sitemoment-desktop/internal/release"
)

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Usage = func() { fmt.Fprintln(os.Stderr, "usage: stamp-version [-root dir] <version, e.g. 1.2.3>") }
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	if err := release.Stamp(*root, flag.Arg(0)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	num, _ := release.Numeric(flag.Arg(0))
	fmt.Println(num)
}
