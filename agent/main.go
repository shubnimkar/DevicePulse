package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	osuser "os/user"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/devicepulse/agent/collector"
	"github.com/devicepulse/agent/commander"
	"github.com/devicepulse/agent/queue"
	"github.com/devicepulse/agent/updater"
	"github.com/joho/godotenv"
)

// defaultAPIURL is the fallback API endpoint. Override at build time with:
//
//	go build -ldflags "-X main.defaultAPIURL=https://your-ec2-domain.com"
var defaultAPIURL = "http://localhost:8000"

// agentVersion is the current binary version. Override at build time with:
//
//	go build -ldflags "-X main.agentVersion=1.2.0"
var agentVersion = "0.0.1-dev"

const maxTelemetryPayloadBytes = 5 * 1024 * 1024

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

	policyMu              sync.RWMutex
	syncInterval          = 10 * time.Second
	collectSystemInfo     = true
	collectHardwareStats  = true
	collectProcesses      = true
	collectBrowserHistory = true
	collectActiveWindow   = true
	collectServices       = true
	collectNetworkPorts   = true
	collectInstalledApps  = true
	collectOSUpdates      = true
	collectUSBDevices     = true
	browserHistoryMode    = "full_url"
	browserHistoryLimit   = 200 // show up to 200 recent entries by default (was 10)
	registerMu            sync.Mutex
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
	if runPlatformService() {
		return
	}
	runAgent()
}

func runAgent() {
	// Load .env if present — allows setting DEVICEPULSE_API_URL, DEVICEPULSE_DATA_DIR,
	// etc. without rebuilding. Silently ignored in production where env vars are
	// injected by systemd/launchd/container runtime.
	if err := godotenv.Load(); err == nil {
		log.Println("Loaded .env file")
	}

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
	configureLogging(dataDir)
	log.Println("DevicePulse Agent starting...")
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

	// 3. Start remote-command poller (collect_now / restart / lock /
	//    quarantine / wipe). Credentials were pushed to the commander during
	//    registration.
	commander.SetDataDir(dataDir)
	go commander.Poller(5 * time.Second)

	// 4. Start Update Poller — checks for new binary every few minutes
	go updater.Poller(apiURL, apiKey, agentVersion, 5*time.Minute)

	// 4. Start Policy Poller
	go policyPoller()

	// 5. Start Sync Engine (Drains SQLite to API)
	go syncEngine(q)

	// ── Collectors ──────────────────────────────────────────────────────────────
	sysInfo := &collector.SystemInfo{}
	procMon := &collector.ProcessMonitor{}
	browserHist := collector.NewBrowserHistory(filepath.Join(dataDir, "browser_history_state.json"))
	hwStats := &collector.HardwareStats{}
	// Linux uses a separate user-session service for active-window data. The
	// root service cannot reliably see the user's GUI session and running both
	// collectors would submit duplicate focus intervals.
	var activeWin *collector.ActiveWindowTracker
	if runtime.GOOS != "linux" {
		activeWin = &collector.ActiveWindowTracker{}
		if err := activeWin.Start(); err != nil {
			log.Printf("ActiveWindowTracker failed to start: %v", err)
		}
	}

	// Fast collectors — run every sync cycle.
	services := &collector.Services{}
	netPorts := &collector.NetworkPorts{}
	usbEvents := &collector.USBEvents{}

	// Slow collectors — run in background on a fixed 60-second interval.
	// Results are cached and included in every payload until refreshed.
	installedApps := &collector.InstalledApps{}
	osUpdates := &collector.OSUpdates{}

	type slowCache struct {
		mu    sync.RWMutex
		apps  map[string]interface{}
		osUpd map[string]interface{}
	}
	cache := &slowCache{}

	// Run slow collectors once immediately in a goroutine, then every 60s.
	go func() {
		for {
			policyMu.RLock()
			shouldCollectApps := collectInstalledApps
			shouldCollectUpdates := collectOSUpdates
			policyMu.RUnlock()

			if shouldCollectApps {
				if v, err := installedApps.Collect(); err == nil {
					cache.mu.Lock()
					cache.apps = v
					cache.mu.Unlock()
					log.Printf("Slow collector: InstalledApps refreshed")
				} else {
					log.Printf("Error collecting installed apps: %v", err)
				}
			} else {
				cache.mu.Lock()
				cache.apps = nil
				cache.mu.Unlock()
			}

			if shouldCollectUpdates {
				if v, err := osUpdates.Collect(); err == nil {
					cache.mu.Lock()
					cache.osUpd = v
					cache.mu.Unlock()
					log.Printf("Slow collector: OSUpdates refreshed")
				} else {
					log.Printf("Error collecting OS updates: %v", err)
				}
			} else {
				cache.mu.Lock()
				cache.osUpd = nil
				cache.mu.Unlock()
			}

			time.Sleep(60 * time.Second)
		}
	}()

	// 5. Main Collection Loop
	agentSessionID := newAgentSessionID()
	bootID := readBootID()
	var sequence uint64
	for {
		policyMu.RLock()
		shouldCollectSystem := collectSystemInfo
		shouldCollectHardware := collectHardwareStats
		shouldCollectProcesses := collectProcesses
		collectHistory := collectBrowserHistory
		shouldCollectActiveWindow := collectActiveWindow
		shouldCollectServices := collectServices
		shouldCollectPorts := collectNetworkPorts
		shouldCollectUSB := collectUSBDevices
		historyMode := browserHistoryMode
		historyLimit := browserHistoryLimit
		policyMu.RUnlock()

		dataMap := map[string]interface{}{}
		var err error

		if shouldCollectSystem {
			if sysPayload, err := sysInfo.Collect(); err != nil {
				log.Printf("Error collecting system info: %v", err)
			} else {
				dataMap[sysInfo.Name()] = sysPayload
			}
		}

		if shouldCollectProcesses {
			if procPayload, err := procMon.Collect(); err != nil {
				log.Printf("Error collecting process info: %v", err)
			} else {
				dataMap[procMon.Name()] = procPayload
			}
		}

		var histPayload map[string]interface{}
		if collectHistory {
			histPayload, err = browserHist.Collect()
			if err != nil {
				log.Printf("Error collecting browser history: %v", err)
			} else {
				applyBrowserHistoryPolicy(histPayload, historyMode, historyLimit)
				dataMap[browserHist.Name()] = histPayload
			}
		}

		if shouldCollectHardware {
			if hwPayload, err := hwStats.Collect(); err != nil {
				log.Printf("Error collecting hardware stats: %v", err)
			} else {
				dataMap[hwStats.Name()] = hwPayload
			}
		}

		if shouldCollectServices {
			if svcPayload, err := services.Collect(); err != nil {
				log.Printf("Error collecting services: %v", err)
			} else {
				dataMap[services.Name()] = svcPayload
			}
		}

		if shouldCollectPorts {
			if portsPayload, err := netPorts.Collect(); err != nil {
				log.Printf("Error collecting network ports: %v", err)
			} else {
				dataMap[netPorts.Name()] = portsPayload
			}
		}

		if shouldCollectUSB {
			if usbPayload, err := usbEvents.Collect(); err != nil {
				log.Printf("Error collecting USB events: %v", err)
			} else {
				dataMap[usbEvents.Name()] = usbPayload
			}
		}

		if shouldCollectActiveWindow && activeWin != nil {
			if activeWinPayload, err := activeWin.Collect(); err != nil {
				log.Printf("Error collecting active window: %v", err)
			} else {
				dataMap[activeWin.Name()] = activeWinPayload
			}
		}

		// Read slow-collector cache (non-blocking)
		cache.mu.RLock()
		appsPayload := cache.apps
		osUpdPayload := cache.osUpd
		cache.mu.RUnlock()

		// Only include slow payloads once they have been collected at least once
		if appsPayload != nil {
			dataMap[installedApps.Name()] = appsPayload
		}
		if osUpdPayload != nil {
			dataMap[osUpdates.Name()] = osUpdPayload
		}

		finalPayload := map[string]interface{}{
			"device_id":        deviceID,
			"event_id":         fmt.Sprintf("%s-%d", agentSessionID, sequence),
			"agent_session_id": agentSessionID,
			"boot_id":          bootID,
			"sequence":         sequence,
			"timestamp":        time.Now().Format(time.RFC3339),
			"username":         currentUsername(),
			"agent_version":    agentVersion,
			"agent_os":         runtime.GOOS,
			"agent_arch":       runtime.GOARCH,
			"collector_role":   map[bool]string{true: "active_window", false: "system"}[activeWin != nil],
			"data":             dataMap,
		}
		sequence++

		if err := q.Push(finalPayload); err != nil {
			log.Printf("Failed to push to local queue: %v", err)
		} else {
			if err := browserHist.Commit(); err != nil {
				log.Printf("Failed to commit browser history cursor: %v", err)
			}
			log.Printf("Saved telemetry to local queue")
		}

		policyMu.RLock()
		interval := syncInterval
		policyMu.RUnlock()

		// Sleep until the next cycle, or wake immediately when a collect_now
		// command arrives from the commander poller.
		select {
		case <-commander.CollectNowChan():
			log.Printf("Remote command: collect_now — starting next cycle immediately")
		case <-time.After(interval):
		}
	}
}

func currentUsername() string {
	if u, err := osuser.Current(); err == nil && u != nil {
		if u.Username != "" {
			parts := strings.FieldsFunc(u.Username, func(r rune) bool {
				return r == '\\' || r == '/'
			})
			if len(parts) > 0 {
				return parts[len(parts)-1]
			}
			return u.Username
		}
	}
	for _, key := range []string{"USER", "USERNAME", "LOGNAME"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return "unknown"
}

func newAgentSessionID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("session-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func readBootID() string {
	if data, err := os.ReadFile("/proc/sys/kernel/random/boot_id"); err == nil {
		if value := strings.TrimSpace(string(data)); value != "" {
			return value
		}
	}
	return "unknown"
}

func configureLogging(dir string) {
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}
	logFile := filepath.Join(dir, "agent.log")
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
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
	return registerDeviceWithCache(true)
}

func registerDeviceWithCache(useCache bool) error {
	registerMu.Lock()
	defer registerMu.Unlock()

	regFile := filepath.Join(dataDir, "registration.json")

	fp := collector.GetHardwareFingerprint()
	log.Printf("Hardware fingerprint: %s", fp)

	// Try loading cached credentials
	if useCache {
		if data, err := os.ReadFile(regFile); err == nil {
			var reg map[string]string
			if json.Unmarshal(data, &reg) == nil {
				cachedUUID := reg["hardware_uuid"]
				cachedMAC := reg["mac_address"]
				// Accept cached creds only if the hardware identifiers still match.
				// This handles the case where the binary is copied to a different machine.
				if reg["device_id"] != "" && reg["api_key"] != "" &&
					cachedUUID == fp.HardwareUUID && cachedMAC == fp.MACAddress {
					deviceID = reg["device_id"]
					apiKey = reg["api_key"]
					commander.SetCredentials(apiURL, apiKey)
					log.Printf("Loaded cached registration: device_id=%s", deviceID)
					return nil
				}
				log.Printf("Fingerprint mismatch or incomplete cache — re-registering")
			}
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
	apiKey = result["api_key"]

	if deviceID == "" || apiKey == "" {
		return fmt.Errorf("registration response missing device_id or api_key")
	}
	commander.SetCredentials(apiURL, apiKey)

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

// policyPoller fetches global policy configuration from the API.
func policyPoller() {
	for {
		req, _ := http.NewRequest(http.MethodGet, apiURL+"/policy", nil)
		req.Header.Set("X-API-Key", apiKey)
		client := &http.Client{Timeout: 5 * time.Second}

		resp, err := client.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			var pol map[string]interface{}
			if err := json.NewDecoder(resp.Body).Decode(&pol); err == nil {
				policyMu.Lock()
				if v, ok := pol["sync_interval_seconds"].(float64); ok {
					newInterval := time.Duration(v) * time.Second
					if syncInterval != newInterval {
						log.Printf("Policy Update: sync interval -> %v", newInterval)
						syncInterval = newInterval
					}
				}
				if v, ok := pol["collect_system_info"].(bool); ok {
					collectSystemInfo = v
				}
				if v, ok := pol["collect_hardware_stats"].(bool); ok {
					collectHardwareStats = v
				}
				if v, ok := pol["collect_processes"].(bool); ok {
					collectProcesses = v
				}
				if v, ok := pol["collect_browser_history"].(bool); ok {
					collectBrowserHistory = v
				}
				if v, ok := pol["collect_active_window"].(bool); ok {
					collectActiveWindow = v
				}
				if v, ok := pol["collect_services"].(bool); ok {
					collectServices = v
				}
				if v, ok := pol["collect_network_ports"].(bool); ok {
					collectNetworkPorts = v
				}
				if v, ok := pol["collect_installed_apps"].(bool); ok {
					collectInstalledApps = v
				}
				if v, ok := pol["collect_os_updates"].(bool); ok {
					collectOSUpdates = v
				}
				if v, ok := pol["collect_usb_devices"].(bool); ok {
					collectUSBDevices = v
				}
				if v, ok := pol["browser_history_mode"].(string); ok {
					browserHistoryMode = v
				}
				if v, ok := pol["browser_history_limit"].(float64); ok {
					browserHistoryLimit = int(v)
				}
				if browserHistoryMode == "disabled" {
					collectBrowserHistory = false
				}
				policyMu.Unlock()
			}
			resp.Body.Close()
		}
		time.Sleep(10 * time.Second)
	}
}

func applyBrowserHistoryPolicy(payload map[string]interface{}, mode string, limit int) {
	if limit < 0 {
		limit = 0
	}
	for _, key := range []string{"top_recent_urls", "new_history_entries"} {
		entries, ok := payload[key].([]collector.HistoryEntry)
		if !ok {
			continue
		}
		if mode == "domain_only" {
			for i := range entries {
				entries[i].URL = domainOnlyURL(entries[i].URL)
				entries[i].Title = ""
			}
		}
		// Apply the limit to both recent and new entries so the admin always
		// sees a meaningful number of rows.  limit=0 means no cap.
		if limit > 0 && len(entries) > limit {
			entries = entries[:limit]
		}
		payload[key] = entries
	}
}

func domainOnlyURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return raw
	}
	return parsed.Scheme + "://" + parsed.Host
}

// syncEngine drains the local SQLite queue and uploads newest telemetry first so
// the live dashboard recovers quickly after API downtime.
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
			droppedCount := 0
			client := &http.Client{Timeout: 2 * time.Minute}

			for _, item := range items {
				if len(item.Payload) > maxTelemetryPayloadBytes {
					log.Printf("SyncEngine: dropping oversized queue item %d (%d bytes > %d bytes)",
						item.ID, len(item.Payload), maxTelemetryPayloadBytes)
					sentIDs = append(sentIDs, item.ID)
					droppedCount++
					continue
				}

				status, sent := uploadQueuedItem(client, item)
				if sent {
					sentIDs = append(sentIDs, item.ID)
					continue
				}

				if status == http.StatusUnauthorized {
					log.Printf("SyncEngine: cached credentials rejected; re-registering device")
					if err := registerDeviceWithCache(false); err != nil {
						log.Printf("SyncEngine: re-registration failed: %v", err)
						continue
					}

					if _, sent := uploadQueuedItem(client, item); sent {
						sentIDs = append(sentIDs, item.ID)
					}
				}
			}

			if len(sentIDs) > 0 {
				if err := q.MarkSent(sentIDs); err != nil {
					log.Printf("Error clearing sent items: %v", err)
				} else {
					uploadedCount := len(sentIDs) - droppedCount
					if droppedCount > 0 {
						log.Printf("SyncEngine: cleared %d items (%d uploaded, %d dropped)",
							len(sentIDs), uploadedCount, droppedCount)
					} else {
						log.Printf("SyncEngine: uploaded %d items", uploadedCount)
					}
				}
			}
		}

		time.Sleep(5 * time.Second)
	}
}

func uploadQueuedItem(client *http.Client, item queue.TelemetryItem) (int, bool) {
	req, err := http.NewRequest(http.MethodPost, apiURL+"/ingest",
		bytes.NewBuffer([]byte(item.Payload)))
	if err != nil {
		log.Printf("SyncEngine: failed to build request for queue item %d: %v", item.ID, err)
		return 0, false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("SyncEngine: upload failed for queue item %d: %v", item.ID, err)
		return 0, false
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		var result struct {
			CheckUpdateNow bool `json:"check_update_now"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err == nil && result.CheckUpdateNow {
			log.Printf("SyncEngine: server requested immediate agent update check")
			go func() {
				if err := updater.CheckAndUpdate(apiURL, apiKey, agentVersion); err != nil {
					log.Printf("Updater: immediate check failed: %v", err)
				}
			}()
		}
		return resp.StatusCode, true
	}

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	log.Printf("SyncEngine: upload failed for queue item %d: status %d: %s",
		item.ID, resp.StatusCode, string(bytes.TrimSpace(body)))
	return resp.StatusCode, false
}

// runWindowOnlyMode is entered when DEVICEPULSE_MODE=window_only.
// Used by the per-user desktop service which runs as the logged-in user and has
// access to the GUI session. It collects active window focus data, then pushes
// it to the shared SQLite queue that the root service drains.
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

	// Registration credentials are written by the root service on first boot
	// with owner-only permissions. If this low-privilege user service cannot
	// read them, it can still queue active-window payloads; the root service
	// drains the shared queue and authenticates the upload.
	regFile := filepath.Join(dataDir, "registration.json")
	if data, err := os.ReadFile(regFile); err == nil {
		var reg map[string]string
		if json.Unmarshal(data, &reg) == nil {
			deviceID = reg["device_id"]
			apiKey = reg["api_key"]
		}
	} else {
		log.Printf("window_only: registration cache not readable (%v); queueing without local credentials", err)
	}
	if deviceID == "" {
		// Root service may not have registered yet, or registration.json may be
		// intentionally private. Retry briefly for installs that have not
		// finished bootstrapping, then keep running without local credentials.
		log.Printf("window_only: waiting briefly for root service registration...")
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
			log.Printf("window_only: continuing without registration cache; root sync will attach device identity")
		}
	}
	if deviceID != "" && apiKey != "" {
		log.Printf("window_only: using device_id=%s", deviceID)
		commander.SetCredentials(apiURL, apiKey)
		commander.SetDataDir(dataDir)

		// The window service also polls remote commands: session-context actions
		// (lock_screen) must run here because the root system service cannot reach
		// the GUI session. Command claiming is atomic server-side, so both this
		// process and the root service can poll concurrently without conflicts.
		go commander.Poller(5 * time.Second)
	} else {
		log.Printf("window_only: command polling disabled until credentials are readable")
	}

	activeWin := &collector.ActiveWindowTracker{}
	if err := activeWin.Start(); err != nil {
		log.Fatalf("window_only: ActiveWindowTracker failed to start: %v", err)
	}
	defer activeWin.Stop()

	// Use the same sync interval as the main service (default 10s).
	// Policy poller is not started here — the root service handles that.
	interval := 10 * time.Second
	agentSessionID := newAgentSessionID()
	bootID := readBootID()
	var sequence uint64

	for {
		payload, err := activeWin.Collect()
		if err != nil {
			log.Printf("window_only: collect error: %v", err)
			time.Sleep(interval)
			continue
		}
		finalPayload := map[string]interface{}{
			"device_id":        deviceID,
			"event_id":         fmt.Sprintf("%s-%d", agentSessionID, sequence),
			"agent_session_id": agentSessionID,
			"boot_id":          bootID,
			"sequence":         sequence,
			"timestamp":        time.Now().Format(time.RFC3339),
			"username":         currentUsername(),
			"agent_version":    agentVersion,
			"agent_os":         runtime.GOOS,
			"agent_arch":       runtime.GOARCH,
			"collector_role":   "active_window",
			"data": map[string]interface{}{
				activeWin.Name(): payload,
			},
		}
		sequence++

		if err := q.Push(finalPayload); err != nil {
			log.Printf("window_only: queue push error: %v", err)
		} else {
			log.Printf("window_only: active window data queued")
		}

		time.Sleep(interval)
	}
}
