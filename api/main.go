package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/joho/godotenv"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// ─── Globals ──────────────────────────────────────────────────────────────────

var (
	mongoClient *mongo.Client
	dbName      = "devicepulse"
	// adminSecret is loaded from ADMIN_SECRET env var and required for
	// privileged endpoints: POST /policy and POST /update/release.
	adminSecret string
)

// ─── Focus Cache ──────────────────────────────────────────────────────────────

// focusCache holds per-device cumulative focus totals aggregated from all
// telemetry documents. It is built once at startup from MongoDB, then updated
// incrementally on every ingest — no data is lost across agent restarts.

type AppFocusEntry struct {
	AppName      string  `json:"app_name"`
	TotalFocusS  float64 `json:"total_focus_seconds"`
	SessionCount int     `json:"session_count"`
}

type FocusCache struct {
	mu   sync.RWMutex
	data map[string]map[string]*AppFocusEntry // device_id -> app_name -> entry
}

var globalFocusCache = &FocusCache{
	data: make(map[string]map[string]*AppFocusEntry),
}

// applyFocusSummaries merges a slice of per-cycle summaries into the cache for
// a given device. Safe to call from multiple goroutines.
func (fc *FocusCache) applyFocusSummaries(deviceID string, summaries []interface{}) {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	apps, ok := fc.data[deviceID]
	if !ok {
		apps = make(map[string]*AppFocusEntry)
		fc.data[deviceID] = apps
	}

	for _, raw := range summaries {
		m, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := m["app_name"].(string)
		if name == "" {
			continue
		}
		dur, _      := toFloat(m["total_focus_seconds"])
		sessions, _ := toFloat(m["session_count"])

		e, ok := apps[name]
		if !ok {
			e = &AppFocusEntry{AppName: name}
			apps[name] = e
		}
		e.TotalFocusS  += dur
		e.SessionCount += int(sessions)
	}
}

// snapshot returns a sorted slice of AppFocusEntry for the given device.
func (fc *FocusCache) snapshot(deviceID string) []AppFocusEntry {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	apps := fc.data[deviceID]
	result := make([]AppFocusEntry, 0, len(apps))
	for _, e := range apps {
		result = append(result, *e)
	}
	// sort descending by focus time
	sort.Slice(result, func(i, j int) bool {
		return result[i].TotalFocusS > result[j].TotalFocusS
	})
	return result
}

// buildFocusCacheFromMongo reads the last 500 telemetry documents per device
// and reconstructs the focus cache. Called once at startup.
func buildFocusCacheFromMongo() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	coll := mongoClient.Database(dbName).Collection("telemetry")

	// Get distinct device IDs present in telemetry
	deviceIDs, err := coll.Distinct(ctx, "device_id", bson.M{})
	if err != nil {
		log.Printf("FocusCache: failed to list devices: %v", err)
		return
	}

	for _, rawID := range deviceIDs {
		deviceID, ok := rawID.(string)
		if !ok || deviceID == "" {
			continue
		}

		opts := options.Find().
			SetSort(bson.D{{Key: "_id", Value: -1}}).
			SetLimit(500).
			SetProjection(bson.M{"data.ActiveWindowTracker.app_summaries": 1, "_id": 0})

		cursor, err := coll.Find(ctx, bson.M{"device_id": deviceID}, opts)
		if err != nil {
			log.Printf("FocusCache: query error for %s: %v", deviceID, err)
			continue
		}

		count := 0
		for cursor.Next(ctx) {
			var doc bson.M
			if err := cursor.Decode(&doc); err != nil {
				continue
			}
			summaries := extractFocusSummaries(doc)
			if len(summaries) > 0 {
				globalFocusCache.applyFocusSummaries(deviceID, summaries)
				count++
			}
		}
		cursor.Close(ctx)
		log.Printf("FocusCache: built from %d telemetry docs for device %s", count, deviceID)
	}
}

// extractFocusSummaries pulls the app_summaries array out of a raw telemetry doc.
func extractFocusSummaries(doc bson.M) []interface{} {
	data, ok := doc["data"].(bson.M)
	if !ok {
		return nil
	}
	awt, ok := data["ActiveWindowTracker"].(bson.M)
	if !ok {
		return nil
	}
	summaries, _ := awt["app_summaries"].(bson.A)
	result := make([]interface{}, len(summaries))
	for i, s := range summaries {
		result[i] = s
	}
	return result
}

// ─── Policy Store ─────────────────────────────────────────────────────────────

type PolicyStore struct {
	mu     sync.RWMutex
	config map[string]interface{}
}

var globalPolicy = PolicyStore{
	config: map[string]interface{}{
		"sync_interval_seconds": 10,
	},
}

// loadPolicyFromMongo reads the persisted policy document from MongoDB.
// Called once at startup so the last-saved policy survives API restarts.
func loadPolicyFromMongo() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	coll := mongoClient.Database(dbName).Collection("config")
	var doc bson.M
	if err := coll.FindOne(ctx, bson.M{"_id": "global_policy"}).Decode(&doc); err != nil {
		// No saved policy yet — keep the default.
		return
	}
	delete(doc, "_id")

	globalPolicy.mu.Lock()
	for k, v := range doc {
		globalPolicy.config[k] = v
	}
	globalPolicy.mu.Unlock()
	log.Printf("Policy loaded from MongoDB: %v", globalPolicy.config)
}

// savePolicyToMongo persists the current policy config to MongoDB so it
// survives API restarts.
func savePolicyToMongo(cfg map[string]interface{}) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	update := bson.M{"$set": cfg}
	opts := options.Update().SetUpsert(true)
	_, err := mongoClient.Database(dbName).Collection("config").UpdateOne(
		ctx, bson.M{"_id": "global_policy"}, update, opts,
	)
	if err != nil {
		log.Printf("Policy persist error: %v", err)
	}
}

// ─── CORS Middleware ───────────────────────────────────────────────────────────

func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, Authorization, X-API-Key, X-Admin-Secret, X-Dashboard-Token")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next(w, r)
	}
}

// ─── Auth Middleware ───────────────────────────────────────────────────────────

// authMiddleware validates the X-API-Key header against the devices collection.
// It returns the device_id associated with the key, or "" if invalid.
func resolveAPIKey(ctx context.Context, key string) (string, bool) {
	if key == "" {
		return "", false
	}
	coll := mongoClient.Database(dbName).Collection("devices")
	var device bson.M
	err := coll.FindOne(ctx, bson.M{"api_key": key}).Decode(&device)
	if err != nil {
		return "", false
	}
	deviceID, _ := device["device_id"].(string)
	return deviceID, deviceID != ""
}

// requireAdmin is a middleware that enforces the X-Admin-Secret header.
// Returns 401 if the header is missing or doesn't match ADMIN_SECRET.
func requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if adminSecret == "" {
			// ADMIN_SECRET not configured — block all admin requests so a
			// misconfigured deployment doesn't accidentally allow open access.
			http.Error(w, "Admin access not configured (set ADMIN_SECRET)", http.StatusServiceUnavailable)
			return
		}
		if r.Header.Get("X-Admin-Secret") != adminSecret {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// requireRead enforces the X-Dashboard-Token header on read endpoints.
// This prevents unauthenticated access to device telemetry, browser history,
// and focus data from anyone who can reach port 8080.
// Set DASHBOARD_TOKEN to any strong random value (e.g. openssl rand -hex 32).
// If unset, read endpoints remain open (backwards-compatible default).
func requireRead(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := os.Getenv("DASHBOARD_TOKEN")
		if token == "" {
			// Not configured — allow through but log a warning once.
			// This preserves backwards compatibility for local dev.
			next(w, r)
			return
		}
		if r.Header.Get("X-Dashboard-Token") != token {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// ─── Registration Handler ──────────────────────────────────────────────────────

// POST /devices/register
//
// Body:
//
//	{
//	  "hostname":      "my-mac",
//	  "hardware_uuid": "A1B2C3...",   // gopsutil host.HostID — SMBIOS/IOKit UUID
//	  "mac_address":   "a4c3f0112233" // primary NIC MAC, no separators
//	}
//
// Returns: { "device_id": "...", "api_key": "..." }
//
// Deduplication priority: hardware_uuid → mac_address → hostname.
// If a device with the same hardware identity is found it returns the existing
// credentials so the agent recovers cleanly after a reinstall.
func registerHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body map[string]string
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Bad JSON", http.StatusBadRequest)
		return
	}

	hostname     := body["hostname"]
	hardwareUUID := body["hardware_uuid"]
	macAddress   := body["mac_address"]

	if hostname == "" {
		hostname = "unknown"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	coll := mongoClient.Database(dbName).Collection("devices")

	// Build a dedup filter in priority order.
	// We try the most stable identifier first.
	var dedupFilter bson.M
	switch {
	case hardwareUUID != "":
		dedupFilter = bson.M{"hardware_uuid": hardwareUUID}
	case macAddress != "":
		dedupFilter = bson.M{"mac_address": macAddress}
	default:
		dedupFilter = bson.M{"hostname": hostname}
	}

	var existing bson.M
	if err := coll.FindOne(ctx, dedupFilter).Decode(&existing); err == nil {
		// Device already registered — return existing credentials.
		// Also update hostname/mac/uuid in case they changed (e.g. NIC swap).
		update := bson.M{"$set": bson.M{
			"hostname":      hostname,
			"hardware_uuid": hardwareUUID,
			"mac_address":   macAddress,
			"last_seen":     time.Now(),
		}}
		coll.UpdateOne(ctx, dedupFilter, update)

		deviceID, _ := existing["device_id"].(string)
		apiKey, _   := existing["api_key"].(string)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"device_id": deviceID,
			"api_key":   apiKey,
		})
		return
	}

	// New device — mint a stable device_id derived from hardware UUID when available.
	var deviceID string
	if hardwareUUID != "" {
		deviceID = fmt.Sprintf("device-%s", hardwareUUID)
	} else if macAddress != "" {
		deviceID = fmt.Sprintf("device-mac-%s", macAddress)
	} else {
		deviceID = fmt.Sprintf("device-%s-%d", hostname, time.Now().UnixMilli())
	}

	apiKey := generateAPIKey()

	doc := bson.M{
		"device_id":     deviceID,
		"api_key":       apiKey,
		"hostname":      hostname,
		"hardware_uuid": hardwareUUID,
		"mac_address":   macAddress,
		"registered_at": time.Now(),
		"last_seen":     time.Now(),
		"status":        "active",
	}

	if _, err := coll.InsertOne(ctx, doc); err != nil {
		log.Printf("Registration insert error: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	log.Printf("New device registered: %s (uuid=%s mac=%s host=%s)",
		deviceID, hardwareUUID, macAddress, hostname)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"device_id": deviceID,
		"api_key":   apiKey,
	})
}

// generateAPIKey creates a cryptographically random 48-char hex key.
func generateAPIKey() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure is unrecoverable — the system entropy pool is
		// broken. Fatalf so we don't mint insecure keys silently.
		log.Fatalf("generateAPIKey: crypto/rand.Read failed: %v", err)
	}
	return hex.EncodeToString(b)
}

// ─── Ingest Handler ────────────────────────────────────────────────────────────

func ingestHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Authenticate
	apiKey := r.Header.Get("X-API-Key")
	authDeviceID, ok := resolveAPIKey(ctx, apiKey)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	body, err := io.ReadAll(r.Body)
	defer r.Body.Close()
	if err != nil {
		http.Error(w, "Error reading body", http.StatusInternalServerError)
		return
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "Error parsing JSON", http.StatusBadRequest)
		return
	}

	// Ensure device_id matches the registered key
	payload["device_id"] = authDeviceID

	collTelemetry := mongoClient.Database(dbName).Collection("telemetry")
	collDevices   := mongoClient.Database(dbName).Collection("devices")

	// Insert telemetry event
	if _, err = collTelemetry.InsertOne(ctx, payload); err != nil {
		log.Printf("Error inserting telemetry: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	// Upsert latest device state (store last-seen snapshot)
	opts := options.Update().SetUpsert(true)
	update := bson.M{"$set": bson.M{
		"device_id": authDeviceID,
		"timestamp": payload["timestamp"],
		"data":      payload["data"],
		"last_seen": time.Now(),
	}}
	if _, err = collDevices.UpdateOne(ctx, bson.M{"device_id": authDeviceID}, update, opts); err != nil {
		log.Printf("Error updating device state: %v", err)
	}

	// Update focus cache from the per-cycle app_summaries in this payload
	go func() {
		if data, ok := payload["data"].(map[string]interface{}); ok {
			if awt, ok := data["ActiveWindowTracker"].(map[string]interface{}); ok {
				if raw, ok := awt["app_summaries"].([]interface{}); ok && len(raw) > 0 {
					globalFocusCache.applyFocusSummaries(authDeviceID, raw)
				}
			}
		}
	}()

	log.Printf("Ingested telemetry for %s", authDeviceID)
	w.WriteHeader(http.StatusOK)
}

func toFloat(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	}
	return 0, false
}

// ─── Device Delete Handler ─────────────────────────────────────────────────────

// DELETE /devices/{device_id}
func deviceDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) < 2 || parts[1] == "" {
		http.Error(w, "Invalid URL: missing device_id", http.StatusBadRequest)
		return
	}
	deviceID := parts[1]

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := mongoClient.Database(dbName).Collection("devices").DeleteOne(ctx, bson.M{"device_id": deviceID})
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	if res.DeletedCount == 0 {
		http.Error(w, "Device not found", http.StatusNotFound)
		return
	}

	log.Printf("Device deleted: %s", deviceID)
	w.WriteHeader(http.StatusOK)
}

// ─── Devices Handler ───────────────────────────────────────────────────────────

func devicesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cursor, err := mongoClient.Database(dbName).Collection("devices").Find(ctx, bson.M{})
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var devices []bson.M
	if err = cursor.All(ctx, &devices); err != nil {
		http.Error(w, "Parsing error", http.StatusInternalServerError)
		return
	}

	// Strip api_key from the list response
	for _, d := range devices {
		delete(d, "api_key")
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(devices)
}

// ─── History Handler ───────────────────────────────────────────────────────────

func historyHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		http.Error(w, "Invalid URL", http.StatusBadRequest)
		return
	}
	deviceID := parts[2]

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	opts := options.Find().SetSort(bson.D{{Key: "_id", Value: -1}}).SetLimit(100)
	cursor, err := mongoClient.Database(dbName).Collection("telemetry").Find(ctx, bson.M{"device_id": deviceID}, opts)
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var history []bson.M
	if err = cursor.All(ctx, &history); err != nil {
		http.Error(w, "Parsing error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(history)
}

// ─── Policy Handler ────────────────────────────────────────────────────────────

func policyHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		globalPolicy.mu.RLock()
		defer globalPolicy.mu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(globalPolicy.config)

	case http.MethodPost:
		var newConfig map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&newConfig); err != nil {
			http.Error(w, "Bad JSON", http.StatusBadRequest)
			return
		}
		globalPolicy.mu.Lock()
		globalPolicy.config = newConfig
		globalPolicy.mu.Unlock()
		// Persist so the policy survives API restarts.
		go savePolicyToMongo(newConfig)
		log.Println("Global policy updated")
		w.WriteHeader(http.StatusOK)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// ─── Focus Handler ─────────────────────────────────────────────────────────────

// GET /focus/{device_id}
// Returns the persistent cumulative focus totals for a device.
func focusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) < 2 || parts[1] == "" {
		http.Error(w, "Missing device_id", http.StatusBadRequest)
		return
	}
	deviceID := parts[1]

	entries := globalFocusCache.snapshot(deviceID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"device_id":    deviceID,
		"app_summaries": entries,
	})
}

// ─── Update Check Handler ──────────────────────────────────────────────────────

// AgentRelease describes a published agent binary in the releases collection.
type AgentRelease struct {
	Version     string    `bson:"version"          json:"version"`
	OS          string    `bson:"os"               json:"os"`           // "darwin" | "linux" | "windows"
	Arch        string    `bson:"arch"             json:"arch"`         // "amd64" | "arm64"
	DownloadURL string    `bson:"download_url"     json:"download_url"`
	Checksum    string    `bson:"checksum_sha256"  json:"checksum_sha256"`
	PublishedAt time.Time `bson:"published_at"     json:"published_at"`
}

// GET /update/check?version=<current>&os=<goos>&arch=<goarch>
//
// Returns:
//
//	{ "update_available": false }                         — already latest
//	{ "update_available": true, "version": "...",
//	  "download_url": "...", "checksum_sha256": "..." }   — update ready
func updateCheckHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	currentVersion := r.URL.Query().Get("version")
	agentOS        := r.URL.Query().Get("os")
	agentArch      := r.URL.Query().Get("arch")

	if currentVersion == "" || agentOS == "" || agentArch == "" {
		http.Error(w, "Missing required query params: version, os, arch", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Find the latest release for this OS/arch, sorted by published_at desc.
	coll := mongoClient.Database(dbName).Collection("agent_releases")
	opts := options.FindOne().SetSort(bson.D{{Key: "published_at", Value: -1}})
	filter := bson.M{"os": agentOS, "arch": agentArch}

	var latest AgentRelease
	if err := coll.FindOne(ctx, filter, opts).Decode(&latest); err != nil {
		// No releases published yet — agent is up to date.
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"update_available": false})
		return
	}

	if latest.Version == currentVersion {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"update_available": false})
		return
	}

	log.Printf("Update available for %s/%s: %s → %s", agentOS, agentArch, currentVersion, latest.Version)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"update_available": true,
		"version":          latest.Version,
		"download_url":     latest.DownloadURL,
		"checksum_sha256":  latest.Checksum,
	})
}

// ─── Release Publish Handler ───────────────────────────────────────────────────

// POST /update/release  (admin use — publish a new agent release)
//
// Body: AgentRelease JSON
// Returns: 201 Created
func releasePublishHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var rel AgentRelease
	if err := json.NewDecoder(r.Body).Decode(&rel); err != nil {
		http.Error(w, "Bad JSON", http.StatusBadRequest)
		return
	}

	if rel.Version == "" || rel.OS == "" || rel.Arch == "" || rel.DownloadURL == "" || rel.Checksum == "" {
		http.Error(w, "version, os, arch, download_url and checksum_sha256 are required", http.StatusBadRequest)
		return
	}

	rel.PublishedAt = time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	coll := mongoClient.Database(dbName).Collection("agent_releases")
	if _, err := coll.InsertOne(ctx, rel); err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	log.Printf("Agent release published: %s %s/%s", rel.Version, rel.OS, rel.Arch)
	w.WriteHeader(http.StatusCreated)
}

func main() {
	// Load .env file if present (silently ignored when not found, e.g. in production).
	// We look next to the source file first (for `go run`), then next to the binary
	// (for compiled deployments), so both workflows work without configuration.
	{
		// Locate the directory containing main.go via os.Executable or __file__ equivalent.
		// For `go run`, the cwd is the source directory; for compiled binaries it's wherever
		// the binary lives. Try both the cwd and the executable's directory.
		candidates := []string{".env"}
		if exe, err := os.Executable(); err == nil {
			candidates = append(candidates, filepath.Join(filepath.Dir(exe), ".env"))
		}
		loaded := false
		for _, p := range candidates {
			if err := godotenv.Load(p); err == nil {
				log.Printf("Loaded env from %s", p)
				loaded = true
				break
			}
		}
		if !loaded {
			log.Println("No .env file found, relying on environment variables")
		}
	}

	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI == "" {
		log.Fatal("MONGO_URI environment variable must be set")
	}

	// Load admin secret for privileged endpoints.
	adminSecret = os.Getenv("ADMIN_SECRET")
	if adminSecret == "" {
		log.Println("WARNING: ADMIN_SECRET is not set — POST /policy and POST /update/release are disabled")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI))
	if err != nil {
		log.Fatalf("Failed to connect to MongoDB: %v", err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		log.Fatalf("Failed to ping MongoDB: %v", err)
	}
	mongoClient = client
	log.Println("Connected to MongoDB")

	// Ensure indexes
	go ensureIndexes()

	// Restore persisted policy so sync intervals survive restarts.
	loadPolicyFromMongo()

	// Build focus cache from existing telemetry data
	go buildFocusCacheFromMongo()

	// Routes
	http.HandleFunc("/devices/register", corsMiddleware(registerHandler))
	http.HandleFunc("/devices/", corsMiddleware(requireRead(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/history") {
			historyHandler(w, r)
		} else if r.Method == http.MethodDelete {
			deviceDeleteHandler(w, r)
		} else {
			http.NotFound(w, r)
		}
	})))
	http.HandleFunc("/devices", corsMiddleware(requireRead(devicesHandler)))
	http.HandleFunc("/ingest", corsMiddleware(ingestHandler))
	// GET /policy is public; POST /policy requires admin.
	http.HandleFunc("/policy", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			requireAdmin(policyHandler)(w, r)
		} else {
			policyHandler(w, r)
		}
	}))
	http.HandleFunc("/focus/", corsMiddleware(requireRead(focusHandler)))

	http.HandleFunc("/update/check",   corsMiddleware(updateCheckHandler))
	// POST /update/release is admin-only — requires X-Admin-Secret header.
	http.HandleFunc("/update/release", corsMiddleware(requireAdmin(releasePublishHandler)))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("DevicePulse API listening on :%s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

func ensureIndexes() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db := mongoClient.Database(dbName)

	// devices: unique on device_id, api_key; sparse indexes on fingerprint fields
	deviceIdx := []mongo.IndexModel{
		{Keys: bson.D{{Key: "device_id", Value: 1}},     Options: options.Index().SetUnique(true).SetSparse(true)},
		{Keys: bson.D{{Key: "api_key", Value: 1}},       Options: options.Index().SetUnique(true).SetSparse(true)},
		{Keys: bson.D{{Key: "hostname", Value: 1}},       Options: options.Index().SetSparse(true)},
		{Keys: bson.D{{Key: "hardware_uuid", Value: 1}},  Options: options.Index().SetSparse(true)},
		{Keys: bson.D{{Key: "mac_address", Value: 1}},    Options: options.Index().SetSparse(true)},
	}
	db.Collection("devices").Indexes().CreateMany(ctx, deviceIdx)

	// telemetry: index on device_id + _id for fast history queries
	db.Collection("telemetry").Indexes().CreateOne(ctx,
		mongo.IndexModel{Keys: bson.D{{Key: "device_id", Value: 1}, {Key: "_id", Value: -1}}})

	// agent_releases: index on os+arch+published_at for fast latest-release lookup
	db.Collection("agent_releases").Indexes().CreateOne(ctx,
		mongo.IndexModel{Keys: bson.D{
			{Key: "os", Value: 1},
			{Key: "arch", Value: 1},
			{Key: "published_at", Value: -1},
		}})
}
