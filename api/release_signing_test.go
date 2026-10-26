package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"
)

// Same fixed seed and inputs as the agent's TestVerifyReleaseGoldenSignature and
// cmd/sign_release: all three must agree byte for byte.
func TestReleaseSigningMessageMatchesAgentGoldenVector(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i + 1)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	const want = "TVEIPM4gwoh1H4byCxamT6+/Panyi2ZT/s+lpFzWHsyUoX9VKNnPXNi1VZpbNyFqiiT/kI71f9eJ20AudXINAg=="
	got := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, releaseSigningMessage("1.2.3", "linux", "amd64", strings.Repeat("ab", 32))))
	if got != want {
		t.Fatalf("API signing message diverged from the agent's:\n got %s\nwant %s", got, want)
	}
}

func TestVerifyReleaseSignatureShapeAndKey(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	priv := ed25519.NewKeyFromSeed(seed)
	rel := AgentRelease{Version: "1.0.0", OS: "linux", Arch: "amd64", Checksum: strings.Repeat("cd", 32)}
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, releaseSigningMessage(rel.Version, rel.OS, rel.Arch, rel.Checksum)))

	t.Setenv("UPDATE_PUBLIC_KEY", "")
	if err := verifyReleaseSignature(rel, sig); err != nil {
		t.Fatalf("well-formed signature with no key configured: %v", err)
	}
	if err := verifyReleaseSignature(rel, "AAAA"); err == nil {
		t.Fatal("malformed signature accepted")
	}

	t.Setenv("UPDATE_PUBLIC_KEY", base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey)))
	if err := verifyReleaseSignature(rel, sig); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	rel.Version = "9.9.9"
	if err := verifyReleaseSignature(rel, sig); err == nil {
		t.Fatal("signature must not verify for a different version")
	}
}
