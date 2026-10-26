// sign_release signs an agent release with the offline Ed25519 release key.
//
//	sign_release -genkey
//	    prints a new key pair: keep PRIVATE offline, embed PUBLIC in agent builds
//	    (DEVICEPULSE_UPDATE_PUBKEY) and optionally set it as UPDATE_PUBLIC_KEY on the API.
//
//	DEVICEPULSE_SIGNING_KEY=<private> sign_release -version 1.2.3 -os linux -arch amd64 -checksum <sha256 hex>
//	    prints the base64 signature to paste next to the release in the dashboard.
//
// The signed message must match updater.ReleaseMessage in the agent.
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

func releaseMessage(version, goos, goarch, checksum string) []byte {
	return []byte("devicepulse-release-v1\n" + version + "\n" + goos + "/" + goarch + "\n" + strings.ToLower(checksum))
}

func sign(privB64, version, goos, goarch, checksum string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(privB64))
	if err != nil {
		return "", fmt.Errorf("signing key is not valid base64: %w", err)
	}
	var priv ed25519.PrivateKey
	switch len(raw) {
	case ed25519.SeedSize:
		priv = ed25519.NewKeyFromSeed(raw)
	case ed25519.PrivateKeySize:
		priv = ed25519.PrivateKey(raw)
	default:
		return "", fmt.Errorf("signing key must be a 32-byte seed or 64-byte private key, got %d bytes", len(raw))
	}
	if version == "" || goos == "" || goarch == "" || len(checksum) != 64 {
		return "", fmt.Errorf("version, os, arch and a 64-character sha256 checksum are required")
	}
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, releaseMessage(version, goos, goarch, checksum))), nil
}

func main() {
	genkey := flag.Bool("genkey", false, "generate a new key pair")
	version := flag.String("version", "", "release version, e.g. 1.2.3")
	goos := flag.String("os", "", "linux | darwin | windows")
	goarch := flag.String("arch", "", "amd64 | arm64")
	checksum := flag.String("checksum", "", "sha256 of the agent binary (hex)")
	flag.Parse()

	if *genkey {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("PRIVATE (keep offline, never commit): %s\n", base64.StdEncoding.EncodeToString(priv.Seed()))
		fmt.Printf("PUBLIC  (embed in agent builds):      %s\n", base64.StdEncoding.EncodeToString(pub))
		return
	}

	sig, err := sign(os.Getenv("DEVICEPULSE_SIGNING_KEY"), *version, *goos, *goarch, *checksum)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Println(sig)
}
