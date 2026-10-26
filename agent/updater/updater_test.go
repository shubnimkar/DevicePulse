package updater

import (
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"
)

// Fixed seed so the golden signature pins the wire format. cmd/sign_release must
// produce the same bytes for the same inputs.
func testKey() (ed25519.PrivateKey, string) {
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i + 1)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	return priv, base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey))
}

func sign(priv ed25519.PrivateKey, version, goos, goarch, checksum string) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, ReleaseMessage(version, goos, goarch, checksum)))
}

func withKey(t *testing.T, key string) {
	t.Helper()
	old := PublicKeyB64
	PublicKeyB64 = key
	t.Cleanup(func() { PublicKeyB64 = old })
}

func TestReleaseMessageFormat(t *testing.T) {
	got := string(ReleaseMessage("1.2.3", "linux", "amd64", "ABCDEF"))
	want := "devicepulse-release-v1\n1.2.3\nlinux/amd64\nabcdef"
	if got != want {
		t.Fatalf("message = %q, want %q", got, want)
	}
}

func TestVerifyReleaseGoldenSignature(t *testing.T) {
	priv, pub := testKey()
	withKey(t, pub)
	cr := CheckResponse{Version: "1.2.3", Checksum: strings.Repeat("ab", 32)}
	cr.Signature = sign(priv, cr.Version, "linux", "amd64", cr.Checksum)
	// Ed25519 is deterministic: this value must never change, or already-signed
	// releases stop verifying on installed agents.
	const golden = "TVEIPM4gwoh1H4byCxamT6+/Panyi2ZT/s+lpFzWHsyUoX9VKNnPXNi1VZpbNyFqiiT/kI71f9eJ20AudXINAg=="
	if cr.Signature != golden {
		t.Fatalf("golden signature changed: %s", cr.Signature)
	}
	if err := verifyRelease(cr, "linux", "amd64"); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyReleaseRejectsTampering(t *testing.T) {
	priv, pub := testKey()
	withKey(t, pub)
	sum := strings.Repeat("cd", 32)
	good := CheckResponse{Version: "1.2.3", Checksum: sum, Signature: sign(priv, "1.2.3", "linux", "amd64", sum)}

	cases := map[string]func(CheckResponse) (CheckResponse, string, string){
		"different binary": func(c CheckResponse) (CheckResponse, string, string) {
			c.Checksum = strings.Repeat("ee", 32)
			return c, "linux", "amd64"
		},
		"different version": func(c CheckResponse) (CheckResponse, string, string) { c.Version = "9.9.9"; return c, "linux", "amd64" },
		"different target":  func(c CheckResponse) (CheckResponse, string, string) { return c, "windows", "amd64" },
		"missing signature": func(c CheckResponse) (CheckResponse, string, string) { c.Signature = ""; return c, "linux", "amd64" },
		"malformed": func(c CheckResponse) (CheckResponse, string, string) {
			c.Signature = "not-base64!"
			return c, "linux", "amd64"
		},
		"short signature": func(c CheckResponse) (CheckResponse, string, string) {
			c.Signature = "AAAA"
			return c, "linux", "amd64"
		},
	}
	for name, mutate := range cases {
		c, goos, goarch := mutate(good)
		if err := verifyRelease(c, goos, goarch); err == nil {
			t.Errorf("%s: expected the update to be refused", name)
		}
	}

	otherPriv := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	forged := good
	forged.Signature = sign(otherPriv, "1.2.3", "linux", "amd64", sum)
	if err := verifyRelease(forged, "linux", "amd64"); err == nil {
		t.Error("a signature from another key must be refused")
	}
}

func TestVerifyReleaseOffWithoutEmbeddedKey(t *testing.T) {
	withKey(t, "")
	if err := verifyRelease(CheckResponse{Version: "1.0.0", Checksum: "aa"}, "linux", "amd64"); err != nil {
		t.Fatalf("legacy builds must keep accepting unsigned updates: %v", err)
	}
}

func TestVerifyReleaseRejectsBadEmbeddedKey(t *testing.T) {
	withKey(t, "not-a-key")
	if err := verifyRelease(CheckResponse{Version: "1.0.0", Checksum: "aa", Signature: "x"}, "linux", "amd64"); err == nil {
		t.Fatal("a corrupt embedded key must fail closed")
	}
}
