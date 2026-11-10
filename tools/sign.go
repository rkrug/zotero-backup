//go:build ignore

// Ed25519 signing of release files for zotero-backup upgrade (upgrade.go).
//
//	go run tools/sign.go keygen FILE
//	    writes a new private key (base64) to FILE, readable only by you, and
//	    prints the public key (base64) for updatePublicKey in upgrade.go.
//	    Store FILE as the GitHub secret ZB_UPDATE_SIGNING_KEY
//	    (gh secret set ZB_UPDATE_SIGNING_KEY < FILE) and keep it safe.
//	ZB_UPDATE_SIGNING_KEY=<base64> go run tools/sign.go sign FILE
//	    writes FILE.sig (base64 signature over FILE's bytes).
package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "sign:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	switch {
	case len(args) == 2 && args[0] == "keygen":
		pub, priv, err := ed25519.GenerateKey(nil)
		if err != nil {
			return err
		}
		f, err := os.OpenFile(args[1], os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintln(f, base64.StdEncoding.EncodeToString(priv.Seed())); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		fmt.Println("private key written to", args[1])
		fmt.Println("public key:", base64.StdEncoding.EncodeToString(pub))
		return nil
	case len(args) == 2 && args[0] == "sign":
		seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("ZB_UPDATE_SIGNING_KEY")))
		if err != nil || len(seed) != ed25519.SeedSize {
			return fmt.Errorf("ZB_UPDATE_SIGNING_KEY is missing or not a base64 Ed25519 private key")
		}
		data, err := os.ReadFile(args[1])
		if err != nil {
			return err
		}
		priv := ed25519.NewKeyFromSeed(seed)
		sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, data))
		if err := os.WriteFile(args[1]+".sig", []byte(sig+"\n"), 0o644); err != nil {
			return err
		}
		fmt.Printf("signed: %s.sig (public key %s)\n", args[1], base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey)))
		return nil
	}
	return fmt.Errorf("usage: go run tools/sign.go keygen FILE | sign FILE")
}
