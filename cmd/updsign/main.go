// SPDX-License-Identifier: AGPL-3.0-or-later

// Command updsign makes and checks the Ed25519 signatures of update assets
// (see internal/updates): each signature is bound to the exact release tag
// and asset name, not just the file's bytes, so a correctly-signed old
// update cannot be replayed under a newer tag and a signature for one asset
// cannot be copied onto another.
//
//	go run ./cmd/updsign -keygen                                            # prints UPDATE_PUBLIC_KEY=… and UPDATE_SIGNING_KEY=…
//	UPDATE_SIGNING_KEY=… go run ./cmd/updsign -sign -tag vX.Y.Z -name <asset-name> FILE…   # writes FILE.sig
//	go run ./cmd/updsign -verify -pub <key> -tag vX.Y.Z -name <asset-name> FILE…           # checks FILE.sig
//
// -name is the release asset's exact file name (e.g.
// SiteMoment-macos-universal.zip), which may differ from FILE's local path.
// The signing key is read from the environment only, never from the command line.
package main

import (
	"crypto/sha256"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/sitemoment/sitemoment-desktop/internal/updates"
)

func digest(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

func main() {
	keygen := flag.Bool("keygen", false, "print a new key pair")
	sign := flag.Bool("sign", false, "sign FILE… with $UPDATE_SIGNING_KEY into FILE.sig")
	verify := flag.Bool("verify", false, "check FILE….sig against -pub")
	tag := flag.String("tag", "", "release git tag, e.g. v1.2.3 (binds the signature to this exact release)")
	name := flag.String("name", "", "release asset name, e.g. SiteMoment-macos-universal.zip (binds the signature to this exact asset)")
	pub := flag.String("pub", "", "base64 public key for -verify")
	flag.Parse()
	fail := func(err error) {
		fmt.Fprintln(os.Stderr, "updsign:", err)
		os.Exit(1)
	}
	switch {
	case *keygen:
		p, s, err := updates.GenerateKey()
		if err != nil {
			fail(err)
		}
		fmt.Printf("UPDATE_PUBLIC_KEY=%s\nUPDATE_SIGNING_KEY=%s\n", p, s)
	case *sign:
		if *tag == "" || *name == "" {
			fail(fmt.Errorf("-sign needs -tag and -name"))
		}
		seed := os.Getenv("UPDATE_SIGNING_KEY")
		if seed == "" {
			fail(fmt.Errorf("UPDATE_SIGNING_KEY is not set"))
		}
		for _, f := range flag.Args() {
			d, err := digest(f)
			if err != nil {
				fail(err)
			}
			sig, err := updates.SignRelease(seed, *tag, *name, d)
			if err != nil {
				fail(err)
			}
			if err := os.WriteFile(f+".sig", []byte(sig+"\n"), 0o644); err != nil {
				fail(err)
			}
			fmt.Println("signed", f)
		}
	case *verify:
		if *tag == "" || *name == "" {
			fail(fmt.Errorf("-verify needs -tag and -name"))
		}
		key, err := updates.ParsePublicKey(*pub)
		if err != nil {
			fail(err)
		}
		for _, f := range flag.Args() {
			d, err := digest(f)
			if err != nil {
				fail(err)
			}
			b, err := os.ReadFile(f + ".sig")
			if err != nil {
				fail(err)
			}
			sig, err := updates.ParseSignature(b)
			if err != nil {
				fail(err)
			}
			if !updates.VerifyRelease(key, *tag, *name, d, sig) {
				fail(fmt.Errorf("%s: signature does not verify", f))
			}
			fmt.Println("ok", f)
		}
	default:
		flag.Usage()
		os.Exit(2)
	}
}
