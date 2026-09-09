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
	"errors"
	"fmt"
	"net/http"
)

const payloadKeyHeader = "X-DevicePulse-Public-Key"
const payloadProtocol = "devicepulse-response-v1"
const maxEncryptedResponseBytes = 64 << 20

// Wire format: server P-256 public key (65) | HKDF salt (32) | GCM nonce (12)
// | ciphertext and authentication tag (16). Only public material crosses the
// wire. HTTPS authenticates this ephemeral key exchange; this is not a TLS replacement.
func encryptedResponse(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		encodedKey := r.Header.Get(payloadKeyHeader)
		if encodedKey == "" {
			next(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		publicBytes, err := base64.StdEncoding.DecodeString(encodedKey)
		if err != nil || len(publicBytes) != 65 {
			http.Error(w, "Invalid response encryption key", http.StatusBadRequest)
			return
		}
		publicKey, err := ecdh.P256().NewPublicKey(publicBytes)
		if err != nil {
			http.Error(w, "Invalid response encryption key", http.StatusBadRequest)
			return
		}
		privateKey, err := ecdh.P256().GenerateKey(rand.Reader)
		if err != nil {
			http.Error(w, "Response encryption unavailable", http.StatusInternalServerError)
			return
		}
		shared, err := privateKey.ECDH(publicKey)
		if err != nil {
			http.Error(w, "Response encryption unavailable", http.StatusInternalServerError)
			return
		}
		salt := make([]byte, 32)
		nonce := make([]byte, 12)
		if _, err = rand.Read(salt); err != nil {
			http.Error(w, "Response encryption unavailable", http.StatusInternalServerError)
			return
		}
		if _, err = rand.Read(nonce); err != nil {
			http.Error(w, "Response encryption unavailable", http.StatusInternalServerError)
			return
		}
		key, err := hkdf.Key(sha256.New, shared, salt, payloadProtocol, 32)
		if err != nil {
			http.Error(w, "Response encryption unavailable", http.StatusInternalServerError)
			return
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			http.Error(w, "Response encryption unavailable", http.StatusInternalServerError)
			return
		}
		gcm, err := cipher.NewGCM(block)
		if err != nil {
			http.Error(w, "Response encryption unavailable", http.StatusInternalServerError)
			return
		}

		buffer := &encryptedResponseWriter{header: w.Header().Clone()}
		next(buffer, r)
		if buffer.status == 0 {
			buffer.status = http.StatusOK
		}
		if buffer.overflow {
			buffer.status = http.StatusInternalServerError
			buffer.body.Reset()
			buffer.body.WriteString("Response exceeds encryption size limit")
			buffer.header.Set("Content-Type", "text/plain; charset=utf-8")
		}
		contentType := buffer.header.Get("Content-Type")
		if contentType == "" {
			contentType = http.DetectContentType(buffer.body.Bytes())
		}
		for name := range w.Header() {
			w.Header().Del(name)
		}
		for name, values := range buffer.header {
			w.Header()[name] = values
		}
		w.Header().Del("Content-Length")
		w.Header().Del("Content-Encoding")
		w.Header().Del("ETag")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-DevicePulse-Encryption", payloadProtocol)
		w.Header().Set("X-DevicePulse-Content-Type", contentType)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(buffer.status)
		if r.Method == http.MethodHead || buffer.status == 204 || buffer.status == 304 {
			return
		}
		// The fresh client key binds this response to one fetch; status and type
		// are authenticated as additional data so they cannot be substituted.
		aad := []byte(fmt.Sprintf("%s\n%d\n%s", payloadProtocol, buffer.status, contentType))
		wire := append(privateKey.PublicKey().Bytes(), salt...)
		wire = append(wire, nonce...)
		wire = gcm.Seal(wire, nonce, buffer.body.Bytes(), aad)
		_, _ = w.Write(wire)
	}
}

// Dashboard endpoints fail closed rather than serving readable JSON when a
// client omits the key. Agent-only endpoints retain their existing protocol.
func requireEncryptedResponse(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := w.(*encryptedResponseWriter); !ok {
			w.Header().Set("Cache-Control", "no-store")
			http.Error(w, "Dashboard response encryption required", http.StatusBadRequest)
			return
		}
		next(w, r)
	}
}

type encryptedResponseWriter struct {
	header   http.Header
	body     bytes.Buffer
	status   int
	overflow bool
}

func (w *encryptedResponseWriter) Header() http.Header { return w.header }
func (w *encryptedResponseWriter) WriteHeader(status int) {
	if w.status == 0 && status >= 200 {
		w.status = status
	}
}
func (w *encryptedResponseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.overflow || len(p) > maxEncryptedResponseBytes-w.body.Len() {
		w.overflow = true
		return 0, errors.New("encrypted response too large")
	}
	return w.body.Write(p)
}
