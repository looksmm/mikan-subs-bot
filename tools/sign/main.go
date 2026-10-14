// sign signs a file as mikan checks an addon source: the base64 ed25519 signature of its exact
// bytes, on one line. The key is the base64 of a 32-byte seed (or a 64-byte private key), read
// from the environment variable -key-env names; -public prints the public key to give admins.
//
//	go run ./tools/sign -key-env MIKAN_ADDON_KEY mikan-addon.json > mikan-addon.json.sig
//	go run ./tools/sign -key-env MIKAN_ADDON_KEY -public
//	go run ./tools/sign -new   (a new key: the seed for the secret, the public key for the README)
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	env := flag.String("key-env", "MIKAN_ADDON_KEY", "the environment variable that holds the key")
	public := flag.Bool("public", false, "print the public key")
	fresh := flag.Bool("new", false, "make a new key")
	flag.Parse()
	if *fresh {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			fail(err)
		}
		fmt.Println("secret (MIKAN_ADDON_KEY):", base64.StdEncoding.EncodeToString(priv.Seed()))
		fmt.Println("public key:", base64.StdEncoding.EncodeToString(pub))
		return
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv(*env)))
	if err != nil {
		fail(fmt.Errorf("%s: %w", *env, err))
	}
	var priv ed25519.PrivateKey
	switch len(raw) {
	case ed25519.SeedSize:
		priv = ed25519.NewKeyFromSeed(raw)
	case ed25519.PrivateKeySize:
		priv = ed25519.PrivateKey(raw)
	default:
		fail(fmt.Errorf("%s: %d bytes, want 32 or 64", *env, len(raw)))
	}
	if *public {
		fmt.Println(base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey)))
		return
	}
	if flag.NArg() != 1 {
		fail(fmt.Errorf("usage: sign -key-env VAR <file>"))
	}
	data, err := os.ReadFile(flag.Arg(0))
	if err != nil {
		fail(err)
	}
	fmt.Println(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, data)))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "sign:", err)
	os.Exit(1)
}
