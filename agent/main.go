package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/devicepulse/agent/collector"
	"github.com/devicepulse/agent/queue"
	"github.com/devicepulse/agent/updater"
)

// defaultAPIURL is the fallback API endpoint. Override at build time with:
//
//	go build -ldflags "-X main.defaultAPIURL=https://your-ec2-domain.com"
var defaultAPIURL = "http://localhost:8080"

// agentVersion is the current binary version. Override at build time with:
//
//	go build -ldflags "-X main.agentVersion=1.2.0"
var agentVersion = "0.0.1-dev"

// apiURL is the resolved endpoint used at runtime.
// Priority: DEVICEPULSE_API_URL env var → defaultAPIURL (build-time default).
var apiURL string

// dataDir is the directory used for the SQLite queue and registration.json.
// Resolved in main() — see resolveDataDir().
var dataDir string

// apiKey is populated after device registration
var (
	apiKey   string
	deviceID string

	policyMu     sync.RWMutex
	syncInterval = 10 * time.Second
)

// resolveDataDir returns the absolute path to the agent's data directory.
// Priority: DEVICEPULSE_DATA_DIR env var → OS-specific default.
//
//	Windows : C:\ProgramData\DevicePulse\Agent
//	Linux   : /var/lib/devicepulse
//	macOS   : /var/lib/devicepulse
func resolveDataDir() string {
	if v := os.Getenv("DEVICEPULSE_DATA_DIR"); v != "" {
		return v
	}
	switch runtime.GOOS {
	case "windows":
		// %ProgramData% is always set on Windows (C:\ProgramData).
		base := os.Getenv("ProgramData")
		if base == "" {
			base = `C:\ProgramData`
		}
		return filepath.Join(base, "DevicePulse", "Agent")
	default:
		// Linux and macOS both use /var/lib/devicepulse when running as root
		// via systemd / LaunchDaemon.
		return "/var/lib/devicepulse"
	}
}

func main() {
	fmt.Println("DevicePulse Agent starting...")

	// Resolve API URL: env var takes priority over build-time default.
	apiURL = defaultAPIURL
	if v := os.Getenv("DEVICEPULSE_API_URL"); v != "" {
		apiURL = v
	}
	log.Printf("API endpoint: %s", apiURL)

	// Resolve data directory.
	// Priority: DEVICEPULSE_DATA_DIR env var → platform default.
	//   Windows : C:\ProgramData\DevicePulse\Agent
	//   macOS   : /var/lib/devicepulse   (LaunchDaemon WorkingDirectory)
	//   Linux   : /var/lib/devicepulse   (systemd WorkingDirectory)
	// The env var allows overriding in dev/test without recompiling.
	dataDir = resolveDataDir()
	log.Printf("Data directory: %s", dataDir)

	// DEVICEPULSE_MODE controls which collectors are active.
	//   (empty)       — all collectors (default, root/system service)
	//   window_only   — only ActiveWindowTracker (Linux user session service)
	mode := os.Getenv("DEVICEPULSE_MODE")
	if mode == "window_only" {
		runWindowOnlyMode()
		return
	}

	// 1. Initialize SQLite offline queue
	os.MkdirAll(dataDir, 0755)
	q, err := queue.NewQueue(filepath.Join(dataDir, "devicepulse.db"))
	if err != nil {
		log.Fatalf("Failed to initialize queue: %v", err)
	}

	// 2. Register device and obtain API key
	if err := registerDevice(); err != nil {
		log.Fatalf("Device registration failed: %v", err)
	}
	log.Printf("Registered as device_id=%s", deviceID)

	// 3. Start Update Poller — checks for new binary every hour
	go updater.Poller(apiURL, apiKey, agentVersion, 1*time.Hour)

	// 4. Start Policy Poller
	go policyPoller()

	// 5. Start Sync Engine (Drains SQLite to API)
	go syncEngine(q)

	// ── Collectors ──────────────────────────────────────────────────────────────
	sysInfo      := &collector.SystemInfo{}
	procMon      := &collector.ProcessMonitor{}
	browserHist  := &collector.BrowserHistory{}
	hwStats      := &collector.HardwareStats{}
	activeWin    := &collector.ActiveWindowTracker{}
	if err := activeWin.Start(); err != nil {
		log.Printf("ActiveWindowTracker failed to start: %v", err)
	}

	// Fast collectors — run every sync cycle.
	services  := &collector.Services{}
	netPorts  := &collector.NetworkPorts{}
	usbEvents := &collector.USBEvents{}

	// Slow collectors — run in background on a fixed 60-second interval.
	// Results are cached and included in every payload until refreshed.
	installedApps := &collector.InstalledApps{}
	osUpdates     := &collector.OSUpdates{}

	type slowCache struct {
		mu    sync.RWMutex
		apps  map[string]interface{}
		osUpd map[string]interface{}
	}
	cache := &slowCache{}

	// Run slow collectors once immediately in a goroutine, then every 60s.
	go func() {
		for {
			if v, err := installedApps.Collect(); err == nil {
				cache.mu.Lock()
				cache.apps = v
				cache.mu.Unlock()
				log.Printf("Slow collector: InstalledApps refreshed")
			} else {
				log.Printf("Error collecting installed apps: %v", err)
			}

			if v, err := osUpdates.Collect(); err == nil {
				cache.mu.Lock()
				cache.osUpd = v
				cache.mu.Unlock()
				log.Printf("Slow collector: OSUpdates refreshed")
			} else {
				log.Printf("Error collecting OS updates: %v", err)
			}

			time.Sleep(60 * time.Second)
		}
	}()

	// 5. Main Collection Loop
	for {
		sysPayload, err := sysInfo.Collect()
		if err != nil {
			log.Printf("Error collecting system info: %v", err)
		}

		procPayload, err := procMon.Collect()
		if err != nil {
			log.Printf("Error collecting process info: %v", err)
		}

		histPayload, err := browserHist.Collect()
		if err != nil {
			log.Printf("Error collecting browser history: %v", err)
		}

		hwPayload, err := hwStats.Collect()
		if err != nil {
			log.Printf("Error collecting hardware stats: %v", err)
		}

		svcPayload, err := services.Collect()
		if err != nil {
			log.Printf("Error collecting services: %v", err)
		}

		portsPayload, err := netPorts.Collect()
		if err != nil {
			log.Printf("Error collecting network ports: %v", err)
		}

		usbPayload, err := usbEvents.Collect()
		if err != nil {
			log.Printf("Error collecting USB events: %v", err)
		}

		activeWinPayload, err := activeWin.Collect()
		if err != nil {
			log.Printf("Error collecting active window: %v", err)
		}

		// Read slow-collector cache (non-blocking)
		cache.mu.RLock()
		appsPayload  := cache.apps
		osUpdPayload := cache.osUpd
		cache.mu.RUnlock()

		dataMap := map[string]interface{}{
			sysInfo.Name():     sysPayload,
			procMon.Name():     procPayload,
			browserHist.Name(): histPayload,
			hwStats.Name():     hwPayload,
			services.Name():    svcPayload,
			netPorts.Name():    portsPayload,
			usbEvents.Name():   usbPayload,
			activeWin.Name():   activeWinPayload,
		}
		// Only include slow payloads once they have been collected at least once
		if appsPayload != nil {
			dataMap[installedApps.Name()] = appsPayload
		}
		if osUpdPayload != nil {
			dataMap[osUpdates.Name()] = osUpdPayload
		}

		finalPayload := map[string]interface{}{
			"device_id": deviceID,
			"timestamp": time.Now().Format(time.RFC3339),
			"data":      dataMap,
		}

		if err := q.Push(finalPayload); err != nil {
			log.Printf("Failed to push to local queue: %v", err)
		} else {
			log.Printf("Saved telemetry to local queue")
		}

		policyMu.RLock()
		interval := syncInterval
		policyMu.RUnlock()

		time.Sleep(interval)
	}
}

// registerDevice calls POST /devices/register and stores the device_id + api_key.
//
// Strategy:
//  1. Collect the hardware fingerprint (UUID + MAC).
//  2. If a cached registration.json exists AND its fingerprint matches the
//     current hardware, reuse those credentials without hitting the API.
//  3. Otherwise register (or re-register) with the API, which will return
//     existing credentials if the hardware was seen before.
func registerDevice() error {
	regFile := filepath.Join(dataDir, "registration.json")

	fp := collector.GetHardwareFingerprint()
	log.Printf("Hardware fingerprint: %s", fp)

	// Try loading cached credentials
	if data, err := os.ReadFile(regFile); err == nil {
		var reg map[string]string
		if json.Unmarshal(data, &reg) == nil {
			cachedUUID := reg["hardware_uuid"]
			cachedMAC  := reg["mac_address"]
			// Accept cached creds only if the hardware identifiers still match.
			// This handles the case where the binary is copied to a different machine.
			if reg["device_id"] != "" && reg["api_key"] != "" &&
				cachedUUID == fp.HardwareUUID && cachedMAC == fp.MACAddress {
				deviceID = reg["device_id"]
				apiKey   = reg["api_key"]
				log.Printf("Loaded cached registration: device_id=%s", deviceID)
				return nil
			}
			log.Printf("Fingerprint mismatch or incomplete cache — re-registering")
		}
	}

	// Build registration payload with full fingerprint
	payload := map[string]string{
		"hostname":      fp.Hostname,
		"hardware_uuid": fp.HardwareUUID,
		"mac_address":   fp.MACAddress,
	}
	body, _ := json.Marshal(payload)

	resp, err := http.Post(apiURL+"/devices/register", "application/json", bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("registration request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("registration returned status %d", resp.StatusCode)
	}

	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to decode registration response: %w", err)
	}

	deviceID = result["device_id"]
	apiKey   = result["api_key"]

	if deviceID == "" || apiKey == "" {
		return fmt.Errorf("registration response missing device_id or api_key")
	}

	// Persist credentials + fingerprint so we can validate on next startup
	reg := map[string]string{
		"device_id":     deviceID,
		"api_key":       apiKey,
		"hardware_uuid": fp.HardwareUUID,
		"mac_address":   fp.MACAddress,
	}
	if data, err := json.MarshalIndent(reg, "", "  "); err == nil {
		os.WriteFile(regFile, data, 0600)
	}

	return nil
}

// policyPoller fetches policy rules from the API
func policyPoller() {
	for {
		req, _ := http.NewRequest(http.MethodGet, apiURL+"/policy", nil)
		req.Header.Set("X-API-Key", apiKey)
		client := &http.Client{Timeout: 5 * time.Second}

		resp, err := client.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			var pol map[string]interface{}
			if err := json.NewDecoder(resp.Body).Decode(&pol); err == nil {
				if v, ok := pol["sync_interval_seconds"].(float64); ok {
					newInterval := time.Duration(v) * time.Second
					policyMu.Lock()
					if syncInterval != newInterval {
						log.Printf("Policy Update: sync interval -> %v", newInterval)
						syncInterval = newInterval
					}
					policyMu.Unlock()
				}
			}
			resp.Body.Close()
		}
		time.Sleep(10 * time.Second)
	}
}

// syncEngine drains the local SQLite queue and uploads to the API
func syncEngine(q *queue.Queue) {
	for {
		items, err := q.PopBatch(50)
		if err != nil {
			log.Printf("Queue read error: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		if len(items) > 0 {
			var sentIDs []int
			client := &http.Client{Timeout: 10 * time.Second}

			for _, item := range items {
				req, err := http.NewRequest(http.MethodPost, apiURL+"/ingest",
					bytes.NewBuffer([]byte(item.Payload)))
				if err != nil {
					continue
				}
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-API-Key", apiKey)

				resp, err := client.Do(req)
				if err == nil && resp.StatusCode == http.StatusOK {
					sentIDs = append(sentIDs, item.ID)
					resp.Body.Close()
				}
			}

			if len(sentIDs) > 0 {
				if err := q.MarkSent(sentIDs); err != nil {
					log.Printf("Error clearing sent items: %v", err)
				} else {
					log.Printf("SyncEngine: uploaded %d items", len(sentIDs))
				}
			}
		}

		time.Sleep(5 * time.Second)
	}
}

// runWindowOnlyMode is entered when DEVICEPULSE_MODE=window_only.
// Used by the Linux per-user systemd service (devicepulse-agent-window.service)
// which runs as the logged-in desktop user and has access to $DISPLAY /
// $WAYLAND_DISPLAY. It collects ONLY active window focus data and pushes it
// to the same shared SQLite queue that the root service drains.
//
// All other collectors are disabled to avoid duplicate telemetry.
func runWindowOnlyMode() {
	log.Printf("Starting in window_only mode (user session active window tracker)")

	// Shared queue — root service owns the directory; we just write to it.
	// The data dir must be world-writable (mode 0777) or group-writable for
	// the logged-in user. The postinst script sets it to 0777.
	os.MkdirAll(dataDir, 0777)
	q, err := queue.NewQueue(filepath.Join(dataDir, "devicepulse.db"))
	if err != nil {
		log.Fatalf("window_only: failed to open queue: %v", err)
	}

	// Registration credentials are written by the root service on first boot.
	// We load them here so we can tag payloads with the correct device_id.
	regFile := filepath.Join(dataDir, "registration.json")
	if data, err := os.ReadFile(regFile); err == nil {
		var reg map[string]string
		if json.Unmarshal(data, &reg) == nil {
			deviceID = reg["device_id"]
			apiKey = reg["api_key"]
		}
	}
	if deviceID == "" {
		// Root service hasn't registered yet — wait and retry.
		log.Printf("window_only: waiting for root service to complete registration...")
		for i := 0; i < 30; i++ {
			time.Sleep(2 * time.Second)
			if data, err := os.ReadFile(regFile); err == nil {
				var reg map[string]string
				if json.Unmarshal(data, &reg) == nil && reg["device_id"] != "" {
					deviceID = reg["device_id"]
					apiKey = reg["api_key"]
					break
				}
			}
		}
		if deviceID == "" {
			log.Fatalf("window_only: device not registered after 60s, exiting")
		}
	}
	log.Printf("window_only: using device_id=%s", deviceID)

	activeWin := &collector.ActiveWindowTracker{}
	if err := activeWin.Start(); err != nil {
		log.Fatalf("window_only: ActiveWindowTracker failed to start: %v", err)
	}
	defer activeWin.Stop()

	// Use the same sync interval as the main service (default 10s).
	// Policy poller is not started here — the root service handles that.
	interval := 10 * time.Second

	for {
		payload, err := activeWin.Collect()
		if err != nil {
			log.Printf("window_only: collect error: %v", err)
			time.Sleep(interval)
			continue
		}

		finalPayload := map[string]interface{}{
			"device_id": deviceID,
			"timestamp": time.Now().Format(time.RFC3339),
			"data": map[string]interface{}{
				activeWin.Name(): payload,
			},
		}

		if err := q.Push(finalPayload); err != nil {
			log.Printf("window_only: queue push error: %v", err)
		} else {
			log.Printf("window_only: active window data queued")
		}

		time.Sleep(interval)
	}
}
