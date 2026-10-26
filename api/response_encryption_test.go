package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
)

func TestEncryptedResponse(t *testing.T) {
	client, _ := ecdh.P256().GenerateKey(rand.Reader)
	handler := corsMiddleware(requireEncryptedResponse(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		http.SetCookie(w, &http.Cookie{Name: "devicepulse_session", Value: "test", HttpOnly: true})
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"private message"}`))
	}))
	request := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	request.Header.Set(payloadKeyHeader, base64.StdEncoding.EncodeToString(client.PublicKey().Bytes()))
	response := httptest.NewRecorder()
	handler(response, request)
	if response.Code != 401 || response.Header().Get("Content-Type") != "application/octet-stream" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected encrypted response: %d %v", response.Code, response.Header())
	}
	if len(response.Result().Cookies()) != 1 {
		t.Fatal("session cookie was lost")
	}
	wire := response.Body.Bytes()
	if bytes.Contains(wire, []byte("private message")) {
		t.Fatal("plaintext leaked")
	}
	server, err := ecdh.P256().NewPublicKey(wire[:65])
	if err != nil {
		t.Fatal(err)
	}
	shared, _ := client.ECDH(server)
	key, _ := hkdf.Key(sha256.New, shared, wire[65:97], payloadProtocol, 32)
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	aad := []byte(fmt.Sprintf("%s\n401\napplication/json", payloadProtocol))
	plain, err := gcm.Open(nil, wire[97:109], wire[109:], aad)
	if err != nil || string(plain) != `{"error":"private message"}` {
		t.Fatalf("decrypt: %s %v", plain, err)
	}
	wire[len(wire)-1] ^= 1
	if _, err = gcm.Open(nil, wire[97:109], wire[109:], aad); err == nil {
		t.Fatal("tampering accepted")
	}
}

func TestEncryptionRequiredAndAgentCompatibility(t *testing.T) {
	called := false
	next := func(w http.ResponseWriter, r *http.Request) { called = true; _, _ = w.Write([]byte("agent OK")) }
	for _, key := range []string{"", "invalid", base64.StdEncoding.EncodeToString(make([]byte, 65))} {
		called = false
		r := httptest.NewRequest(http.MethodGet, "/devices", nil)
		if key != "" {
			r.Header.Set(payloadKeyHeader, key)
		}
		w := httptest.NewRecorder()
		corsMiddleware(requireEncryptedResponse(next))(w, r)
		if w.Code != 400 || called {
			t.Fatalf("invalid/missing key allowed: %q", key)
		}
	}
	w := httptest.NewRecorder()
	corsMiddleware(next)(w, httptest.NewRequest(http.MethodGet, "/commands/pending", nil))
	if !called || w.Body.String() != "agent OK" {
		t.Fatal("agent compatibility broken")
	}
	w = httptest.NewRecorder()
	corsMiddleware(requireEncryptedResponse(next))(w, httptest.NewRequest(http.MethodOptions, "/devices", nil))
	if w.Code != 200 {
		t.Fatal("preflight must not require encryption")
	}
}

func TestEncryptedResponseOverflow(t *testing.T) {
	w := &encryptedResponseWriter{header: make(http.Header)}
	chunk := make([]byte, 1<<20)
	for i := 0; i < 64; i++ {
		if _, err := w.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.Write([]byte("x")); err == nil || !w.overflow || w.body.Len() != maxEncryptedResponseBytes {
		t.Fatal("response buffer limit not enforced")
	}
}

func TestDashboardRoutesRequireEncryption(t *testing.T) {
	mux := http.NewServeMux()
	registerRoutes(mux)
	for _, path := range []string{
		"/auth/bootstrap", "/auth/login", "/auth/register", "/auth/logout", "/auth/me",
		"/users", "/users/example/password", "/devices", "/devices/example/commands",
		"/devices/example/browser-history", "/devices/example/history", "/devices/example/name",
		"/devices/example/ping", "/devices/example/app-usage", "/devices/example/presence",
		"/reports/weekly", "/policy", "/focus/example", "/update/releases", "/update/builds",
		"/update/release", "/update/release/activate", "/update/release/signature", "/update/release/rollout-latest",
	} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != 400 || !bytes.Contains(w.Body.Bytes(), []byte("encryption required")) {
			t.Fatalf("%s did not require encryption: %d %s", path, w.Code, w.Body.String())
		}
	}
	// Encryption negotiation must not bypass session authentication.
	client, _ := ecdh.P256().GenerateKey(rand.Reader)
	r := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	r.Header.Set(payloadKeyHeader, base64.StdEncoding.EncodeToString(client.PublicKey().Bytes()))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized || w.Header().Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("expected encrypted unauthorized response: %d", w.Code)
	}
}

// Exercise the actual TypeScript fetch helper against Go's encryption, using
// Node's Web Crypto implementation. Dashboard dependencies are needed for TS.
func TestResponseEncryptionWebCrypto(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node unavailable")
	}
	if _, err := os.Stat("../dashboard/node_modules/typescript"); err != nil {
		t.Skip("dashboard dependencies unavailable")
	}
	server := httptest.NewServer(corsMiddleware(requireEncryptedResponse(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/empty" {
			w.WriteHeader(204)
			return
		}
		if r.URL.Path == "/error" {
			w.WriteHeader(403)
		}
		_, _ = w.Write([]byte(`{"message":"private telemetry ✓"}`))
	})))
	defer server.Close()
	cmd := exec.Command("node", "../dashboard/scripts/test-response-encryption.cjs", server.URL)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Web Crypto interoperability: %v\n%s", err, out)
	}
}
