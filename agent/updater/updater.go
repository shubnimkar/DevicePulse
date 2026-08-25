// Package updater handles agent self-update logic.
//
// Flow:
//  1. Poll GET /update/check with current version + platform info.
//  2. If the server signals an update is available, download the binary URL.
//  3. Verify the SHA-256 checksum returned by the server.
//  4. Atomically replace the running executable.
//  5. Re-exec the new binary so the update takes effect immediately.
package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

// CheckResponse is the JSON returned by GET /update/check.
type CheckResponse struct {
	UpdateAvailable bool   `json:"update_available"`
	Version         string `json:"version"`         // e.g. "1.2.0"
	DownloadURL     string `json:"download_url"`    // full URL to new binary
	Checksum        string `json:"checksum_sha256"` // hex-encoded SHA-256 of the binary
}

// Poller runs in a goroutine and checks for updates on the given interval.
// It calls GET <apiURL>/update/check and self-updates when a new version is available.
//
// Parameters:
//   - apiURL:        base URL of the DevicePulse API, e.g. "https://api.example.com"
//   - apiKey:        X-API-Key header value for authentication
//   - currentVersion: the version string baked into the running binary, e.g. "1.0.0"
//   - interval:      how often to poll (recommended: 5m in production, 30s for dev)
func Poller(apiURL, apiKey, currentVersion string, interval time.Duration) {
	log.Printf("Updater: running version %s, polling every %v", currentVersion, interval)

	// Stagger the first check so it doesn't pile up with startup work.
	time.Sleep(30 * time.Second)

	for {
		if err := checkAndUpdate(apiURL, apiKey, currentVersion); err != nil {
			log.Printf("Updater: check failed: %v", err)
		}
		time.Sleep(interval)
	}
}

func checkAndUpdate(apiURL, apiKey, currentVersion string) error {
	// Build check URL with current version + platform so the server can
	// return the right binary for this OS/arch.
	url := fmt.Sprintf("%s/update/check?version=%s&os=%s&arch=%s",
		apiURL, currentVersion, runtime.GOOS, runtime.GOARCH)

	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("X-API-Key", apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("check request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("check returned status %d", resp.StatusCode)
	}

	var cr CheckResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	if !cr.UpdateAvailable {
		log.Printf("Updater: already up to date (%s)", currentVersion)
		return nil
	}

	log.Printf("Updater: new version available: %s → %s", currentVersion, cr.Version)

	if cr.DownloadURL == "" {
		return fmt.Errorf("update available but download_url is empty")
	}
	if cr.Checksum == "" {
		return fmt.Errorf("update available but checksum_sha256 is empty — refusing unsafe update")
	}

	return applyUpdate(cr)
}

// applyUpdate downloads the new binary, verifies its checksum, and replaces
// the current executable, then re-execs into the new binary.
func applyUpdate(cr CheckResponse) error {
	// Find the path of the running executable.
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}
	// Follow any symlinks to get the real path.
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return fmt.Errorf("eval symlinks: %w", err)
	}

	exeDir := filepath.Dir(exePath)

	// Download to a temp file in the same directory so os.Rename is atomic
	// (same filesystem guarantees atomic rename on POSIX systems).
	tmpFile, err := os.CreateTemp(exeDir, ".agent-update-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	// Clean up temp file on any error path.
	defer func() {
		tmpFile.Close()
		// If the rename succeeded tmpPath no longer exists; Remove is a no-op.
		os.Remove(tmpPath)
	}()

	log.Printf("Updater: downloading %s → %s", cr.Version, tmpPath)

	client := &http.Client{Timeout: 5 * time.Minute}
	dlResp, err := client.Get(cr.DownloadURL)
	if err != nil {
		return fmt.Errorf("download binary: %w", err)
	}
	defer dlResp.Body.Close()

	if dlResp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned status %d", dlResp.StatusCode)
	}

	// Stream download while computing checksum simultaneously.
	hasher := sha256.New()
	writer := io.MultiWriter(tmpFile, hasher)

	if _, err := io.Copy(writer, dlResp.Body); err != nil {
		return fmt.Errorf("write binary: %w", err)
	}
	tmpFile.Close()

	// Verify checksum.
	got := hex.EncodeToString(hasher.Sum(nil))
	if got != cr.Checksum {
		return fmt.Errorf("checksum mismatch: want %s got %s — aborting update", cr.Checksum, got)
	}
	log.Printf("Updater: checksum verified (%s)", got[:12]+"...")

	// Make the new binary executable.
	if err := os.Chmod(tmpPath, 0755); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}

	// Atomically replace the running binary.
	if err := os.Rename(tmpPath, exePath); err != nil {
		return fmt.Errorf("rename binary: %w", err)
	}

	log.Printf("Updater: binary replaced, re-execing into version %s...", cr.Version)

	// Re-exec: replace this process image with the new binary.
	// os.Args[0] keeps the same invocation arguments.
	return syscall.Exec(exePath, os.Args, os.Environ())
}
