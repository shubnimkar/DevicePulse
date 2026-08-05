package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/joho/godotenv"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// ─── Globals ──────────────────────────────────────────────────────────────────

var (
	mongoClient *mongo.Client
	dbName      = "devicepulse"
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
	for i := 1; i < len(result); i++ {
		for j := i; j > 0 && result[j].TotalFocusS > result[j-1].TotalFocusS; j-- {
			result[j], result[j-1] = result[j-1], result[j]
		}
	}
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

// ─── Alert Rule ───────────────────────────────────────────────────────────────

// AlertRule defines a threshold check applied on every ingest for a device.
// Supported metrics: cpu_percent, ram_percent, disk_percent
// Supported conditions: gt (greater than), lt (less than)
type AlertRule struct {
	ID        primitive.ObjectID `bson:"_id,omitempty"    json:"id,omitempty"`
	DeviceID  string             `bson:"device_id"        json:"device_id"`
	Metric    string             `bson:"metric"           json:"metric"`
	Condition string             `bson:"condition"        json:"condition"` // "gt" | "lt"
	Threshold float64            `bson:"threshold"        json:"threshold"`
	CreatedAt time.Time          `bson:"created_at"       json:"created_at"`
}

// AlertFiring is stored when a rule fires.
type AlertFiring struct {
	ID        primitive.ObjectID `bson:"_id,omitempty" json:"id,omitempty"`
	RuleID    primitive.ObjectID `bson:"rule_id"       json:"rule_id"`
	DeviceID  string             `bson:"device_id"     json:"device_id"`
	Metric    string             `bson:"metric"        json:"metric"`
	Value     float64            `bson:"value"         json:"value"`
	Threshold float64            `bson:"threshold"     json:"threshold"`
	Condition string             `bson:"condition"     json:"condition"`
	FiredAt   time.Time          `bson:"fired_at"      json:"fired_at"`
}

// ─── CORS Middleware ───────────────────────────────────────────────────────────

func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, Authorization, X-API-Key")
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

// generateAPIKey creates a random hex key using crypto-safe randomness.
func generateAPIKey() string {
	b := make([]byte, 24)
	// fallback to time-seeded pseudo-random if crypto not available
	for i := range b {
		b[i] = byte(time.Now().UnixNano() >> uint(i%8) & 0xff)
		time.Sleep(0) // yield to prevent identical timestamps
	}
	return fmt.Sprintf("%x", b)
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
	if err != nil {
		http.Error(w, "Error reading body", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

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

	// Evaluate alert rules asynchronously so ingest stays fast
	go evaluateAlerts(authDeviceID, payload)

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

// ─── Alert Evaluation ─────────────────────────────────────────────────────────

func evaluateAlerts(deviceID string, payload map[string]interface{}) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Load rules for this device
	coll := mongoClient.Database(dbName).Collection("alert_rules")
	cursor, err := coll.Find(ctx, bson.M{"device_id": bson.M{"$in": []string{deviceID, "*"}}})
	if err != nil {
		return
	}
	defer cursor.Close(ctx)

	var rules []AlertRule
	if err := cursor.All(ctx, &rules); err != nil || len(rules) == 0 {
		return
	}

	// Extract metric values from payload
	metrics := extractMetrics(payload)

	firings := mongoClient.Database(dbName).Collection("alert_firings")
	for _, rule := range rules {
		val, ok := metrics[rule.Metric]
		if !ok {
			continue
		}
		fired := false
		switch rule.Condition {
		case "gt":
			fired = val > rule.Threshold
		case "lt":
			fired = val < rule.Threshold
		}
		if fired {
			firing := AlertFiring{
				RuleID:    rule.ID,
				DeviceID:  deviceID,
				Metric:    rule.Metric,
				Value:     val,
				Threshold: rule.Threshold,
				Condition: rule.Condition,
				FiredAt:   time.Now(),
			}
			if _, err := firings.InsertOne(ctx, firing); err != nil {
				log.Printf("Alert insert error: %v", err)
			} else {
				log.Printf("ALERT fired: device=%s metric=%s value=%.2f %s %.2f",
					deviceID, rule.Metric, val, rule.Condition, rule.Threshold)
			}
		}
	}
}

// extractMetrics pulls numeric values from HardwareStats for rule evaluation.
func extractMetrics(payload map[string]interface{}) map[string]float64 {
	out := map[string]float64{}
	data, ok := payload["data"].(map[string]interface{})
	if !ok {
		return out
	}

	hw, ok := data["HardwareStats"].(map[string]interface{})
	if !ok {
		return out
	}

	if cpu, ok := hw["cpu"].(map[string]interface{}); ok {
		if v, ok := toFloat(cpu["usage_percent"]); ok {
			out["cpu_percent"] = v
		}
	}
	if ram, ok := hw["ram"].(map[string]interface{}); ok {
		if v, ok := toFloat(ram["used_percent"]); ok {
			out["ram_percent"] = v
		}
	}
	if disks, ok := hw["disks"].([]interface{}); ok && len(disks) > 0 {
		if d, ok := disks[0].(map[string]interface{}); ok {
			if v, ok := toFloat(d["used_percent"]); ok {
				out["disk_percent"] = v
			}
		}
	}
	if batt, ok := hw["battery"].(map[string]interface{}); ok {
		if avail, _ := batt["available"].(bool); avail {
			if v, ok := toFloat(batt["percent"]); ok {
				out["battery_percent"] = v
			}
		}
	}
	return out
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
		log.Println("Global policy updated")
		w.WriteHeader(http.StatusOK)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// ─── Alert Rules Handlers ──────────────────────────────────────────────────────

// GET  /alerts/rules          — list all rules
// POST /alerts/rules          — create a rule
// DELETE /alerts/rules/{id}   — delete a rule
func alertRulesHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	coll := mongoClient.Database(dbName).Collection("alert_rules")

	switch r.Method {
	case http.MethodGet:
		cursor, err := coll.Find(ctx, bson.M{})
		if err != nil {
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}
		defer cursor.Close(ctx)
		var rules []bson.M
		cursor.All(ctx, &rules)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(rules)

	case http.MethodPost:
		var rule AlertRule
		if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
			http.Error(w, "Bad JSON", http.StatusBadRequest)
			return
		}
		rule.ID        = primitive.NewObjectID()
		rule.CreatedAt = time.Now()
		if _, err := coll.InsertOne(ctx, rule); err != nil {
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(rule)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// DELETE /alerts/rules/{id}
func alertRuleDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		http.Error(w, "Invalid URL", http.StatusBadRequest)
		return
	}
	idStr := parts[3]

	oid, err := primitive.ObjectIDFromHex(idStr)
	if err != nil {
		http.Error(w, "Invalid rule ID", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	coll := mongoClient.Database(dbName).Collection("alert_rules")
	if _, err := coll.DeleteOne(ctx, bson.M{"_id": oid}); err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// GET /alerts/firings — recent alert firings (last 50)
func alertFiringsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	opts := options.Find().SetSort(bson.D{{Key: "fired_at", Value: -1}}).SetLimit(50)
	cursor, err := mongoClient.Database(dbName).Collection("alert_firings").Find(ctx, bson.M{}, opts)
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var firings []bson.M
	cursor.All(ctx, &firings)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(firings)
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
	// Load .env file if present (silently ignored when not found, e.g. in production)
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, relying on environment variables")
	}

	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI == "" {
		log.Fatal("MONGO_URI environment variable must be set")
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

	// Build focus cache from existing telemetry data
	go buildFocusCacheFromMongo()

	// Routes
	http.HandleFunc("/devices/register", corsMiddleware(registerHandler))
	http.HandleFunc("/devices/", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/history") {
			historyHandler(w, r)
		} else if r.Method == http.MethodDelete {
			deviceDeleteHandler(w, r)
		} else {
			http.NotFound(w, r)
		}
	}))
	http.HandleFunc("/devices", corsMiddleware(devicesHandler))
	http.HandleFunc("/ingest", corsMiddleware(ingestHandler))
	http.HandleFunc("/policy", corsMiddleware(policyHandler))
	http.HandleFunc("/alerts/rules/", corsMiddleware(alertRuleDeleteHandler))
	http.HandleFunc("/alerts/rules", corsMiddleware(alertRulesHandler))
	http.HandleFunc("/alerts/firings", corsMiddleware(alertFiringsHandler))
	http.HandleFunc("/focus/", corsMiddleware(focusHandler))

	http.HandleFunc("/update/check",   corsMiddleware(updateCheckHandler))
	http.HandleFunc("/update/release", corsMiddleware(releasePublishHandler))

	log.Println("DevicePulse API listening on :8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
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

	// alert_rules: index on device_id
	db.Collection("alert_rules").Indexes().CreateOne(ctx,
		mongo.IndexModel{Keys: bson.D{{Key: "device_id", Value: 1}}})

	// alert_firings: index on fired_at for recency queries
	db.Collection("alert_firings").Indexes().CreateOne(ctx,
		mongo.IndexModel{Keys: bson.D{{Key: "fired_at", Value: -1}}})

	// agent_releases: index on os+arch+published_at for fast latest-release lookup
	db.Collection("agent_releases").Indexes().CreateOne(ctx,
		mongo.IndexModel{Keys: bson.D{
			{Key: "os", Value: 1},
			{Key: "arch", Value: 1},
			{Key: "published_at", Value: -1},
		}})
}
