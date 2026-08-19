package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/joho/godotenv"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"golang.org/x/crypto/bcrypt"
)

// ─── Globals ──────────────────────────────────────────────────────────────────

var (
	mongoClient *mongo.Client
	dbName      = "devicepulse"
	// adminSecret is loaded from ADMIN_SECRET env var and required for
	// privileged endpoints: POST /policy and POST /update/release.
	adminSecret   string
	sessionSecret string
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
		dur, _ := toFloat(m["total_focus_seconds"])
		sessions, _ := toFloat(m["session_count"])

		e, ok := apps[name]
		if !ok {
			e = &AppFocusEntry{AppName: name}
			apps[name] = e
		}
		e.TotalFocusS += dur
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

		// No limit — scan all telemetry to build accurate cumulative totals.
		opts := options.Find().
			SetSort(bson.D{{Key: "_id", Value: -1}}).
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
	config: defaultPolicy(),
}

func defaultPolicy() map[string]interface{} {
	return map[string]interface{}{
		"sync_interval_seconds":          10,
		"telemetry_retention_days":       30,
		"delta_upload_enabled":           true,
		"cache_unchanged_collector_data": true,
		"browser_history_mode":           "disabled",
		"browser_history_limit":          10,
		"collect_system_info":            true,
		"collect_hardware_stats":         true,
		"collect_processes":              true,
		"collect_browser_history":        false,
		"collect_active_window":          true,
		"collect_services":               true,
		"collect_network_ports":          true,
		"collect_installed_apps":         true,
		"collect_os_updates":             true,
		"collect_usb_devices":            true,
	}
}

func normalizePolicy(input map[string]interface{}) map[string]interface{} {
	policy := defaultPolicy()
	for k, v := range input {
		policy[k] = v
	}

	clampNumber(policy, "sync_interval_seconds", 10, 3600)
	clampNumber(policy, "telemetry_retention_days", 1, 3650)
	clampNumber(policy, "browser_history_limit", 0, 1000)

	mode, _ := policy["browser_history_mode"].(string)
	switch mode {
	case "disabled", "domain_only", "full_url":
	default:
		policy["browser_history_mode"] = "disabled"
	}

	boolKeys := []string{
		"delta_upload_enabled",
		"cache_unchanged_collector_data",
		"collect_system_info",
		"collect_hardware_stats",
		"collect_processes",
		"collect_browser_history",
		"collect_active_window",
		"collect_services",
		"collect_network_ports",
		"collect_installed_apps",
		"collect_os_updates",
		"collect_usb_devices",
	}
	for _, key := range boolKeys {
		if _, ok := policy[key].(bool); !ok {
			policy[key] = defaultPolicy()[key]
		}
	}

	if policy["browser_history_mode"] == "disabled" {
		policy["collect_browser_history"] = false
	}

	return policy
}

func clampNumber(policy map[string]interface{}, key string, min, max float64) {
	v, ok := toFloat(policy[key])
	if !ok {
		policy[key] = defaultPolicy()[key]
		return
	}
	if v < min {
		v = min
	}
	if v > max {
		v = max
	}
	policy[key] = v
}

func currentRetentionDays() int {
	globalPolicy.mu.RLock()
	defer globalPolicy.mu.RUnlock()

	days, ok := toFloat(globalPolicy.config["telemetry_retention_days"])
	if !ok || days < 1 {
		return 30
	}
	return int(days)
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
	globalPolicy.config = normalizePolicy(doc)
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
		origin := r.Header.Get("Origin")
		allowedOrigin := os.Getenv("DASHBOARD_ORIGIN")
		if allowedOrigin == "" {
			allowedOrigin = "http://localhost:3000"
		}
		if origin != "" && (origin == allowedOrigin || strings.HasPrefix(origin, "http://localhost:")) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		} else {
			w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
		}
		w.Header().Set("Access-Control-Allow-Credentials", "true")
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

type contextKey string

const dashboardUserContextKey contextKey = "dashboard_user"

type UserRole string

const (
	RoleAdmin   UserRole = "admin"
	RoleManager UserRole = "manager"
	RoleViewer  UserRole = "viewer"
)

type DashboardUser struct {
	ID           primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Email        string             `bson:"email" json:"email"`
	Name         string             `bson:"name" json:"name"`
	PasswordHash string             `bson:"password_hash,omitempty" json:"-"`
	Role         UserRole           `bson:"role" json:"role"`
	Status       string             `bson:"status" json:"status"`
	CreatedAt    time.Time          `bson:"created_at" json:"created_at"`
	UpdatedAt    time.Time          `bson:"updated_at" json:"updated_at"`
}

type SessionClaims struct {
	UserID string   `json:"user_id"`
	Email  string   `json:"email"`
	Name   string   `json:"name"`
	Role   UserRole `json:"role"`
	Exp    int64    `json:"exp"`
}

type authRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
	Role     string `json:"role"`
}

type passwordResetRequest struct {
	Password string `json:"password"`
}

type roleUpdateRequest struct {
	Role string `json:"role"`
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func validRole(role UserRole) bool {
	switch role {
	case RoleAdmin, RoleManager, RoleViewer:
		return true
	default:
		return false
	}
}

func roleRank(role UserRole) int {
	switch role {
	case RoleAdmin:
		return 3
	case RoleManager:
		return 2
	case RoleViewer:
		return 1
	default:
		return 0
	}
}

func canAccessRole(actual UserRole, required UserRole) bool {
	return roleRank(actual) >= roleRank(required)
}

func userResponse(user DashboardUser) map[string]interface{} {
	return map[string]interface{}{
		"id":         user.ID.Hex(),
		"email":      user.Email,
		"name":       user.Name,
		"role":       user.Role,
		"status":     user.Status,
		"created_at": user.CreatedAt,
		"updated_at": user.UpdatedAt,
	}
}

func signSessionPayload(payload []byte) string {
	mac := hmac.New(sha256.New, []byte(sessionSecret))
	mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func createSessionToken(user DashboardUser) (string, error) {
	claims := SessionClaims{
		UserID: user.ID.Hex(),
		Email:  user.Email,
		Name:   user.Name,
		Role:   user.Role,
		Exp:    time.Now().Add(24 * time.Hour).Unix(),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	signature := signSessionPayload([]byte(encodedPayload))
	return encodedPayload + "." + signature, nil
}

func parseSessionToken(token string) (SessionClaims, bool) {
	var claims SessionClaims
	parts := strings.Split(token, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return claims, false
	}
	expected := signSessionPayload([]byte(parts[0]))
	if !hmac.Equal([]byte(expected), []byte(parts[1])) {
		return claims, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return claims, false
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return claims, false
	}
	if claims.Exp < time.Now().Unix() || claims.UserID == "" || !validRole(claims.Role) {
		return claims, false
	}
	return claims, true
}

func setSessionCookie(w http.ResponseWriter, user DashboardUser) error {
	token, err := createSessionToken(user)
	if err != nil {
		return err
	}
	secureCookie := strings.EqualFold(os.Getenv("COOKIE_SECURE"), "true")
	http.SetCookie(w, &http.Cookie{
		Name:     "devicepulse_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secureCookie,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400,
	})
	return nil
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     "devicepulse_session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func currentUser(r *http.Request) (SessionClaims, bool) {
	user, ok := r.Context().Value(dashboardUserContextKey).(SessionClaims)
	return user, ok
}

func requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("devicepulse_session")
		if err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		claims, ok := parseSessionToken(cookie.Value)
		if !ok {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		userID, err := primitive.ObjectIDFromHex(claims.UserID)
		if err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		var user DashboardUser
		err = mongoClient.Database(dbName).Collection("users").FindOne(ctx, bson.M{
			"_id":    userID,
			"status": "active",
		}).Decode(&user)
		if err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		claims.Email = user.Email
		claims.Name = user.Name
		claims.Role = user.Role

		next(w, r.WithContext(context.WithValue(r.Context(), dashboardUserContextKey, claims)))
	}
}

func requireRole(required UserRole, next http.HandlerFunc) http.HandlerFunc {
	return requireAuth(func(w http.ResponseWriter, r *http.Request) {
		user, ok := currentUser(r)
		if !ok || !canAccessRole(user.Role, required) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	})
}

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

// requireAgent validates that the request belongs to a registered agent.
func requireAgent(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if _, ok := resolveAPIKey(ctx, r.Header.Get("X-API-Key")); !ok {
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

	hostname := body["hostname"]
	hardwareUUID := body["hardware_uuid"]
	macAddress := body["mac_address"]

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
		apiKey, _ := existing["api_key"].(string)
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

// POST /auth/register
// Bootstraps the first dashboard admin. Once any dashboard user exists, only an
// existing admin can create users through POST /users.
func authRegisterHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body authRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Bad JSON", http.StatusBadRequest)
		return
	}

	email := normalizeEmail(body.Email)
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = email
	}
	if email == "" || len(body.Password) < 8 {
		http.Error(w, "Email and password with at least 8 characters are required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	coll := mongoClient.Database(dbName).Collection("users")
	count, err := coll.CountDocuments(ctx, bson.M{})
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	if count > 0 {
		http.Error(w, "Registration is closed. Ask an admin to create your account.", http.StatusForbidden)
		return
	}
	if _, err := mongoClient.Database(dbName).Collection("config").InsertOne(ctx, bson.M{
		"_id":        "dashboard_bootstrap",
		"created_at": time.Now(),
	}); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			http.Error(w, "Registration is closed. Ask an admin to create your account.", http.StatusForbidden)
			return
		}
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		mongoClient.Database(dbName).Collection("config").DeleteOne(ctx, bson.M{"_id": "dashboard_bootstrap"})
		http.Error(w, "Password hashing error", http.StatusInternalServerError)
		return
	}

	now := time.Now()
	user := DashboardUser{
		ID:           primitive.NewObjectID(),
		Email:        email,
		Name:         name,
		PasswordHash: string(passwordHash),
		Role:         RoleAdmin,
		Status:       "active",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if _, err := coll.InsertOne(ctx, user); err != nil {
		mongoClient.Database(dbName).Collection("config").DeleteOne(ctx, bson.M{"_id": "dashboard_bootstrap"})
		if mongo.IsDuplicateKeyError(err) {
			http.Error(w, "User already exists", http.StatusConflict)
			return
		}
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	if err := setSessionCookie(w, user); err != nil {
		http.Error(w, "Session error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"user": userResponse(user)})
}

// POST /auth/login
func authLoginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body authRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Bad JSON", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var user DashboardUser
	err := mongoClient.Database(dbName).Collection("users").FindOne(ctx, bson.M{
		"email":  normalizeEmail(body.Email),
		"status": "active",
	}).Decode(&user)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(body.Password)) != nil {
		http.Error(w, "Invalid email or password", http.StatusUnauthorized)
		return
	}
	if err := setSessionCookie(w, user); err != nil {
		http.Error(w, "Session error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"user": userResponse(user)})
}

// POST /auth/logout
func authLogoutHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	clearSessionCookie(w)
	w.WriteHeader(http.StatusOK)
}

// GET /auth/me
func authMeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := currentUser(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"user": map[string]interface{}{
			"id":    user.UserID,
			"email": user.Email,
			"name":  user.Name,
			"role":  user.Role,
		},
	})
}

// GET /auth/bootstrap
// Returns whether the dashboard still needs its first admin account.
func authBootstrapHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	count, err := mongoClient.Database(dbName).Collection("users").CountDocuments(ctx, bson.M{})
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"bootstrap_required": count == 0})
}

// GET/POST /users
// Admin-only dashboard user management. Registration after bootstrap is closed.
func usersHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}})
		cursor, err := mongoClient.Database(dbName).Collection("users").Find(ctx, bson.M{}, opts)
		if err != nil {
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}
		defer cursor.Close(ctx)

		var users []DashboardUser
		if err := cursor.All(ctx, &users); err != nil {
			http.Error(w, "Parsing error", http.StatusInternalServerError)
			return
		}
		result := make([]map[string]interface{}, 0, len(users))
		for _, user := range users {
			result = append(result, userResponse(user))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)

	case http.MethodPost:
		var body authRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "Bad JSON", http.StatusBadRequest)
			return
		}
		email := normalizeEmail(body.Email)
		name := strings.TrimSpace(body.Name)
		role := UserRole(strings.TrimSpace(body.Role))
		if role == "" {
			role = RoleViewer
		}
		if name == "" {
			name = email
		}
		if email == "" || len(body.Password) < 8 || !validRole(role) {
			http.Error(w, "Email, valid role and password with at least 8 characters are required", http.StatusBadRequest)
			return
		}

		passwordHash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
		if err != nil {
			http.Error(w, "Password hashing error", http.StatusInternalServerError)
			return
		}

		now := time.Now()
		user := DashboardUser{
			ID:           primitive.NewObjectID(),
			Email:        email,
			Name:         name,
			PasswordHash: string(passwordHash),
			Role:         role,
			Status:       "active",
			CreatedAt:    now,
			UpdatedAt:    now,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if _, err := mongoClient.Database(dbName).Collection("users").InsertOne(ctx, user); err != nil {
			if mongo.IsDuplicateKeyError(err) {
				http.Error(w, "User already exists", http.StatusConflict)
				return
			}
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{"user": userResponse(user)})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// POST /users/{id}/password
// Admin-only password reset for any dashboard user, including the current admin.
func userPasswordHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) != 3 || parts[0] != "users" || parts[1] == "" || parts[2] != "password" {
		http.Error(w, "Invalid URL", http.StatusBadRequest)
		return
	}
	userID, err := primitive.ObjectIDFromHex(parts[1])
	if err != nil {
		http.Error(w, "Invalid user id", http.StatusBadRequest)
		return
	}

	var body passwordResetRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Bad JSON", http.StatusBadRequest)
		return
	}
	if len(body.Password) < 8 {
		http.Error(w, "Password with at least 8 characters is required", http.StatusBadRequest)
		return
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "Password hashing error", http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	update := bson.M{"$set": bson.M{
		"password_hash": string(passwordHash),
		"updated_at":    time.Now(),
		"status":        "active",
	}}
	res, err := mongoClient.Database(dbName).Collection("users").UpdateOne(ctx, bson.M{"_id": userID}, update)
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	if res.MatchedCount == 0 {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// POST /users/{id}/role
// Admin-only role change for dashboard users.
func userRoleHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) != 3 || parts[0] != "users" || parts[1] == "" || parts[2] != "role" {
		http.Error(w, "Invalid URL", http.StatusBadRequest)
		return
	}
	userID, err := primitive.ObjectIDFromHex(parts[1])
	if err != nil {
		http.Error(w, "Invalid user id", http.StatusBadRequest)
		return
	}

	var body roleUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Bad JSON", http.StatusBadRequest)
		return
	}
	role := UserRole(strings.TrimSpace(body.Role))
	if !validRole(role) {
		http.Error(w, "Valid role is required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	update := bson.M{"$set": bson.M{
		"role":       role,
		"updated_at": time.Now(),
	}}
	res, err := mongoClient.Database(dbName).Collection("users").UpdateOne(ctx, bson.M{"_id": userID}, update)
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	if res.MatchedCount == 0 {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func userDetailHandler(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/password") {
		userPasswordHandler(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/role") {
		userRoleHandler(w, r)
		return
	}
	http.NotFound(w, r)
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
	r.Body = http.MaxBytesReader(w, r.Body, 5*1024*1024)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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

	// Ensure device_id matches the registered key and attach retention metadata.
	now := time.Now()
	retentionDays := currentRetentionDays()
	payload["device_id"] = authDeviceID
	payload["ingested_at"] = now
	payload["telemetry_expires_at"] = now.AddDate(0, 0, retentionDays)

	collTelemetry := mongoClient.Database(dbName).Collection("telemetry")
	collDevices := mongoClient.Database(dbName).Collection("devices")

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
		"last_seen": now,
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
	case int:
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

	db := mongoClient.Database(dbName)
	res, err := db.Collection("devices").DeleteOne(ctx, bson.M{"device_id": deviceID})
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	if res.DeletedCount == 0 {
		http.Error(w, "Device not found", http.StatusNotFound)
		return
	}

	if _, err := db.Collection("telemetry").DeleteMany(ctx, bson.M{"device_id": deviceID}); err != nil {
		log.Printf("Telemetry cleanup failed for deleted device %s: %v", deviceID, err)
	}

	globalFocusCache.mu.Lock()
	delete(globalFocusCache.data, deviceID)
	globalFocusCache.mu.Unlock()

	log.Printf("Device deleted: %s", deviceID)
	w.WriteHeader(http.StatusOK)
}

// ─── Devices Handler ───────────────────────────────────────────────────────────

func devicesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	opts := options.Find().SetProjection(bson.M{
		"api_key": 0,
		"data.BrowserHistory.top_recent_urls": bson.M{
			"$slice": 50,
		},
	})
	cursor, err := mongoClient.Database(dbName).Collection("devices").Find(ctx, bson.M{}, opts)
	if err != nil {
		log.Printf("Devices query error: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	devices := []bson.M{}
	if err = cursor.All(ctx, &devices); err != nil {
		http.Error(w, "Parsing error", http.StatusInternalServerError)
		return
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

	// Optional ?limit=N query param. Defaults to 0 (no limit) for full history.
	// Dashboard live view uses a small limit; report generation omits it.
	var findOpts *options.FindOptions
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if l, err := strconv.ParseInt(lStr, 10, 64); err == nil && l > 0 {
			findOpts = options.Find().SetSort(bson.D{{Key: "_id", Value: -1}}).SetLimit(l)
		}
	}
	if findOpts == nil {
		findOpts = options.Find().SetSort(bson.D{{Key: "_id", Value: -1}})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cursor, err := mongoClient.Database(dbName).Collection("telemetry").Find(ctx, bson.M{"device_id": deviceID}, findOpts)
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
		merged := make(map[string]interface{}, len(globalPolicy.config)+len(newConfig))
		for k, v := range globalPolicy.config {
			merged[k] = v
		}
		for k, v := range newConfig {
			merged[k] = v
		}
		globalPolicy.config = normalizePolicy(merged)
		savedConfig := make(map[string]interface{}, len(globalPolicy.config))
		for k, v := range globalPolicy.config {
			savedConfig[k] = v
		}
		globalPolicy.mu.Unlock()
		// Persist so the policy survives API restarts.
		go savePolicyToMongo(savedConfig)
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
		"device_id":     deviceID,
		"app_summaries": entries,
	})
}

// ─── Update Check Handler ──────────────────────────────────────────────────────

// AgentRelease describes a published agent binary in the releases collection.
type AgentRelease struct {
	Version     string    `bson:"version"          json:"version"`
	OS          string    `bson:"os"               json:"os"`   // "darwin" | "linux" | "windows"
	Arch        string    `bson:"arch"             json:"arch"` // "amd64" | "arm64"
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
	agentOS := r.URL.Query().Get("os")
	agentArch := r.URL.Query().Get("arch")

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

func healthHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func loadEnvFile() {
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

func resetDashboardPasswordCommand(mongoURI string, args []string) {
	fs := flag.NewFlagSet("reset-password", flag.ExitOnError)
	emailFlag := fs.String("email", "", "dashboard user email")
	passwordFlag := fs.String("password", "", "new password")
	if err := fs.Parse(args); err != nil {
		log.Fatal(err)
	}

	email := normalizeEmail(*emailFlag)
	password := *passwordFlag
	if email == "" || len(password) < 8 {
		log.Fatal("Usage: go run . reset-password --email user@example.com --password new-password-min-8-chars")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI))
	if err != nil {
		log.Fatalf("Failed to connect to MongoDB: %v", err)
	}
	defer client.Disconnect(ctx)
	if err := client.Ping(ctx, nil); err != nil {
		log.Fatalf("Failed to ping MongoDB: %v", err)
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		log.Fatalf("Password hashing error: %v", err)
	}

	update := bson.M{"$set": bson.M{
		"password_hash": string(passwordHash),
		"updated_at":    time.Now(),
		"status":        "active",
	}}
	res, err := client.Database(dbName).Collection("users").UpdateOne(ctx, bson.M{"email": email}, update)
	if err != nil {
		log.Fatalf("Password reset failed: %v", err)
	}
	if res.MatchedCount == 0 {
		log.Fatalf("No dashboard user found with email %s", email)
	}

	log.Printf("Password reset for dashboard user %s", email)
}

func main() {
	// Load .env file if present (silently ignored when not found, e.g. in production).
	// We look next to the source file first (for `go run`), then next to the binary
	// (for compiled deployments), so both workflows work without configuration.
	loadEnvFile()

	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI == "" {
		log.Fatal("MONGO_URI environment variable must be set")
	}
	if len(os.Args) > 1 && os.Args[1] == "reset-password" {
		resetDashboardPasswordCommand(mongoURI, os.Args[2:])
		return
	}

	// Load admin secret for privileged endpoints.
	adminSecret = os.Getenv("ADMIN_SECRET")
	if adminSecret == "" {
		log.Println("WARNING: ADMIN_SECRET is not set — POST /policy and POST /update/release are disabled")
	}
	sessionSecret = os.Getenv("SESSION_SECRET")
	if sessionSecret == "" {
		sessionSecret = adminSecret
	}
	if sessionSecret == "" {
		log.Fatal("SESSION_SECRET environment variable must be set for dashboard authentication")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
	http.HandleFunc("/health", corsMiddleware(healthHandler))
	http.HandleFunc("/devices/register", corsMiddleware(registerHandler))
	http.HandleFunc("/auth/bootstrap", corsMiddleware(authBootstrapHandler))
	http.HandleFunc("/auth/register", corsMiddleware(authRegisterHandler))
	http.HandleFunc("/auth/login", corsMiddleware(authLoginHandler))
	http.HandleFunc("/auth/logout", corsMiddleware(authLogoutHandler))
	http.HandleFunc("/auth/me", corsMiddleware(requireAuth(authMeHandler)))
	http.HandleFunc("/users/", corsMiddleware(requireRole(RoleAdmin, userDetailHandler)))
	http.HandleFunc("/users", corsMiddleware(requireRole(RoleAdmin, usersHandler)))
	http.HandleFunc("/devices/", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/history") {
			requireRole(RoleViewer, historyHandler)(w, r)
		} else if r.Method == http.MethodDelete {
			requireRole(RoleAdmin, deviceDeleteHandler)(w, r)
		} else {
			http.NotFound(w, r)
		}
	}))
	http.HandleFunc("/devices", corsMiddleware(requireRole(RoleViewer, devicesHandler)))
	http.HandleFunc("/ingest", corsMiddleware(ingestHandler))
	// Dashboard policy access is role-protected. Agents receive policy through
	// authenticated device flows, not these browser endpoints.
	http.HandleFunc("/policy", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			requireRole(RoleManager, policyHandler)(w, r)
		} else {
			requireRole(RoleViewer, policyHandler)(w, r)
		}
	}))
	http.HandleFunc("/focus/", corsMiddleware(requireRole(RoleViewer, focusHandler)))

	http.HandleFunc("/update/check", corsMiddleware(requireAgent(updateCheckHandler)))
	http.HandleFunc("/update/release", corsMiddleware(requireRole(RoleAdmin, releasePublishHandler)))

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
		{Keys: bson.D{{Key: "device_id", Value: 1}}, Options: options.Index().SetUnique(true).SetSparse(true)},
		{Keys: bson.D{{Key: "api_key", Value: 1}}, Options: options.Index().SetUnique(true).SetSparse(true)},
		{Keys: bson.D{{Key: "hostname", Value: 1}}, Options: options.Index().SetSparse(true)},
		{Keys: bson.D{{Key: "hardware_uuid", Value: 1}}, Options: options.Index().SetSparse(true)},
		{Keys: bson.D{{Key: "mac_address", Value: 1}}, Options: options.Index().SetSparse(true)},
	}
	db.Collection("devices").Indexes().CreateMany(ctx, deviceIdx)

	// telemetry: index on device_id + _id for fast history queries
	db.Collection("telemetry").Indexes().CreateOne(ctx,
		mongo.IndexModel{Keys: bson.D{{Key: "device_id", Value: 1}, {Key: "_id", Value: -1}}})
	db.Collection("telemetry").Indexes().CreateOne(ctx,
		mongo.IndexModel{
			Keys:    bson.D{{Key: "telemetry_expires_at", Value: 1}},
			Options: options.Index().SetExpireAfterSeconds(0).SetSparse(true),
		})

	// agent_releases: index on os+arch+published_at for fast latest-release lookup
	db.Collection("agent_releases").Indexes().CreateOne(ctx,
		mongo.IndexModel{Keys: bson.D{
			{Key: "os", Value: 1},
			{Key: "arch", Value: 1},
			{Key: "published_at", Value: -1},
		}})

	// users: unique email for dashboard login and quick role/status filtering
	userIdx := []mongo.IndexModel{
		{Keys: bson.D{{Key: "email", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "role", Value: 1}, {Key: "status", Value: 1}}},
	}
	db.Collection("users").Indexes().CreateMany(ctx, userIdx)
}
