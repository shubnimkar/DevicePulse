package main

import (
	"strings"
	"testing"
)

// Same fixed seed and inputs as the agent's TestVerifyReleaseGoldenSignature: if
// this stops matching, the tool and the agent no longer agree on the wire format.
func TestSignMatchesAgentGoldenVector(t *testing.T) {
	seed := make([]byte, 32)
	for i := range seed {
		seed[i] = byte(i + 1)
	}
	const goldenSeedB64 = "AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA="
	const want = "TVEIPM4gwoh1H4byCxamT6+/Panyi2ZT/s+lpFzWHsyUoX9VKNnPXNi1VZpbNyFqiiT/kI71f9eJ20AudXINAg=="
	got, err := sign(goldenSeedB64, "1.2.3", "linux", "amd64", strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("signature differs from the agent's golden vector:\n got %s\nwant %s", got, want)
	}
}

func TestSignValidatesInput(t *testing.T) {
	if _, err := sign("not base64!", "1", "linux", "amd64", strings.Repeat("a", 64)); err == nil {
		t.Error("bad key accepted")
	}
	if _, err := sign("AAAA", "1", "linux", "amd64", strings.Repeat("a", 64)); err == nil {
		t.Error("short key accepted")
	}
	if _, err := sign("AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA=", "1", "linux", "amd64", "short"); err == nil {
		t.Error("short checksum accepted")
	}
}
