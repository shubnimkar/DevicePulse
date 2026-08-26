package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
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
	adminSecret    string
	sessionSecret  string
	browserArchive *BrowserHistoryArchive
	telemetryStore *TelemetryArchive
	activityStore  *DailyActivityArchive
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
		name = strings.TrimSpace(name)
		if !isVisibleAppUsageName(name) {
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
			SetProjection(bson.M{
				"data.ActiveWindowTracker.app_summaries":        1,
				"data.ActiveWindowTracker.cumulative_summaries": 1,
				"_id": 0,
			})

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

// extractFocusSummaries pulls the app_summaries (or cumulative_summaries) array
// out of a raw telemetry doc. Prefers cumulative_summaries when present.
func extractFocusSummaries(doc bson.M) []interface{} {
	data, ok := doc["data"].(bson.M)
	if !ok {
		return nil
	}
	awt, ok := data["ActiveWindowTracker"].(bson.M)
	if !ok {
		return nil
	}
	// Prefer cumulative_summaries; fall back to per-cycle app_summaries.
	summaries, ok := awt["cumulative_summaries"].(bson.A)
	if !ok || len(summaries) == 0 {
		summaries, _ = awt["app_summaries"].(bson.A)
	}
	result := make([]interface{}, 0, len(summaries))
	for _, s := range summaries {
		if isVisibleAppUsageSummary(s) {
			result = append(result, s)
		}
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
		"browser_history_mode":           "full_url",
		"browser_history_limit":          200,
		"collect_system_info":            true,
		"collect_hardware_stats":         true,
		"collect_processes":              true,
		"collect_browser_history":        true,
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
	clampNumber(policy, "browser_history_limit", 0, 5000)

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

// ─── Telemetry S3 Archive ──────────────────────────────────────────────────────

type TelemetryArchive struct {
	client *s3.Client
	bucket string
	prefix string
}

type TelemetryArchiveResult struct {
	Bucket string `json:"bucket" bson:"bucket"`
	Key    string `json:"key" bson:"key"`
}

func initTelemetryArchive(ctx context.Context) *TelemetryArchive {
	bucket := strings.TrimSpace(os.Getenv("TELEMETRY_S3_BUCKET"))
	if bucket == "" {
		bucket = strings.TrimSpace(os.Getenv("S3_BUCKET"))
	}
	if bucket == "" {
		log.Println("Telemetry S3 archive disabled (set TELEMETRY_S3_BUCKET to enable)")
		return nil
	}

	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Printf("Telemetry S3 archive disabled: AWS config error: %v", err)
		return nil
	}

	prefix := strings.Trim(strings.TrimSpace(os.Getenv("TELEMETRY_S3_PREFIX")), "/")
	if prefix == "" {
		prefix = "telemetry"
	}
	endpoint := strings.TrimSpace(os.Getenv("TELEMETRY_S3_ENDPOINT"))
	if endpoint == "" {
		endpoint = strings.TrimSpace(os.Getenv("S3_ENDPOINT"))
	}
	usePathStyle := strings.EqualFold(os.Getenv("TELEMETRY_S3_PATH_STYLE"), "true") ||
		strings.EqualFold(os.Getenv("S3_PATH_STYLE"), "true") ||
		endpoint != ""
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
		o.UsePathStyle = usePathStyle
	})

	log.Printf("Telemetry S3 archive enabled: bucket=%s prefix=%s", bucket, prefix)
	return &TelemetryArchive{client: client, bucket: bucket, prefix: prefix}
}

func (a *TelemetryArchive) Archive(ctx context.Context, deviceID string, payload map[string]interface{}) (*TelemetryArchiveResult, error) {
	if a == nil {
		return nil, nil
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	eventTime := payloadTime(payload["timestamp"])
	sum := sha256.Sum256(body)
	key := fmt.Sprintf("%s/device_id=%s/date=%s/%s-%x.json",
		a.prefix,
		safeS3PathSegment(deviceID),
		eventTime.UTC().Format("2006-01-02"),
		eventTime.UTC().Format("20060102T150405.000000000Z"),
		sum[:8],
	)

	if _, err = a.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(a.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(body),
		ContentType: aws.String("application/json"),
	}); err != nil {
		return nil, err
	}

	return &TelemetryArchiveResult{Bucket: a.bucket, Key: key}, nil
}

func (a *TelemetryArchive) Read(ctx context.Context, result map[string]interface{}) (bson.M, error) {
	if a == nil {
		return nil, fmt.Errorf("telemetry S3 archive is not configured")
	}
	key, _ := result["key"].(string)
	if key == "" {
		return nil, fmt.Errorf("telemetry archive key is missing")
	}
	bucket, _ := result["bucket"].(string)
	if bucket == "" {
		bucket = a.bucket
	}

	raw, err := a.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, err
	}
	defer raw.Body.Close()

	var payload bson.M
	if err := json.NewDecoder(raw.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func (a *TelemetryArchive) DeleteDevice(ctx context.Context, deviceID string) (int, error) {
	if a == nil {
		return 0, nil
	}

	deleted := 0
	prefix := fmt.Sprintf("%s/device_id=%s/", a.prefix, safeS3PathSegment(deviceID))
	paginator := s3.NewListObjectsV2Paginator(a.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(a.bucket),
		Prefix: aws.String(prefix),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return deleted, err
		}
		for _, obj := range page.Contents {
			if obj.Key == nil {
				continue
			}
			if _, err := a.client.DeleteObject(ctx, &s3.DeleteObjectInput{
				Bucket: aws.String(a.bucket),
				Key:    obj.Key,
			}); err != nil {
				return deleted, err
			}
			deleted++
		}
	}
	return deleted, nil
}

// ─── Daily App Usage Archive ─────────────────────────────────────────────────

type DailyActivityArchive struct {
	client *s3.Client
	bucket string
	prefix string
}

type DailyActivityArchiveResult struct {
	Bucket     string        `json:"bucket" bson:"bucket"`
	Key        string        `json:"key" bson:"key"`
	Date       string        `json:"date" bson:"date"`
	Username   string        `json:"username" bson:"username"`
	TotalS     float64       `json:"total_seconds" bson:"total_seconds"`
	TopApps    []interface{} `json:"top_apps" bson:"top_apps"`
	SessionCnt int           `json:"session_count" bson:"session_count"`
}

func initDailyActivityArchive(ctx context.Context) *DailyActivityArchive {
	bucket := strings.TrimSpace(os.Getenv("ACTIVITY_S3_BUCKET"))
	if bucket == "" {
		bucket = strings.TrimSpace(os.Getenv("TELEMETRY_S3_BUCKET"))
	}
	if bucket == "" {
		bucket = strings.TrimSpace(os.Getenv("BROWSER_HISTORY_S3_BUCKET"))
	}
	if bucket == "" {
		bucket = strings.TrimSpace(os.Getenv("S3_BUCKET"))
	}
	if bucket == "" {
		log.Println("Daily app usage S3 archive disabled (set ACTIVITY_S3_BUCKET or TELEMETRY_S3_BUCKET to enable)")
		return nil
	}

	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Printf("Daily app usage S3 archive disabled: AWS config error: %v", err)
		return nil
	}

	prefix := strings.Trim(strings.TrimSpace(os.Getenv("ACTIVITY_S3_PREFIX")), "/")
	if prefix == "" {
		prefix = "app-usage"
	}
	endpoint := strings.TrimSpace(os.Getenv("ACTIVITY_S3_ENDPOINT"))
	if endpoint == "" {
		endpoint = strings.TrimSpace(os.Getenv("TELEMETRY_S3_ENDPOINT"))
	}
	if endpoint == "" {
		endpoint = strings.TrimSpace(os.Getenv("S3_ENDPOINT"))
	}
	usePathStyle := strings.EqualFold(os.Getenv("ACTIVITY_S3_PATH_STYLE"), "true") ||
		strings.EqualFold(os.Getenv("TELEMETRY_S3_PATH_STYLE"), "true") ||
		strings.EqualFold(os.Getenv("S3_PATH_STYLE"), "true") ||
		endpoint != ""
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
		o.UsePathStyle = usePathStyle
	})

	log.Printf("Daily app usage S3 archive enabled: bucket=%s prefix=%s", bucket, prefix)
	return &DailyActivityArchive{client: client, bucket: bucket, prefix: prefix}
}

func (a *DailyActivityArchive) Archive(ctx context.Context, deviceID string, payload map[string]interface{}) (*DailyActivityArchiveResult, error) {
	if a == nil {
		return nil, nil
	}
	username := activityUsername(payload)
	sessions := extractFocusSessions(payload)
	if len(sessions) == 0 {
		return nil, nil
	}

	eventTime := payloadTime(payload["timestamp"])
	date := eventTime.UTC().Format("2006-01-02")
	key := dailyActivityS3Key(a.prefix, deviceID, username, date)

	doc := map[string]interface{}{
		"date":       date,
		"device_id":  deviceID,
		"username":   username,
		"sessions":   []interface{}{},
		"apps":       []interface{}{},
		"updated_at": time.Now().UTC(),
	}
	existing, err := a.readObject(ctx, key)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		doc = existing
	}
	doc["date"] = date
	doc["device_id"] = deviceID
	doc["username"] = username
	doc["updated_at"] = time.Now().UTC()

	allSessions := appendInterfaceSlice(doc["sessions"], sessions)
	sort.Slice(allSessions, func(i, j int) bool {
		return activitySessionStart(allSessions[i]).Before(activitySessionStart(allSessions[j]))
	})
	doc["sessions"] = allSessions
	apps, totalS := summarizeActivitySessions(allSessions)
	doc["apps"] = apps
	doc["total_seconds"] = totalS
	doc["session_count"] = len(allSessions)

	body, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	if _, err = a.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(a.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(body),
		ContentType: aws.String("application/json"),
	}); err != nil {
		return nil, err
	}

	topApps := apps
	if len(topApps) > 10 {
		topApps = topApps[:10]
	}
	return &DailyActivityArchiveResult{
		Bucket:     a.bucket,
		Key:        key,
		Date:       date,
		Username:   username,
		TotalS:     totalS,
		TopApps:    topApps,
		SessionCnt: len(allSessions),
	}, nil
}

func (a *DailyActivityArchive) readObject(ctx context.Context, key string) (map[string]interface{}, error) {
	raw, err := a.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var notFound interface{ ErrorCode() string }
		if ok := errors.As(err, &notFound); ok && (notFound.ErrorCode() == "NoSuchKey" || notFound.ErrorCode() == "NotFound") {
			return nil, nil
		}
		return nil, err
	}
	defer raw.Body.Close()
	var doc map[string]interface{}
	if err := json.NewDecoder(raw.Body).Decode(&doc); err != nil {
		return nil, err
	}
	return doc, nil
}

func (a *DailyActivityArchive) DeleteDevice(ctx context.Context, deviceID string) (int, error) {
	if a == nil {
		return 0, nil
	}
	deleted := 0
	prefix := fmt.Sprintf("%s/", a.prefix)
	paginator := s3.NewListObjectsV2Paginator(a.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(a.bucket),
		Prefix: aws.String(prefix),
	})
	needle := "/device_id=" + safeS3PathSegment(deviceID) + "/"
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return deleted, err
		}
		for _, obj := range page.Contents {
			if obj.Key == nil || !strings.Contains(*obj.Key, needle) {
				continue
			}
			if _, err := a.client.DeleteObject(ctx, &s3.DeleteObjectInput{
				Bucket: aws.String(a.bucket),
				Key:    obj.Key,
			}); err != nil {
				return deleted, err
			}
			deleted++
		}
	}
	return deleted, nil
}

func dailyActivityS3Key(prefix, deviceID, username, date string) string {
	day := payloadTime(date + "T00:00:00Z")
	return fmt.Sprintf("%s/year=%s/month=%s/day=%s/device_id=%s/user=%s/activity.json",
		prefix,
		day.Format("2006"),
		day.Format("01"),
		day.Format("02"),
		safeS3PathSegment(deviceID),
		safeS3PathSegment(username),
	)
}

func telemetryMetadata(payload map[string]interface{}, archiveResult *TelemetryArchiveResult) bson.M {
	doc := bson.M{
		"device_id":             payload["device_id"],
		"timestamp":             payload["timestamp"],
		"ingested_at":           payload["ingested_at"],
		"telemetry_expires_at":  payload["telemetry_expires_at"],
		"telemetry_archive":     archiveResult,
		"telemetry_archive_key": archiveResult.Key,
		"agent_version":         payload["agent_version"],
		"agent_os":              payload["agent_os"],
		"agent_arch":            payload["agent_arch"],
		"username":              payload["username"],
		"app_usage_archive":     payload["app_usage_archive"],
	}
	if summaries := focusSummariesFromPayload(payload); len(summaries) > 0 {
		doc["data"] = bson.M{
			"ActiveWindowTracker": bson.M{
				"app_summaries": summaries,
			},
		}
	}
	return doc
}

func deviceIdentityFilters(deviceID, hardwareUUID, macAddress, hostname string) bson.A {
	filters := bson.A{}
	if deviceID != "" {
		filters = append(filters, bson.M{"device_id": deviceID})
	}
	if hardwareUUID != "" {
		filters = append(filters, bson.M{"hardware_uuid": hardwareUUID})
	}
	if macAddress != "" {
		filters = append(filters, bson.M{"mac_address": macAddress})
	}
	if hostname != "" && hostname != "unknown" {
		filters = append(filters, bson.M{"hostname": hostname})
	}
	return filters
}

func revokedDeviceFilter(deviceID, hardwareUUID, macAddress, hostname string) bson.M {
	filters := deviceIdentityFilters(deviceID, hardwareUUID, macAddress, hostname)
	if len(filters) == 0 {
		return bson.M{"_id": "__never__"}
	}
	return bson.M{"$or": filters}
}

func isDeviceRevoked(ctx context.Context, deviceID, hardwareUUID, macAddress, hostname string) bool {
	filter := revokedDeviceFilter(deviceID, hardwareUUID, macAddress, hostname)
	err := mongoClient.Database(dbName).Collection("device_revocations").FindOne(ctx, filter).Err()
	return err == nil
}

func createDeviceRevocation(ctx context.Context, device bson.M, reason string) error {
	now := time.Now().UTC()
	deviceID, _ := device["device_id"].(string)
	hardwareUUID, _ := device["hardware_uuid"].(string)
	macAddress, _ := device["mac_address"].(string)
	hostname, _ := device["hostname"].(string)
	if deviceID == "" && hardwareUUID == "" && macAddress == "" && hostname == "" {
		return nil
	}

	doc := bson.M{
		"device_id":     deviceID,
		"hardware_uuid": hardwareUUID,
		"mac_address":   macAddress,
		"hostname":      hostname,
		"reason":        reason,
		"revoked_at":    now,
	}
	update := bson.M{
		"$set":         doc,
		"$setOnInsert": bson.M{"created_at": now},
	}
	_, err := mongoClient.Database(dbName).Collection("device_revocations").UpdateOne(
		ctx,
		revokedDeviceFilter(deviceID, hardwareUUID, macAddress, hostname),
		update,
		options.Update().SetUpsert(true),
	)
	return err
}

func focusSummariesFromPayload(payload map[string]interface{}) []interface{} {
	data, ok := payload["data"].(map[string]interface{})
	if !ok {
		return nil
	}
	awt, ok := data["ActiveWindowTracker"].(map[string]interface{})
	if !ok {
		return nil
	}
	// Prefer cumulative_summaries (all apps since agent start) so persisted
	// documents capture the full picture rather than just the current-cycle delta.
	raw, ok := awt["cumulative_summaries"].([]interface{})
	if !ok || len(raw) == 0 {
		raw, ok = awt["app_summaries"].([]interface{})
	}
	if !ok || len(raw) == 0 {
		return nil
	}
	out := make([]interface{}, 0, len(raw))
	for _, item := range raw {
		if isVisibleAppUsageSummary(item) {
			out = append(out, item)
		}
	}
	return out
}

func sanitizeActiveWindowPayload(payload map[string]interface{}) {
	data, ok := payload["data"].(map[string]interface{})
	if !ok {
		return
	}
	awt, ok := data["ActiveWindowTracker"].(map[string]interface{})
	if !ok {
		return
	}
	if current, _ := awt["current_app"].(string); current != "" && !isVisibleAppUsageName(current) {
		awt["current_app"] = ""
	}
	for _, key := range []string{"app_summaries", "cumulative_summaries", "sessions"} {
		raw, ok := awt[key].([]interface{})
		if !ok {
			continue
		}
		filtered := make([]interface{}, 0, len(raw))
		for _, item := range raw {
			if isVisibleAppUsageSummary(item) {
				filtered = append(filtered, item)
			}
		}
		awt[key] = filtered
	}
}

var ignoredAppUsageNames = map[string]struct{}{
	// ── DevicePulse own processes ─────────────────────────────────────────────
	"devicepulse-age":   {},
	"devicepulse-agent": {},

	// ── systemd / init ────────────────────────────────────────────────────────
	"systemd":            {},
	"systemd-journald":   {},
	"systemd-logind":     {},
	"systemd-udevd":      {},
	"systemd-resolved":   {},
	"systemd-networkd":   {},
	"systemd-timesyncd":  {},
	"init":               {},

	// ── package managers / updaters ───────────────────────────────────────────
	"apt-check":          {},
	"apt.systemd.daily":  {},
	"apt-get":            {},
	"dpkg":               {},
	"packagekitd":        {},
	"packagekit":         {},
	"snapd":              {},
	"snap":               {},
	"appstreamcli":       {},
	"xdelta3":            {},
	"unattended-upgr":    {},
	"unattended-upgrade": {},
	"update-notifier":    {},
	"dnf":                {},
	"yum":                {},
	"rpm":                {},
	"zypper":             {},
	"pacman":             {},
	"flatpak":            {},

	// ── D-Bus / IPC daemons ───────────────────────────────────────────────────
	"dbus-daemon":         {},
	"dbus-launch":         {},
	"gdbus":               {},
	"ibus-daemon":         {},
	"at-spi-bus-launcher": {},
	"at-spi2-registryd":   {},

	// ── display / compositor infrastructure ───────────────────────────────────
	"xorg":          {},
	"xwayland":      {},
	"mutter":        {},
	"kwin_wayland":  {},
	"kwin_x11":      {},
	"kwin":          {},
	"openbox":       {},
	"xfwm4":         {},
	"picom":         {},
	"compiz":        {},
	"sway":          {},
	"wayfire":       {},

	// ── GNOME / KDE shell services ────────────────────────────────────────────
	"gnome-shell":           {},
	"gnome-session":         {},
	"gnome-session-binary":  {},
	"gnome-keyring-daemon":  {},
	"gnome-settings-daemon": {},
	"plasmashell":           {},
	"kded5":                 {},
	"kded6":                 {},
	"baloo_file":            {},
	"kactivitymanagerd":     {},

	// ── hardware / power / device daemons ────────────────────────────────────
	"upowerd":        {},
	"udisksd":        {},
	"bluetoothd":     {},
	"networkmanager": {},
	"modemmanager":   {},
	"wpa_supplicant": {},
	"avahi-daemon":   {},
	"cupsd":          {},
	"fwupd":          {},
	"thermald":       {},
	"rtkit-daemon":   {},
	"polkitd":        {},
	"colord":         {},
	"geoclue":        {},

	// ── logging / monitoring daemons ─────────────────────────────────────────
	"rsyslogd": {},
	"syslogd":  {},
	"auditd":   {},
	"crond":    {},
	"cron":     {},
	"atd":      {},

	// ── network / server daemons ─────────────────────────────────────────────
	"sshd":         {},
	"firewalld":    {},
	"nginx":        {},
	"apache2":      {},
	"httpd":        {},
	"mysqld":       {},
	"postgres":     {},
	"redis-server": {},

	// ── container / VM helpers ────────────────────────────────────────────────
	"dockerd":          {},
	"containerd":       {},
	"containerd-shim":  {},

	// ── generic shells / interpreters (never a visible "app") ────────────────
	"sh":      {},
	"bash":    {},
	"dash":    {},
	"zsh":     {},
	"fish":    {},
	"python":  {},
	"python3": {},
	"perl":    {},
	"ruby":    {},
	"node":    {},
	"gjs":     {},

	// ── audio / media pipeline daemons ───────────────────────────────────────
	"pipewire":       {},
	"pipewire-pulse": {},
	"wireplumber":    {},
	"pulseaudio":     {},
}

func isLinuxSystemProcessName(key string) bool {
	return strings.HasPrefix(key, "systemd-") ||
		strings.HasPrefix(key, "gsd-") ||
		strings.HasPrefix(key, "gvfs") ||
		strings.HasPrefix(key, "gnome-") ||
		strings.HasPrefix(key, "plasma") ||
		strings.HasPrefix(key, "akonadi") ||
		strings.HasPrefix(key, "xdg-") ||
		strings.HasPrefix(key, "tracker") ||
		strings.HasPrefix(key, "evolution") ||
		strings.HasPrefix(key, "devicepulse-")
}

func isVisibleAppUsageName(name string) bool {
	key := strings.ToLower(strings.TrimSpace(name))
	key = strings.TrimSuffix(key, ".service")
	if key == "" {
		return false
	}
	if _, ignored := ignoredAppUsageNames[key]; ignored {
		return false
	}
	if isLinuxSystemProcessName(key) {
		return false
	}
	// Catch any remaining packagekit variants
	if strings.Contains(key, "packagekit") {
		return false
	}
	return true
}

func isVisibleAppUsageSummary(raw interface{}) bool {
	switch m := raw.(type) {
	case map[string]interface{}:
		name, _ := m["app_name"].(string)
		return isVisibleAppUsageName(name)
	case bson.M:
		name, _ := m["app_name"].(string)
		return isVisibleAppUsageName(name)
	default:
		return false
	}
}

func filterDailyAppUsageRows(rows []bson.M) []bson.M {
	filteredRows := make([]bson.M, 0, len(rows))
	for _, row := range rows {
		topApps, totalS, sessionCount := filterDailyTopApps(row["top_apps"])
		if len(topApps) == 0 {
			continue
		}
		row["top_apps"] = topApps
		row["total_seconds"] = totalS
		row["session_count"] = sessionCount
		filteredRows = append(filteredRows, row)
	}
	return filteredRows
}

func filterDailyTopApps(raw interface{}) ([]interface{}, float64, int) {
	items, ok := raw.(bson.A)
	if !ok {
		items, ok = raw.([]interface{})
	}
	if !ok {
		return nil, 0, 0
	}
	out := make([]interface{}, 0, len(items))
	totalS := 0.0
	sessionCount := 0
	for _, item := range items {
		if !isVisibleAppUsageSummary(item) {
			continue
		}
		out = append(out, item)
		switch m := item.(type) {
		case bson.M:
			seconds, _ := toFloat(m["total_seconds"])
			sessions, _ := toFloat(m["session_count"])
			totalS += seconds
			sessionCount += int(sessions)
		case map[string]interface{}:
			seconds, _ := toFloat(m["total_seconds"])
			sessions, _ := toFloat(m["session_count"])
			totalS += seconds
			sessionCount += int(sessions)
		}
	}
	return out, totalS, sessionCount
}

func activityUsername(payload map[string]interface{}) string {
	if username, ok := payload["username"].(string); ok && strings.TrimSpace(username) != "" {
		return strings.TrimSpace(username)
	}
	return "unknown"
}

func extractFocusSessions(payload map[string]interface{}) []interface{} {
	data, ok := payload["data"].(map[string]interface{})
	if !ok {
		return nil
	}
	awt, ok := data["ActiveWindowTracker"].(map[string]interface{})
	if !ok {
		return nil
	}
	raw, ok := awt["sessions"].([]interface{})
	if !ok || len(raw) == 0 {
		return nil
	}
	username := activityUsername(payload)
	deviceID, _ := payload["device_id"].(string)
	out := make([]interface{}, 0, len(raw))
	for _, item := range raw {
		session, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		appName, _ := session["app_name"].(string)
		appName = strings.TrimSpace(appName)
		if !isVisibleAppUsageName(appName) {
			continue
		}
		duration, ok := toFloat(session["duration_seconds"])
		if !ok || duration <= 0 {
			continue
		}
		out = append(out, map[string]interface{}{
			"app_name":         appName,
			"start_time":       session["start_time"],
			"end_time":         session["end_time"],
			"duration_seconds": duration,
			"device_id":        deviceID,
			"username":         username,
		})
	}
	return out
}

func appendInterfaceSlice(existing interface{}, next []interface{}) []interface{} {
	var out []interface{}
	if items, ok := existing.([]interface{}); ok {
		out = append(out, items...)
	}
	out = append(out, next...)
	return out
}

func activitySessionStart(raw interface{}) time.Time {
	m, ok := raw.(map[string]interface{})
	if !ok {
		return time.Time{}
	}
	if s, ok := m["start_time"].(string); ok {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func summarizeActivitySessions(sessions []interface{}) ([]interface{}, float64) {
	type appTotal struct {
		AppName      string
		TotalSeconds float64
		SessionCount int
	}
	totals := map[string]*appTotal{}
	totalS := 0.0
	for _, raw := range sessions {
		m, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		appName, _ := m["app_name"].(string)
		appName = strings.TrimSpace(appName)
		duration, ok := toFloat(m["duration_seconds"])
		if !isVisibleAppUsageName(appName) || !ok || duration <= 0 {
			continue
		}
		item, ok := totals[appName]
		if !ok {
			item = &appTotal{AppName: appName}
			totals[appName] = item
		}
		item.TotalSeconds += duration
		item.SessionCount++
		totalS += duration
	}
	apps := make([]interface{}, 0, len(totals))
	for _, item := range totals {
		apps = append(apps, map[string]interface{}{
			"app_name":      item.AppName,
			"total_seconds": item.TotalSeconds,
			"session_count": item.SessionCount,
		})
	}
	sort.Slice(apps, func(i, j int) bool {
		a := apps[i].(map[string]interface{})
		b := apps[j].(map[string]interface{})
		av, _ := toFloat(a["total_seconds"])
		bv, _ := toFloat(b["total_seconds"])
		return av > bv
	})
	return apps, totalS
}

// ─── Browser History S3 Archive ────────────────────────────────────────────────

type BrowserHistoryArchive struct {
	client *s3.Client
	bucket string
	prefix string
}

type BrowserHistoryArchiveObject struct {
	DeviceID   string        `json:"device_id"`
	Timestamp  interface{}   `json:"timestamp"`
	IngestedAt interface{}   `json:"ingested_at"`
	SyncType   string        `json:"sync_type"`
	EntryCount int           `json:"entry_count"`
	Entries    []interface{} `json:"entries"`
}

type BrowserHistoryArchiveResult struct {
	Bucket     string   `json:"bucket" bson:"bucket"`
	Key        string   `json:"key,omitempty" bson:"key,omitempty"`
	Keys       []string `json:"keys" bson:"keys"`
	EntryCount int      `json:"entry_count" bson:"entry_count"`
}

func initBrowserHistoryArchive(ctx context.Context) *BrowserHistoryArchive {
	bucket := strings.TrimSpace(os.Getenv("BROWSER_HISTORY_S3_BUCKET"))
	if bucket == "" {
		bucket = strings.TrimSpace(os.Getenv("S3_BUCKET"))
	}
	if bucket == "" {
		log.Println("Browser history S3 archive disabled (set BROWSER_HISTORY_S3_BUCKET to enable)")
		return nil
	}

	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Printf("Browser history S3 archive disabled: AWS config error: %v", err)
		return nil
	}

	prefix := strings.Trim(strings.TrimSpace(os.Getenv("BROWSER_HISTORY_S3_PREFIX")), "/")
	if prefix == "" {
		prefix = "browser-history"
	}
	endpoint := strings.TrimSpace(os.Getenv("BROWSER_HISTORY_S3_ENDPOINT"))
	usePathStyle := strings.EqualFold(os.Getenv("BROWSER_HISTORY_S3_PATH_STYLE"), "true") || endpoint != ""
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
		o.UsePathStyle = usePathStyle
	})

	log.Printf("Browser history S3 archive enabled: bucket=%s prefix=%s", bucket, prefix)
	return &BrowserHistoryArchive{client: client, bucket: bucket, prefix: prefix}
}

func (a *BrowserHistoryArchive) Archive(ctx context.Context, deviceID string, payload map[string]interface{}) (*BrowserHistoryArchiveResult, error) {
	if a == nil {
		return nil, nil
	}
	browserPayload, entries := extractBrowserHistoryEntries(payload)
	if len(entries) == 0 {
		return nil, nil
	}

	syncType, _ := browserPayload["sync_type"].(string)
	eventTime := payloadTime(payload["timestamp"])
	groups := map[string][]interface{}{}
	for _, entry := range entries {
		visitAt := browserHistoryEntryVisitTime(entry, eventTime)
		day := visitAt.Format("2006-01-02")
		groups[day] = append(groups[day], entry)
	}

	result := &BrowserHistoryArchiveResult{Bucket: a.bucket, EntryCount: len(entries)}
	for day, group := range groups {
		doc := BrowserHistoryArchiveObject{
			DeviceID:   deviceID,
			Timestamp:  payload["timestamp"],
			IngestedAt: payload["ingested_at"],
			SyncType:   syncType,
			EntryCount: len(group),
			Entries:    group,
		}
		body, err := json.Marshal(doc)
		if err != nil {
			return nil, err
		}

		sum := sha256.Sum256(body)
		key := fmt.Sprintf("%s/device_id=%s/date=%s/%s-%x.json",
			a.prefix,
			safeS3PathSegment(deviceID),
			day,
			eventTime.UTC().Format("20060102T150405.000000000Z"),
			sum[:8],
		)
		_, err = a.client.PutObject(ctx, &s3.PutObjectInput{
			Bucket:      aws.String(a.bucket),
			Key:         aws.String(key),
			Body:        bytes.NewReader(body),
			ContentType: aws.String("application/json"),
		})
		if err != nil {
			return nil, err
		}
		result.Keys = append(result.Keys, key)
	}
	sort.Strings(result.Keys)
	if len(result.Keys) > 0 {
		result.Key = result.Keys[0]
	}
	return result, nil
}

func (a *BrowserHistoryArchive) ReadEntries(ctx context.Context, deviceID string, from, to time.Time, limit int) ([]interface{}, error) {
	if a == nil {
		return nil, fmt.Errorf("browser history S3 archive is not configured")
	}
	latestByKey := map[string]interface{}{}
	latestVisitByKey := map[string]int64{}
	var entries []interface{}

	for day := startOfDay(from); !day.After(startOfDay(to)); day = day.AddDate(0, 0, 1) {
		prefix := fmt.Sprintf("%s/device_id=%s/date=%s/", a.prefix, safeS3PathSegment(deviceID), day.Format("2006-01-02"))
		paginator := s3.NewListObjectsV2Paginator(a.client, &s3.ListObjectsV2Input{
			Bucket: aws.String(a.bucket),
			Prefix: aws.String(prefix),
		})
		for paginator.HasMorePages() {
			page, err := paginator.NextPage(ctx)
			if err != nil {
				return nil, err
			}
			for _, obj := range page.Contents {
				if obj.Key == nil {
					continue
				}
				raw, err := a.client.GetObject(ctx, &s3.GetObjectInput{
					Bucket: aws.String(a.bucket),
					Key:    obj.Key,
				})
				if err != nil {
					return nil, err
				}
				var archive BrowserHistoryArchiveObject
				decodeErr := json.NewDecoder(raw.Body).Decode(&archive)
				closeErr := raw.Body.Close()
				if decodeErr != nil {
					return nil, decodeErr
				}
				if closeErr != nil {
					return nil, closeErr
				}
				for _, entry := range archive.Entries {
					m, ok := entry.(map[string]interface{})
					if !ok {
						continue
					}
					visitTime, ok := toFloat(m["last_visit_time"])
					if !ok {
						continue
					}
					visitAt := time.Unix(0, int64(visitTime))
					if visitAt.Before(from) || visitAt.After(to) {
						continue
					}
					dedupeKey := browserHistoryDedupeKey(m)
					if dedupeKey == "" {
						entries = append(entries, entry)
						continue
					}
					visitNanos := int64(visitTime)
					if existing, ok := latestVisitByKey[dedupeKey]; ok && existing >= visitNanos {
						continue
					}
					latestVisitByKey[dedupeKey] = visitNanos
					latestByKey[dedupeKey] = entry
				}
			}
		}
	}

	for _, entry := range latestByKey {
		entries = append(entries, entry)
	}

	sort.Slice(entries, func(i, j int) bool {
		return browserHistoryVisitNanos(entries[i]) > browserHistoryVisitNanos(entries[j])
	})
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}

func (a *BrowserHistoryArchive) DeleteDevice(ctx context.Context, deviceID string) (int, error) {
	if a == nil {
		return 0, nil
	}

	deleted := 0
	prefix := fmt.Sprintf("%s/device_id=%s/", a.prefix, safeS3PathSegment(deviceID))
	paginator := s3.NewListObjectsV2Paginator(a.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(a.bucket),
		Prefix: aws.String(prefix),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return deleted, err
		}
		for _, obj := range page.Contents {
			if obj.Key == nil {
				continue
			}
			if _, err := a.client.DeleteObject(ctx, &s3.DeleteObjectInput{
				Bucket: aws.String(a.bucket),
				Key:    obj.Key,
			}); err != nil {
				return deleted, err
			}
			deleted++
		}
	}
	return deleted, nil
}

func extractBrowserHistoryEntries(payload map[string]interface{}) (map[string]interface{}, []interface{}) {
	data, ok := payload["data"].(map[string]interface{})
	if !ok {
		return nil, nil
	}
	rawBrowser, ok := data["BrowserHistory"]
	if !ok {
		return nil, nil
	}
	browserPayload, ok := rawBrowser.(map[string]interface{})
	if !ok {
		return nil, nil
	}
	seen := map[string]struct{}{}
	entries := []interface{}{}
	appendBrowserHistoryEntries(&entries, seen, browserPayload["new_history_entries"])
	appendBrowserHistoryEntries(&entries, seen, browserPayload["top_recent_urls"])
	return browserPayload, entries
}

func appendBrowserHistoryEntries(entries *[]interface{}, seen map[string]struct{}, raw interface{}) {
	items, ok := raw.([]interface{})
	if !ok {
		return
	}
	for _, entry := range items {
		if m, ok := entry.(map[string]interface{}); ok {
			if key := browserHistoryDedupeKey(m); key != "" {
				if _, exists := seen[key]; exists {
					continue
				}
				seen[key] = struct{}{}
			}
		}
		*entries = append(*entries, entry)
	}
}

func pruneArchivedBrowserHistory(payload map[string]interface{}, archiveResult *BrowserHistoryArchiveResult) {
	if archiveResult == nil {
		return
	}
	data, ok := payload["data"].(map[string]interface{})
	if !ok {
		return
	}
	browserPayload, ok := data["BrowserHistory"].(map[string]interface{})
	if !ok {
		return
	}
	delete(browserPayload, "new_history_entries")
	delete(browserPayload, "top_recent_urls")
	browserPayload["archive"] = archiveResult
}

func payloadTime(v interface{}) time.Time {
	if s, ok := v.(string); ok {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t.UTC()
		}
	}
	return time.Now().UTC()
}

func safeS3PathSegment(s string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", " ", "_", ":", "_")
	return replacer.Replace(s)
}

func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

func browserHistoryDedupeKey(entry map[string]interface{}) string {
	url, _ := entry["url"].(string)
	title, _ := entry["title"].(string)
	browser, _ := entry["browser"].(string)
	if url == "" {
		return ""
	}
	identity := strings.TrimSpace(strings.ToLower(title))
	if identity == "" {
		identity = normalizedBrowserHistoryURL(url)
	}
	return strings.ToLower(browser) + "|" + identity
}

func normalizedBrowserHistoryURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return strings.ToLower(strings.TrimSpace(raw))
	}
	return strings.ToLower(parsed.Host + parsed.Path)
}

func browserHistoryVisitNanos(entry interface{}) int64 {
	m, ok := entry.(map[string]interface{})
	if !ok {
		return 0
	}
	v, ok := toFloat(m["last_visit_time"])
	if !ok {
		return 0
	}
	return int64(v)
}

func browserHistoryEntryVisitTime(entry interface{}, fallback time.Time) time.Time {
	nanos := browserHistoryVisitNanos(entry)
	if nanos <= 0 {
		return fallback.UTC()
	}
	return time.Unix(0, nanos).UTC()
}

// loadPolicyFromMongo reads the persisted policy document from MongoDB.
// Called once at startup so the last-saved policy survives API restarts.
func loadPolicyFromMongo() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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

func cleanupBootstrapLocks() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	db := mongoClient.Database(dbName)
	users, err := db.Collection("users").CountDocuments(ctx, bson.M{})
	if err != nil {
		log.Printf("Bootstrap cleanup skipped: users count error: %v", err)
		return
	}
	if users > 0 {
		if _, err := db.Collection("config").DeleteOne(ctx, bson.M{"_id": "dashboard_bootstrap"}); err != nil {
			log.Printf("Bootstrap cleanup failed: %v", err)
		}
		return
	}

	staleBefore := time.Now().Add(-15 * time.Minute)
	res, err := db.Collection("config").DeleteOne(ctx, bson.M{
		"_id":        "dashboard_bootstrap",
		"created_at": bson.M{"$lt": staleBefore},
	})
	if err != nil {
		log.Printf("Stale bootstrap cleanup failed: %v", err)
		return
	}
	if res.DeletedCount > 0 {
		log.Printf("Removed stale dashboard bootstrap lock")
	}
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

type deviceNameRequest struct {
	Name string `json:"name"`
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
	err := coll.FindOne(ctx, bson.M{
		"api_key": key,
		"status":  bson.M{"$ne": "revoked"},
	}).Decode(&device)
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

	if isDeviceRevoked(ctx, "", hardwareUUID, macAddress, hostname) {
		http.Error(w, "Device registration is revoked", http.StatusForbidden)
		return
	}

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
		deviceID, _ := existing["device_id"].(string)
		if isDeviceRevoked(ctx, deviceID, hardwareUUID, macAddress, hostname) {
			http.Error(w, "Device registration is revoked", http.StatusForbidden)
			return
		}
		// Device already registered — return existing credentials.
		// Also update hostname/mac/uuid in case they changed (e.g. NIC swap).
		update := bson.M{"$set": bson.M{
			"hostname":      hostname,
			"hardware_uuid": hardwareUUID,
			"mac_address":   macAddress,
			"last_seen":     time.Now(),
		}}
		coll.UpdateOne(ctx, dedupFilter, update)

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

	now := time.Now().UTC()
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
	if _, err := mongoClient.Database(dbName).Collection("config").DeleteOne(ctx, bson.M{"_id": "dashboard_bootstrap"}); err != nil {
		log.Printf("Bootstrap lock cleanup failed: %v", err)
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
	if isDeviceRevoked(ctx, authDeviceID, "", "", "") {
		http.Error(w, "Device is revoked", http.StatusForbidden)
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
	sanitizeActiveWindowPayload(payload)

	if browserArchive != nil {
		archiveResult, err := browserArchive.Archive(ctx, authDeviceID, payload)
		if err != nil {
			log.Printf("Browser history S3 archive error for %s: %v", authDeviceID, err)
			http.Error(w, "Browser history archive error", http.StatusInternalServerError)
			return
		}
		if archiveResult != nil {
			payload["browser_history_archive"] = archiveResult
			pruneArchivedBrowserHistory(payload, archiveResult)
		}
	}

	var activityArchiveResult *DailyActivityArchiveResult
	if activityStore != nil {
		activityArchiveResult, err = activityStore.Archive(ctx, authDeviceID, payload)
		if err != nil {
			log.Printf("Daily app usage S3 archive error for %s: %v", authDeviceID, err)
			http.Error(w, "Daily app usage archive error", http.StatusInternalServerError)
			return
		}
		if activityArchiveResult != nil {
			payload["app_usage_archive"] = activityArchiveResult
		}
	}

	var telemetryArchiveResult *TelemetryArchiveResult
	if telemetryStore != nil {
		telemetryArchiveResult, err = telemetryStore.Archive(ctx, authDeviceID, payload)
		if err != nil {
			log.Printf("Telemetry S3 archive error for %s: %v", authDeviceID, err)
			http.Error(w, "Telemetry archive error", http.StatusInternalServerError)
			return
		}
		payload["telemetry_archive"] = telemetryArchiveResult
	}

	collTelemetry := mongoClient.Database(dbName).Collection("telemetry")
	collDevices := mongoClient.Database(dbName).Collection("devices")
	collActivity := mongoClient.Database(dbName).Collection("app_usage_daily")

	// Insert telemetry event
	telemetryDoc := bson.M(payload)
	if telemetryArchiveResult != nil {
		telemetryDoc = telemetryMetadata(payload, telemetryArchiveResult)
	}
	if _, err = collTelemetry.InsertOne(ctx, telemetryDoc); err != nil {
		log.Printf("Error inserting telemetry: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	// Upsert latest device state. Collector payloads are applied as deltas so an
	// incremental/empty collector result does not erase the last useful snapshot.
	opts := options.Update().SetUpsert(true)
	setDoc := bson.M{
		"device_id": authDeviceID,
		"timestamp": payload["timestamp"],
		"last_seen": now,
	}
	if version, ok := payload["agent_version"].(string); ok && version != "" {
		setDoc["agent_version"] = version
	}
	if agentOS, ok := payload["agent_os"].(string); ok && agentOS != "" {
		setDoc["agent_os"] = agentOS
	}
	if agentArch, ok := payload["agent_arch"].(string); ok && agentArch != "" {
		setDoc["agent_arch"] = agentArch
	}
	if username, ok := payload["username"].(string); ok && username != "" {
		setDoc["username"] = username
	}
	if data, ok := payload["data"].(map[string]interface{}); ok {
		for collectorName, collectorPayload := range data {
			if collectorName == "BrowserHistory" {
				applyBrowserHistoryDelta(setDoc, collectorPayload)
				continue
			}
			setDoc["data."+collectorName] = collectorPayload
		}
	} else {
		setDoc["data"] = payload["data"]
	}
	update := bson.M{"$set": setDoc}
	if _, archived := payload["browser_history_archive"]; archived {
		update["$unset"] = bson.M{
			"data.BrowserHistory.new_history_entries": "",
			"data.BrowserHistory.top_recent_urls":     "",
		}
	}
	if _, err = collDevices.UpdateOne(ctx, bson.M{"device_id": authDeviceID}, update, opts); err != nil {
		log.Printf("Error updating device state: %v", err)
	}

	if activityArchiveResult != nil {
		activityDoc := bson.M{
			"device_id":      authDeviceID,
			"username":       activityArchiveResult.Username,
			"date":           activityArchiveResult.Date,
			"total_seconds":  activityArchiveResult.TotalS,
			"session_count":  activityArchiveResult.SessionCnt,
			"top_apps":       activityArchiveResult.TopApps,
			"archive":        activityArchiveResult,
			"archive_bucket": activityArchiveResult.Bucket,
			"archive_key":    activityArchiveResult.Key,
			"updated_at":     now,
		}
		_, err = collActivity.UpdateOne(
			ctx,
			bson.M{"device_id": authDeviceID, "username": activityArchiveResult.Username, "date": activityArchiveResult.Date},
			bson.M{"$set": activityDoc, "$setOnInsert": bson.M{"created_at": now}},
			options.Update().SetUpsert(true),
		)
		if err != nil {
			log.Printf("Error updating daily app usage summary: %v", err)
		}
	}

	// Update focus cache from cumulative_summaries (all apps since agent start),
	// falling back to the per-cycle app_summaries if cumulative is absent.
	go func() {
		if data, ok := payload["data"].(map[string]interface{}); ok {
			if awt, ok := data["ActiveWindowTracker"].(map[string]interface{}); ok {
				raw, ok := awt["cumulative_summaries"].([]interface{})
				if !ok || len(raw) == 0 {
					raw, ok = awt["app_summaries"].([]interface{})
				}
				if ok && len(raw) > 0 {
					globalFocusCache.applyFocusSummaries(authDeviceID, raw)
				}
			}
		}
	}()

	log.Printf("Ingested telemetry for %s", authDeviceID)
	response := bson.M{"ok": true}
	var deviceState bson.M
	if err := collDevices.FindOne(ctx, bson.M{"device_id": authDeviceID}).Decode(&deviceState); err == nil {
		if status, _ := deviceState["agent_update_status"].(string); status == "update_requested" {
			response["check_update_now"] = true
			if target, _ := deviceState["agent_target_version"].(string); target != "" {
				response["target_version"] = target
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func applyBrowserHistoryDelta(setDoc bson.M, v interface{}) {
	m, ok := v.(map[string]interface{})
	if !ok {
		return
	}
	if urls, ok := m["top_recent_urls"].([]interface{}); ok && len(urls) > 0 {
		setDoc["data.BrowserHistory.top_recent_urls"] = urls
	}
	if syncType, ok := m["sync_type"].(string); ok && syncType != "" {
		setDoc["data.BrowserHistory.sync_type"] = syncType
	}
	if archive, ok := m["archive"]; ok {
		setDoc["data.BrowserHistory.archive"] = archive
	}
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

func bsonTime(v interface{}) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t, !t.IsZero()
	case primitive.DateTime:
		return t.Time(), !t.Time().IsZero()
	case string:
		parsed, err := time.Parse(time.RFC3339, t)
		if err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

// ─── Device Delete Handler ─────────────────────────────────────────────────────

// PUT /devices/{device_id}/name
func deviceNameHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) != 3 || parts[0] != "devices" || parts[1] == "" || parts[2] != "name" {
		http.Error(w, "Invalid URL", http.StatusBadRequest)
		return
	}
	deviceID := parts[1]

	var body deviceNameRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Bad JSON", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(body.Name)
	if len(name) > 80 {
		http.Error(w, "Name must be 80 characters or fewer", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	update := bson.M{"$set": bson.M{"updated_at": time.Now()}}
	if name == "" {
		update["$unset"] = bson.M{"display_name": ""}
	} else {
		update["$set"].(bson.M)["display_name"] = name
	}
	res, err := mongoClient.Database(dbName).Collection("devices").UpdateOne(ctx, bson.M{"device_id": deviceID}, update)
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	if res.MatchedCount == 0 {
		http.Error(w, "Device not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"device_id":    deviceID,
		"display_name": name,
	})
}

// POST /devices/{device_id}/ping
func devicePingHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) != 3 || parts[0] != "devices" || parts[1] == "" || parts[2] != "ping" {
		http.Error(w, "Invalid URL", http.StatusBadRequest)
		return
	}
	deviceID := parts[1]

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var device bson.M
	err := mongoClient.Database(dbName).Collection("devices").FindOne(
		ctx,
		bson.M{"device_id": deviceID},
		options.FindOne().SetProjection(bson.M{"api_key": 0}),
	).Decode(&device)
	if err == mongo.ErrNoDocuments {
		http.Error(w, "Device not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	lastSeen, hasLastSeen := bsonTime(device["last_seen"])
	now := time.Now()
	ageSeconds := -1
	online := false
	var lastSeenValue interface{}
	if hasLastSeen {
		age := now.Sub(lastSeen)
		ageSeconds = int(age.Seconds())
		online = age < 60*time.Second
		lastSeenValue = lastSeen
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"device_id":     deviceID,
		"online":        online,
		"last_seen":     lastSeenValue,
		"age_seconds":   ageSeconds,
		"checked_at":    now,
		"message":       map[bool]string{true: "Agent is online", false: "No recent agent check-in"}[online],
		"live_window_s": 60,
	})
}

// GET /devices/{device_id}/app-usage?date=YYYY-MM-DD
func deviceAppUsageHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) != 3 || parts[0] != "devices" || parts[1] == "" || parts[2] != "app-usage" {
		http.Error(w, "Invalid URL", http.StatusBadRequest)
		return
	}
	deviceID := parts[1]
	date := strings.TrimSpace(r.URL.Query().Get("date"))
	if date == "" {
		date = time.Now().UTC().Format("2006-01-02")
	}
	if _, err := time.Parse("2006-01-02", date); err != nil {
		http.Error(w, "Invalid date; use YYYY-MM-DD", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cursor, err := mongoClient.Database(dbName).Collection("app_usage_daily").Find(
		ctx,
		bson.M{"device_id": deviceID, "date": date},
		options.Find().SetSort(bson.D{{Key: "total_seconds", Value: -1}}),
	)
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var rows []bson.M
	if err := cursor.All(ctx, &rows); err != nil {
		http.Error(w, "Parsing error", http.StatusInternalServerError)
		return
	}
	if rows == nil {
		rows = []bson.M{}
	}
	rows = filterDailyAppUsageRows(rows)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"device_id": deviceID,
		"date":      date,
		"users":     rows,
	})
}

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

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	db := mongoClient.Database(dbName)
	var existing bson.M
	if err := db.Collection("devices").FindOne(ctx, bson.M{"device_id": deviceID}).Decode(&existing); err != nil {
		http.Error(w, "Device not found", http.StatusNotFound)
		return
	}
	if err := createDeviceRevocation(ctx, existing, "device_deleted"); err != nil {
		log.Printf("Device revocation failed for deleted device %s: %v", deviceID, err)
		http.Error(w, "Device revocation failed", http.StatusInternalServerError)
		return
	}

	res, err := db.Collection("devices").DeleteOne(ctx, bson.M{"device_id": deviceID})
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	if res.DeletedCount == 0 {
		http.Error(w, "Device not found", http.StatusNotFound)
		return
	}

	telemetryRes, err := db.Collection("telemetry").DeleteMany(ctx, bson.M{"device_id": deviceID})
	if err != nil {
		log.Printf("Telemetry cleanup failed for deleted device %s: %v", deviceID, err)
		http.Error(w, "Device deleted, but telemetry cleanup failed", http.StatusInternalServerError)
		return
	}

	telemetryArchiveDeleted, err := telemetryStore.DeleteDevice(ctx, deviceID)
	if err != nil {
		log.Printf("Telemetry archive cleanup failed for deleted device %s: %v", deviceID, err)
		http.Error(w, "Device deleted, but telemetry archive cleanup failed", http.StatusInternalServerError)
		return
	}

	browserArchiveDeleted, err := browserArchive.DeleteDevice(ctx, deviceID)
	if err != nil {
		log.Printf("Browser history archive cleanup failed for deleted device %s: %v", deviceID, err)
		http.Error(w, "Device deleted, but browser history archive cleanup failed", http.StatusInternalServerError)
		return
	}

	appUsageRes, err := db.Collection("app_usage_daily").DeleteMany(ctx, bson.M{"device_id": deviceID})
	if err != nil {
		log.Printf("App usage summary cleanup failed for deleted device %s: %v", deviceID, err)
		http.Error(w, "Device deleted, but app usage cleanup failed", http.StatusInternalServerError)
		return
	}
	appUsageArchiveDeleted, err := activityStore.DeleteDevice(ctx, deviceID)
	if err != nil {
		log.Printf("App usage archive cleanup failed for deleted device %s: %v", deviceID, err)
		http.Error(w, "Device deleted, but app usage archive cleanup failed", http.StatusInternalServerError)
		return
	}

	globalFocusCache.mu.Lock()
	delete(globalFocusCache.data, deviceID)
	globalFocusCache.mu.Unlock()

	log.Printf("Device deleted: %s (telemetry=%d telemetry_archive_objects=%d browser_archive_objects=%d app_usage=%d app_usage_archive_objects=%d)", deviceID, telemetryRes.DeletedCount, telemetryArchiveDeleted, browserArchiveDeleted, appUsageRes.DeletedCount, appUsageArchiveDeleted)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"device_id":                       deviceID,
		"deleted":                         true,
		"revoked":                         true,
		"telemetry_deleted_count":         telemetryRes.DeletedCount,
		"telemetry_archive_deleted_count": telemetryArchiveDeleted,
		"browser_archive_deleted_count":   browserArchiveDeleted,
		"app_usage_deleted_count":         appUsageRes.DeletedCount,
		"app_usage_archive_deleted_count": appUsageArchiveDeleted,
		"focus_cache_deleted":             true,
	})
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
			"$slice": 200,
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
	for i, doc := range history {
		archive, ok := doc["telemetry_archive"].(bson.M)
		if !ok {
			continue
		}
		if telemetryStore == nil {
			log.Printf("Telemetry history read failed for %s: archive is configured in Mongo but S3 store is disabled", deviceID)
			http.Error(w, "Telemetry S3 archive is not configured", http.StatusServiceUnavailable)
			return
		}
		payload, err := telemetryStore.Read(ctx, archive)
		if err != nil {
			log.Printf("Telemetry S3 read error for %s: %v", deviceID, err)
			http.Error(w, "S3 archive read error", http.StatusInternalServerError)
			return
		}
		payload["_id"] = doc["_id"]
		payload["telemetry_archive"] = doc["telemetry_archive"]
		history[i] = payload
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(history)
}

// GET /devices/{device_id}/browser-history?from=YYYY-MM-DD&to=YYYY-MM-DD&limit=N
func browserHistoryHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if browserArchive == nil {
		http.Error(w, "Browser history S3 archive is not configured", http.StatusServiceUnavailable)
		return
	}

	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		http.Error(w, "Invalid URL", http.StatusBadRequest)
		return
	}
	deviceID := parts[2]

	now := time.Now()
	defaultFrom := startOfDay(now.AddDate(0, 0, -currentRetentionDays()+1))
	defaultTo := startOfDay(now).AddDate(0, 0, 1).Add(-time.Nanosecond)
	from, err := parseDateQuery(r.URL.Query().Get("from"), defaultFrom, false)
	if err != nil {
		http.Error(w, "Invalid from date. Use YYYY-MM-DD.", http.StatusBadRequest)
		return
	}
	to, err := parseDateQuery(r.URL.Query().Get("to"), defaultTo, true)
	if err != nil {
		http.Error(w, "Invalid to date. Use YYYY-MM-DD.", http.StatusBadRequest)
		return
	}
	if to.Before(from) {
		http.Error(w, "to must be on or after from", http.StatusBadRequest)
		return
	}

	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			http.Error(w, "Invalid limit", http.StatusBadRequest)
			return
		}
		limit = parsed
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	entries, err := browserArchive.ReadEntries(ctx, deviceID, from, to, limit)
	if err != nil {
		log.Printf("Browser history S3 read error for %s: %v", deviceID, err)
		http.Error(w, "S3 archive read error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"device_id": deviceID,
		"from":      from.Format(time.RFC3339),
		"to":        to.Format(time.RFC3339),
		"count":     len(entries),
		"entries":   entries,
	})
}

func parseDateQuery(raw string, fallback time.Time, endOfDay bool) (time.Time, error) {
	if raw == "" {
		return fallback, nil
	}
	t, err := time.ParseInLocation("2006-01-02", raw, time.UTC)
	if err != nil {
		return time.Time{}, err
	}
	if endOfDay {
		return t.AddDate(0, 0, 1).Add(-time.Nanosecond), nil
	}
	return t, nil
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
	S3Key       string    `bson:"s3_key,omitempty" json:"s3_key,omitempty"`
	Checksum    string    `bson:"checksum_sha256"  json:"checksum_sha256"`
	PublishedAt time.Time `bson:"published_at"     json:"published_at"`
}

type AgentBuildArtifact struct {
	OS          string `bson:"os"              json:"os"`
	Arch        string `bson:"arch"            json:"arch"`
	Kind        string `bson:"kind"            json:"kind"`
	FileName    string `bson:"file_name"       json:"file_name"`
	S3Key       string `bson:"s3_key"          json:"s3_key"`
	DownloadURL string `bson:"download_url"    json:"download_url"`
	Checksum    string `bson:"checksum_sha256" json:"checksum_sha256"`
	SizeBytes   int64  `bson:"size_bytes"      json:"size_bytes"`
}

type AgentBuildJob struct {
	ID         primitive.ObjectID   `bson:"_id,omitempty"    json:"id"`
	Version    string               `bson:"version"          json:"version"`
	APIURL     string               `bson:"api_url"          json:"api_url"`
	Platforms  []string             `bson:"platforms"        json:"platforms"`
	Archs      []string             `bson:"archs"            json:"archs"`
	Status     string               `bson:"status"           json:"status"`
	Error      string               `bson:"error,omitempty"  json:"error,omitempty"`
	Logs       []string             `bson:"logs"             json:"logs"`
	Artifacts  []AgentBuildArtifact `bson:"artifacts"        json:"artifacts"`
	CreatedAt  time.Time            `bson:"created_at"       json:"created_at"`
	UpdatedAt  time.Time            `bson:"updated_at"       json:"updated_at"`
	StartedAt  *time.Time           `bson:"started_at,omitempty" json:"started_at,omitempty"`
	FinishedAt *time.Time           `bson:"finished_at,omitempty" json:"finished_at,omitempty"`
}

type AgentBuildRequest struct {
	Version   string   `json:"version"`
	APIURL    string   `json:"api_url"`
	Platforms []string `json:"platforms"`
	Archs     []string `json:"archs"`
}

var (
	agentBuildMu      sync.Mutex
	agentVersionRegex = regexp.MustCompile(`^[0-9]+(\.[0-9]+){1,3}([\-+][0-9A-Za-z.-]+)?$`)
	validAgentOS      = map[string]bool{"linux": true, "windows": true, "darwin": true}
	validAgentArch    = map[string]bool{"amd64": true, "arm64": true}
)

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

	checkingDeviceID := ""
	if deviceID, ok := resolveAPIKey(ctx, r.Header.Get("X-API-Key")); ok {
		checkingDeviceID = deviceID
		update := bson.M{"$set": bson.M{
			"agent_version":       currentVersion,
			"agent_os":            agentOS,
			"agent_arch":          agentArch,
			"agent_last_checked":  time.Now(),
			"agent_update_status": "checking",
		}}
		if _, err := mongoClient.Database(dbName).Collection("devices").UpdateOne(ctx, bson.M{"device_id": deviceID}, update); err != nil {
			log.Printf("Agent version update failed for %s: %v", deviceID, err)
		}
	}

	// Find the latest release for this OS/arch, sorted by published_at desc.
	coll := mongoClient.Database(dbName).Collection("agent_releases")
	opts := options.FindOne().SetSort(bson.D{{Key: "published_at", Value: -1}})
	filter := bson.M{"os": agentOS, "arch": agentArch}

	var latest AgentRelease
	if err := coll.FindOne(ctx, filter, opts).Decode(&latest); err != nil {
		// No releases published yet — agent is up to date.
		log.Printf("Update check: device=%s %s/%s current=%s latest=none update=false", checkingDeviceID, agentOS, agentArch, currentVersion)
		markAgentUpdateStatus(ctx, r, "up_to_date")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"update_available": false})
		return
	}

	if latest.Version == currentVersion {
		log.Printf("Update check: device=%s %s/%s current=%s latest=%s update=false", checkingDeviceID, agentOS, agentArch, currentVersion, latest.Version)
		markAgentUpdateStatus(ctx, r, "up_to_date")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"update_available": false})
		return
	}

	log.Printf("Update available for %s/%s: %s → %s", agentOS, agentArch, currentVersion, latest.Version)
	markAgentUpdateStatus(ctx, r, "update_available", latest.Version)
	downloadURL := agentReleaseDownloadURL(ctx, latest)
	log.Printf("Update check: device=%s %s/%s current=%s latest=%s update=true", checkingDeviceID, agentOS, agentArch, currentVersion, latest.Version)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"update_available": true,
		"version":          latest.Version,
		"download_url":     downloadURL,
		"checksum_sha256":  latest.Checksum,
	})
}

func markAgentUpdateStatus(ctx context.Context, r *http.Request, status string, targetVersion ...string) {
	deviceID, ok := resolveAPIKey(ctx, r.Header.Get("X-API-Key"))
	if !ok {
		return
	}
	set := bson.M{"agent_update_status": status, "agent_last_checked": time.Now()}
	unset := bson.M{}
	if len(targetVersion) > 0 && targetVersion[0] != "" {
		set["agent_target_version"] = targetVersion[0]
	} else if status == "up_to_date" {
		unset["agent_target_version"] = ""
		unset["agent_update_requested_at"] = ""
	}
	update := bson.M{"$set": set}
	if len(unset) > 0 {
		update["$unset"] = unset
	}
	if _, err := mongoClient.Database(dbName).Collection("devices").UpdateOne(ctx, bson.M{"device_id": deviceID}, update); err != nil {
		log.Printf("Agent update status failed for %s: %v", deviceID, err)
	}
}

func compareAgentVersions(a, b string) int {
	ap := agentVersionParts(a)
	bp := agentVersionParts(b)
	maxLen := len(ap)
	if len(bp) > maxLen {
		maxLen = len(bp)
	}
	for i := 0; i < maxLen; i++ {
		av, bv := 0, 0
		if i < len(ap) {
			av = ap[i]
		}
		if i < len(bp) {
			bv = bp[i]
		}
		if av > bv {
			return 1
		}
		if av < bv {
			return -1
		}
	}
	return strings.Compare(a, b)
}

func agentVersionParts(v string) []int {
	base := strings.FieldsFunc(v, func(r rune) bool {
		return r == '-' || r == '+'
	})
	if len(base) == 0 {
		return nil
	}
	rawParts := strings.Split(base[0], ".")
	parts := make([]int, 0, len(rawParts))
	for _, raw := range rawParts {
		n, err := strconv.Atoi(raw)
		if err != nil {
			break
		}
		parts = append(parts, n)
	}
	return parts
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

// GET /update/releases
//
// Returns the latest published agent release for each OS/arch pair.
func releaseListHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	coll := mongoClient.Database(dbName).Collection("agent_releases")
	opts := options.Find().SetSort(bson.D{
		{Key: "os", Value: 1},
		{Key: "arch", Value: 1},
		{Key: "published_at", Value: -1},
	})
	cur, err := coll.Find(ctx, bson.M{}, opts)
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer cur.Close(ctx)

	latestByTarget := map[string]AgentRelease{}
	allReleases := []AgentRelease{}
	for cur.Next(ctx) {
		var rel AgentRelease
		if err := cur.Decode(&rel); err != nil {
			continue
		}
		allReleases = append(allReleases, rel)
		key := rel.OS + "/" + rel.Arch
		if _, exists := latestByTarget[key]; !exists {
			latestByTarget[key] = rel
		}
	}
	if err := cur.Err(); err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	releases := make([]AgentRelease, 0, len(latestByTarget))
	for _, rel := range latestByTarget {
		releases = append(releases, rel)
	}
	sort.Slice(releases, func(i, j int) bool {
		if releases[i].OS == releases[j].OS {
			return releases[i].Arch < releases[j].Arch
		}
		return releases[i].OS < releases[j].OS
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"releases": releases, "all_releases": allReleases})
}

// POST /update/release/activate
//
// Makes a previously published release active by copying it with a fresh
// published_at timestamp. Agents use the newest release per OS/arch.
func releaseActivateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		OS      string `json:"os"`
		Arch    string `json:"arch"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad JSON", http.StatusBadRequest)
		return
	}
	req.OS = strings.TrimSpace(req.OS)
	req.Arch = strings.TrimSpace(req.Arch)
	req.Version = strings.TrimSpace(req.Version)
	if !validAgentOS[req.OS] || !validAgentArch[req.Arch] || req.Version == "" {
		http.Error(w, "os, arch and version are required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	coll := mongoClient.Database(dbName).Collection("agent_releases")
	var rel AgentRelease
	filter := bson.M{"os": req.OS, "arch": req.Arch, "version": req.Version}
	opts := options.FindOne().SetSort(bson.D{{Key: "published_at", Value: -1}})
	if err := coll.FindOne(ctx, filter, opts).Decode(&rel); err != nil {
		http.Error(w, "Release not found", http.StatusNotFound)
		return
	}
	rel.PublishedAt = time.Now()
	if _, err := coll.InsertOne(ctx, rel); err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rel)
}

// POST /update/release/rollout-latest
//
// Makes the highest published version active for every OS/arch target and marks
// older matching devices as waiting for update. Agents install it on their next
// update check.
func releaseRolloutLatestHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	db := mongoClient.Database(dbName)
	coll := db.Collection("agent_releases")
	cur, err := coll.Find(ctx, bson.M{})
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer cur.Close(ctx)

	latestByTarget := map[string]AgentRelease{}
	for cur.Next(ctx) {
		var rel AgentRelease
		if err := cur.Decode(&rel); err != nil {
			continue
		}
		key := rel.OS + "/" + rel.Arch
		existing, ok := latestByTarget[key]
		if !ok || compareAgentVersions(rel.Version, existing.Version) > 0 ||
			(compareAgentVersions(rel.Version, existing.Version) == 0 && rel.PublishedAt.After(existing.PublishedAt)) {
			latestByTarget[key] = rel
		}
	}
	if err := cur.Err(); err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	if len(latestByTarget) == 0 {
		http.Error(w, "No agent releases are available", http.StatusNotFound)
		return
	}

	now := time.Now()
	activated := make([]AgentRelease, 0, len(latestByTarget))
	devicesMarked := int64(0)
	for _, rel := range latestByTarget {
		rel.PublishedAt = now
		if _, err := coll.InsertOne(ctx, rel); err != nil {
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}
		activated = append(activated, rel)
		log.Printf("Agent rollout: activated %s for %s/%s", rel.Version, rel.OS, rel.Arch)
		res, err := db.Collection("devices").UpdateMany(ctx, bson.M{
			"agent_os":      rel.OS,
			"agent_arch":    rel.Arch,
			"agent_version": bson.M{"$ne": rel.Version},
		}, bson.M{"$set": bson.M{
			"agent_update_status":       "update_requested",
			"agent_update_requested_at": now,
			"agent_target_version":      rel.Version,
		}})
		if err == nil {
			devicesMarked += res.ModifiedCount
			log.Printf("Agent rollout: marked %d device(s) for %s/%s -> %s", res.ModifiedCount, rel.OS, rel.Arch, rel.Version)
		} else {
			log.Printf("Rollout device mark failed for %s/%s: %v", rel.OS, rel.Arch, err)
		}
	}
	sort.Slice(activated, func(i, j int) bool {
		if activated[i].OS == activated[j].OS {
			return activated[i].Arch < activated[j].Arch
		}
		return activated[i].OS < activated[j].OS
	})

	log.Printf("Agent rollout: latest rollout requested, activated_targets=%d devices_marked=%d", len(activated), devicesMarked)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"activated":      activated,
		"devices_marked": devicesMarked,
	})
}

func agentBuildsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		listAgentBuildJobs(w, r)
	case http.MethodPost:
		createAgentBuildJob(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func listAgentBuildJobs(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	coll := mongoClient.Database(dbName).Collection("agent_build_jobs")
	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetLimit(20)
	cur, err := coll.Find(ctx, bson.M{}, opts)
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer cur.Close(ctx)

	jobs := []AgentBuildJob{}
	for cur.Next(ctx) {
		var job AgentBuildJob
		if err := cur.Decode(&job); err == nil {
			jobs = append(jobs, job)
		}
	}
	if err := cur.Err(); err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"jobs": jobs})
}

func createAgentBuildJob(w http.ResponseWriter, r *http.Request) {
	var req AgentBuildRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad JSON", http.StatusBadRequest)
		return
	}
	if req.APIURL == "" {
		req.APIURL = publicAPIURLFromRequest(r)
	}
	if err := validateAgentBuildRequest(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := validateAgentReleaseS3Config(); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	now := time.Now()
	job := AgentBuildJob{
		ID:        primitive.NewObjectID(),
		Version:   req.Version,
		APIURL:    req.APIURL,
		Platforms: req.Platforms,
		Archs:     req.Archs,
		Status:    "queued",
		Logs:      []string{"Queued build"},
		Artifacts: []AgentBuildArtifact{},
		CreatedAt: now,
		UpdatedAt: now,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	coll := mongoClient.Database(dbName).Collection("agent_build_jobs")
	if _, err := coll.InsertOne(ctx, job); err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	go runAgentBuildJob(job.ID)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(job)
}

func runAgentBuildJob(jobID primitive.ObjectID) {
	agentBuildMu.Lock()
	defer agentBuildMu.Unlock()

	ctx := context.Background()
	coll := mongoClient.Database(dbName).Collection("agent_build_jobs")

	var job AgentBuildJob
	if err := coll.FindOne(ctx, bson.M{"_id": jobID}).Decode(&job); err != nil {
		log.Printf("Agent build job %s missing: %v", jobID.Hex(), err)
		return
	}

	started := time.Now()
	setAgentBuildJob(ctx, jobID, bson.M{"status": "building", "started_at": started, "updated_at": started})
	appendAgentBuildLog(ctx, jobID, "Build started")

	distDir, err := os.MkdirTemp("", "devicepulse-agent-build-*")
	if err != nil {
		failAgentBuildJob(ctx, jobID, fmt.Sprintf("create temp dir: %v", err))
		return
	}
	defer os.RemoveAll(distDir)

	buildRoot := strings.TrimSpace(os.Getenv("AGENT_BUILD_ROOT"))
	if buildRoot == "" {
		buildRoot = "."
	}
	buildScript := filepath.Join(buildRoot, "packaging", "build.sh")
	if _, err := os.Stat(buildScript); err != nil {
		failAgentBuildJob(ctx, jobID, fmt.Sprintf("build script not found at %s", buildScript))
		return
	}

	for _, platform := range job.Platforms {
		appendAgentBuildLog(ctx, jobID, fmt.Sprintf("Building %s targets", platform))
		cmdCtx, cancel := context.WithTimeout(ctx, 20*time.Minute)
		cmd := exec.CommandContext(cmdCtx, "bash", buildScript, "--version", job.Version, "--api-url", job.APIURL, "--platform", platform)
		cmd.Dir = buildRoot
		cmd.Env = append(os.Environ(),
			"DEVICEPULSE_DIST_DIR="+distDir,
			"GOCACHE=/tmp/devicepulse-go-build-cache",
		)
		output, err := cmd.CombinedOutput()
		cancel()
		if len(output) > 0 {
			appendAgentBuildLog(ctx, jobID, trimBuildOutput(string(output)))
		}
		if err != nil {
			failAgentBuildJob(ctx, jobID, fmt.Sprintf("build %s failed: %v", platform, err))
			return
		}
		if platform == "linux" {
			for _, arch := range job.Archs {
				if err := buildLinuxDebPackage(ctx, buildRoot, distDir, job.Version, arch); err != nil {
					failAgentBuildJob(ctx, jobID, err.Error())
					return
				}
				appendAgentBuildLog(ctx, jobID, fmt.Sprintf("Built Linux .deb package for %s", arch))
			}
		}
	}

	setAgentBuildJob(ctx, jobID, bson.M{"status": "uploading", "updated_at": time.Now()})
	appendAgentBuildLog(ctx, jobID, "Uploading artifacts to S3")

	artifacts, err := uploadAgentBuildArtifacts(ctx, job, distDir)
	if err != nil {
		failAgentBuildJob(ctx, jobID, err.Error())
		return
	}

	setAgentBuildJob(ctx, jobID, bson.M{"status": "publishing", "artifacts": artifacts, "updated_at": time.Now()})
	if err := publishAgentArtifacts(ctx, job.Version, artifacts); err != nil {
		failAgentBuildJob(ctx, jobID, err.Error())
		return
	}

	finished := time.Now()
	setAgentBuildJob(ctx, jobID, bson.M{"status": "published", "finished_at": finished, "updated_at": finished})
	appendAgentBuildLog(ctx, jobID, "Published release metadata")
}

func validateAgentBuildRequest(req *AgentBuildRequest) error {
	req.Version = strings.TrimSpace(req.Version)
	req.APIURL = strings.TrimSpace(req.APIURL)
	if !agentVersionRegex.MatchString(req.Version) {
		return fmt.Errorf("version must look like 1.2.3")
	}
	parsed, err := url.Parse(req.APIURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return fmt.Errorf("api_url must be a valid http(s) URL")
	}
	req.APIURL = strings.TrimRight(req.APIURL, "/")
	req.Platforms = uniqueStrings(req.Platforms)
	req.Archs = uniqueStrings(req.Archs)
	if len(req.Platforms) == 0 {
		return fmt.Errorf("choose at least one platform")
	}
	if len(req.Archs) == 0 {
		return fmt.Errorf("choose at least one architecture")
	}
	for _, platform := range req.Platforms {
		if !validAgentOS[platform] {
			return fmt.Errorf("unsupported platform: %s", platform)
		}
	}
	for _, arch := range req.Archs {
		if !validAgentArch[arch] {
			return fmt.Errorf("unsupported architecture: %s", arch)
		}
	}
	for _, platform := range req.Platforms {
		for _, arch := range req.Archs {
			if artifactSuffix(platform, arch) == "" {
				return fmt.Errorf("unsupported build target: %s/%s", platform, arch)
			}
		}
	}
	return nil
}

func validateAgentReleaseS3Config() error {
	if agentReleaseBucketName() == "" {
		return fmt.Errorf("agent release S3 bucket is not configured")
	}
	return nil
}

func buildLinuxDebPackage(ctx context.Context, buildRoot, distDir, version, arch string) error {
	script := filepath.Join(buildRoot, "packaging", "linux", "build_deb.sh")
	if _, err := os.Stat(script); err != nil {
		return fmt.Errorf("deb package script not found at %s", script)
	}
	cmdCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, "bash", script, version, distDir, arch)
	cmd.Dir = buildRoot
	cmd.Env = append(os.Environ(), "GOCACHE=/tmp/devicepulse-go-build-cache")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("deb package %s failed: %v: %s", arch, err, trimBuildOutput(string(output)))
	}
	return nil
}

func uploadAgentBuildArtifacts(ctx context.Context, job AgentBuildJob, distDir string) ([]AgentBuildArtifact, error) {
	client, bucket, prefix, publicBase, err := agentReleaseS3Client(ctx)
	if err != nil {
		return nil, err
	}

	artifacts := []AgentBuildArtifact{}
	for _, platform := range job.Platforms {
		for _, arch := range job.Archs {
			suffix := artifactSuffix(platform, arch)
			if suffix == "" {
				continue
			}
			fileName := fmt.Sprintf("devicepulse-agent-%s-%s", job.Version, suffix)
			artifact, err := uploadAgentArtifactFile(ctx, client, bucket, prefix, publicBase, distDir, job.Version, platform, arch, "binary", fileName, true)
			if err != nil {
				return nil, err
			}
			artifacts = append(artifacts, artifact)

			for _, packageName := range packageArtifactNames(job.Version, platform, arch) {
				artifact, err := uploadAgentArtifactFile(ctx, client, bucket, prefix, publicBase, distDir, job.Version, platform, arch, "package", packageName, false)
				if err != nil {
					return nil, err
				}
				if artifact.FileName != "" {
					artifacts = append(artifacts, artifact)
				}
			}
		}
	}
	if len(artifacts) == 0 {
		return nil, fmt.Errorf("no artifacts were produced")
	}
	return artifacts, nil
}

func uploadAgentArtifactFile(ctx context.Context, client *s3.Client, bucket, prefix, publicBase, distDir, version, platform, arch, kind, fileName string, required bool) (AgentBuildArtifact, error) {
	path := filepath.Join(distDir, fileName)
	info, err := os.Stat(path)
	if err != nil {
		if required {
			return AgentBuildArtifact{}, fmt.Errorf("expected artifact missing: %s", fileName)
		}
		return AgentBuildArtifact{}, nil
	}
	checksum, err := sha256File(path)
	if err != nil {
		return AgentBuildArtifact{}, fmt.Errorf("checksum %s: %w", fileName, err)
	}
	key := strings.Trim(strings.TrimSpace(prefix), "/")
	if key != "" {
		key += "/"
	}
	key += fmt.Sprintf("%s/%s/%s/%s", version, platform, arch, fileName)

	f, err := os.Open(path)
	if err != nil {
		return AgentBuildArtifact{}, fmt.Errorf("open %s: %w", fileName, err)
	}
	_, putErr := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(bucket),
		Key:           aws.String(key),
		Body:          f,
		ContentLength: aws.Int64(info.Size()),
		ContentType:   aws.String(agentArtifactContentType(fileName)),
	})
	f.Close()
	if putErr != nil {
		return AgentBuildArtifact{}, fmt.Errorf("upload %s: %w", fileName, putErr)
	}

	return AgentBuildArtifact{
		OS:          platform,
		Arch:        arch,
		Kind:        kind,
		FileName:    fileName,
		S3Key:       key,
		DownloadURL: agentReleasePublicURL(publicBase, bucket, key),
		Checksum:    checksum,
		SizeBytes:   info.Size(),
	}, nil
}

func packageArtifactNames(version, platform, arch string) []string {
	if platform != "linux" {
		return nil
	}
	return []string{fmt.Sprintf("devicepulse-agent_%s_%s.deb", version, arch)}
}

func agentArtifactContentType(fileName string) string {
	switch {
	case strings.HasSuffix(fileName, ".deb"):
		return "application/vnd.debian.binary-package"
	case strings.HasSuffix(fileName, ".rpm"):
		return "application/x-rpm"
	case strings.HasSuffix(fileName, ".pkg"):
		return "application/octet-stream"
	case strings.HasSuffix(fileName, ".msi"):
		return "application/octet-stream"
	default:
		return "application/octet-stream"
	}
}

func publishAgentArtifacts(ctx context.Context, version string, artifacts []AgentBuildArtifact) error {
	coll := mongoClient.Database(dbName).Collection("agent_releases")
	now := time.Now()
	docs := make([]interface{}, 0, len(artifacts))
	for _, artifact := range artifacts {
		if artifact.Kind != "" && artifact.Kind != "binary" {
			continue
		}
		docs = append(docs, AgentRelease{
			Version:     version,
			OS:          artifact.OS,
			Arch:        artifact.Arch,
			DownloadURL: artifact.DownloadURL,
			S3Key:       artifact.S3Key,
			Checksum:    artifact.Checksum,
			PublishedAt: now,
		})
	}
	if len(docs) == 0 {
		return fmt.Errorf("no binary artifacts were produced")
	}
	if _, err := coll.InsertMany(ctx, docs); err != nil {
		return fmt.Errorf("publish release metadata: %w", err)
	}
	return nil
}

func agentReleaseS3Client(ctx context.Context) (*s3.Client, string, string, string, error) {
	bucket := agentReleaseBucketName()
	if bucket == "" {
		return nil, "", "", "", fmt.Errorf("agent release S3 bucket is not configured")
	}
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, "", "", "", fmt.Errorf("AWS config error: %w", err)
	}
	endpoint := strings.TrimSpace(os.Getenv("AGENT_RELEASE_S3_ENDPOINT"))
	if endpoint == "" {
		endpoint = strings.TrimSpace(os.Getenv("S3_ENDPOINT"))
	}
	usePathStyle := strings.EqualFold(os.Getenv("AGENT_RELEASE_S3_PATH_STYLE"), "true") ||
		strings.EqualFold(os.Getenv("S3_PATH_STYLE"), "true") ||
		endpoint != ""
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
		o.UsePathStyle = usePathStyle
	})
	prefix := strings.Trim(strings.TrimSpace(os.Getenv("AGENT_RELEASE_S3_PREFIX")), "/")
	if prefix == "" {
		prefix = "agent-releases"
	}
	publicBase := strings.TrimRight(strings.TrimSpace(os.Getenv("AGENT_RELEASE_PUBLIC_BASE_URL")), "/")
	return client, bucket, prefix, publicBase, nil
}

func agentReleaseDownloadURL(ctx context.Context, rel AgentRelease) string {
	s3Key := agentReleaseS3Key(rel)
	if s3Key == "" || strings.EqualFold(os.Getenv("AGENT_RELEASE_PRESIGN_DOWNLOADS"), "false") {
		return rel.DownloadURL
	}
	client, bucket, _, _, err := agentReleaseS3Client(ctx)
	if err != nil {
		log.Printf("Agent release presign skipped: %v", err)
		return rel.DownloadURL
	}
	expires := 24 * time.Hour
	if raw := strings.TrimSpace(os.Getenv("AGENT_RELEASE_PRESIGN_TTL")); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			expires = parsed
		}
	}
	presigner := s3.NewPresignClient(client)
	signed, err := presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(s3Key),
	}, func(o *s3.PresignOptions) {
		o.Expires = expires
	})
	if err != nil {
		log.Printf("Agent release presign failed for %s: %v", s3Key, err)
		return rel.DownloadURL
	}
	return signed.URL
}

func agentReleaseS3Key(rel AgentRelease) string {
	if rel.S3Key != "" {
		return rel.S3Key
	}
	u, err := url.Parse(rel.DownloadURL)
	if err != nil {
		return ""
	}
	key, err := url.PathUnescape(strings.TrimPrefix(u.Path, "/"))
	if err != nil {
		return ""
	}
	prefix := strings.Trim(strings.TrimSpace(os.Getenv("AGENT_RELEASE_S3_PREFIX")), "/")
	if prefix == "" {
		prefix = "agent-releases"
	}
	duplicatePrefix := prefix + "/" + prefix + "/"
	if strings.HasPrefix(key, duplicatePrefix) {
		key = strings.TrimPrefix(key, prefix+"/")
	}
	return key
}

func agentReleaseBucketName() string {
	for _, key := range []string{"AGENT_RELEASE_S3_BUCKET", "AGENT_RELEASE_BUCKET", "S3_BUCKET"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func artifactSuffix(platform, arch string) string {
	switch platform + "/" + arch {
	case "darwin/amd64":
		return "darwin-amd64"
	case "darwin/arm64":
		return "darwin-arm64"
	case "linux/amd64":
		return "linux-amd64"
	case "linux/arm64":
		return "linux-arm64"
	case "windows/amd64":
		return "windows-amd64.exe"
	case "windows/arm64":
		return "windows-arm64.exe"
	default:
		return ""
	}
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func agentReleasePublicURL(publicBase, bucket, key string) string {
	escapedKey := strings.ReplaceAll(url.PathEscape(key), "%2F", "/")
	if publicBase != "" {
		return publicBase + "/" + escapedKey
	}
	return fmt.Sprintf("https://%s.s3.amazonaws.com/%s", bucket, escapedKey)
}

func publicAPIURLFromRequest(r *http.Request) string {
	if configured := strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_API_URL")), "/"); configured != "" {
		return configured
	}
	proto := r.Header.Get("X-Forwarded-Proto")
	if proto == "" {
		if r.TLS != nil {
			proto = "https"
		} else {
			proto = "http"
		}
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	return strings.TrimRight(proto+"://"+host, "/")
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func setAgentBuildJob(ctx context.Context, id primitive.ObjectID, fields bson.M) {
	_, err := mongoClient.Database(dbName).Collection("agent_build_jobs").UpdateByID(ctx, id, bson.M{"$set": fields})
	if err != nil {
		log.Printf("Agent build job update failed: %v", err)
	}
}

func appendAgentBuildLog(ctx context.Context, id primitive.ObjectID, msg string) {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return
	}
	_, err := mongoClient.Database(dbName).Collection("agent_build_jobs").UpdateByID(ctx, id, bson.M{
		"$set":  bson.M{"updated_at": time.Now()},
		"$push": bson.M{"logs": msg},
	})
	if err != nil {
		log.Printf("Agent build log update failed: %v", err)
	}
}

func failAgentBuildJob(ctx context.Context, id primitive.ObjectID, msg string) {
	finished := time.Now()
	setAgentBuildJob(ctx, id, bson.M{"status": "failed", "error": msg, "finished_at": finished, "updated_at": finished})
	appendAgentBuildLog(ctx, id, "Failed: "+msg)
}

func trimBuildOutput(output string) string {
	output = strings.TrimSpace(output)
	if len(output) <= 4000 {
		return output
	}
	return output[len(output)-4000:]
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
	cleanupBootstrapLocks()

	telemetryStore = initTelemetryArchive(ctx)
	browserArchive = initBrowserHistoryArchive(ctx)
	activityStore = initDailyActivityArchive(ctx)

	// Build focus cache from existing telemetry data
	go buildFocusCacheFromMongo()

	apiMux := http.NewServeMux()
	registerRoutes(apiMux)

	rootMux := http.NewServeMux()
	rootMux.Handle("/api/", http.StripPrefix("/api", apiMux))
	rootMux.Handle("/api", http.RedirectHandler("/api/health", http.StatusTemporaryRedirect))
	rootMux.Handle("/", apiMux)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8000"
	}
	log.Printf("DevicePulse API listening on :%s", port)
	if err := http.ListenAndServe(":"+port, rootMux); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

func registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/health", corsMiddleware(healthHandler))
	mux.HandleFunc("/devices/register", corsMiddleware(registerHandler))
	mux.HandleFunc("/auth/bootstrap", corsMiddleware(authBootstrapHandler))
	mux.HandleFunc("/auth/register", corsMiddleware(authRegisterHandler))
	mux.HandleFunc("/auth/login", corsMiddleware(authLoginHandler))
	mux.HandleFunc("/auth/logout", corsMiddleware(authLogoutHandler))
	mux.HandleFunc("/auth/me", corsMiddleware(requireAuth(authMeHandler)))
	mux.HandleFunc("/users/", corsMiddleware(requireRole(RoleAdmin, userDetailHandler)))
	mux.HandleFunc("/users", corsMiddleware(requireRole(RoleAdmin, usersHandler)))
	mux.HandleFunc("/devices/", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/browser-history") {
			requireRole(RoleViewer, browserHistoryHandler)(w, r)
		} else if strings.HasSuffix(r.URL.Path, "/history") {
			requireRole(RoleViewer, historyHandler)(w, r)
		} else if strings.HasSuffix(r.URL.Path, "/name") {
			requireRole(RoleAdmin, deviceNameHandler)(w, r)
		} else if strings.HasSuffix(r.URL.Path, "/ping") {
			requireRole(RoleAdmin, devicePingHandler)(w, r)
		} else if strings.HasSuffix(r.URL.Path, "/app-usage") {
			requireRole(RoleViewer, deviceAppUsageHandler)(w, r)
		} else if r.Method == http.MethodDelete {
			requireRole(RoleAdmin, deviceDeleteHandler)(w, r)
		} else {
			http.NotFound(w, r)
		}
	}))
	mux.HandleFunc("/devices", corsMiddleware(requireRole(RoleViewer, devicesHandler)))
	mux.HandleFunc("/ingest", corsMiddleware(ingestHandler))
	// Dashboard policy access is role-protected. Agents receive policy through
	// authenticated device flows, not these browser endpoints.
	mux.HandleFunc("/policy", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			requireRole(RoleManager, policyHandler)(w, r)
		} else if r.Header.Get("X-API-Key") != "" {
			requireAgent(policyHandler)(w, r)
		} else {
			requireRole(RoleViewer, policyHandler)(w, r)
		}
	}))
	mux.HandleFunc("/focus/", corsMiddleware(requireRole(RoleViewer, focusHandler)))

	mux.HandleFunc("/update/check", corsMiddleware(requireAgent(updateCheckHandler)))
	mux.HandleFunc("/update/releases", corsMiddleware(requireRole(RoleAdmin, releaseListHandler)))
	mux.HandleFunc("/update/release/rollout-latest", corsMiddleware(requireRole(RoleAdmin, releaseRolloutLatestHandler)))
	mux.HandleFunc("/update/release/activate", corsMiddleware(requireRole(RoleAdmin, releaseActivateHandler)))
	mux.HandleFunc("/update/release", corsMiddleware(requireRole(RoleAdmin, releasePublishHandler)))
	mux.HandleFunc("/update/builds", corsMiddleware(requireRole(RoleAdmin, agentBuildsHandler)))
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

	revocationIdx := []mongo.IndexModel{
		{Keys: bson.D{{Key: "device_id", Value: 1}}, Options: options.Index().SetSparse(true)},
		{Keys: bson.D{{Key: "hardware_uuid", Value: 1}}, Options: options.Index().SetSparse(true)},
		{Keys: bson.D{{Key: "mac_address", Value: 1}}, Options: options.Index().SetSparse(true)},
		{Keys: bson.D{{Key: "hostname", Value: 1}}, Options: options.Index().SetSparse(true)},
		{Keys: bson.D{{Key: "revoked_at", Value: -1}}},
	}
	db.Collection("device_revocations").Indexes().CreateMany(ctx, revocationIdx)

	// telemetry: index on device_id + _id for fast history queries
	db.Collection("telemetry").Indexes().CreateOne(ctx,
		mongo.IndexModel{Keys: bson.D{{Key: "device_id", Value: 1}, {Key: "_id", Value: -1}}})
	db.Collection("telemetry").Indexes().CreateOne(ctx,
		mongo.IndexModel{
			Keys:    bson.D{{Key: "telemetry_expires_at", Value: 1}},
			Options: options.Index().SetExpireAfterSeconds(0).SetSparse(true),
		})

	activityIdx := []mongo.IndexModel{
		{
			Keys: bson.D{
				{Key: "device_id", Value: 1},
				{Key: "username", Value: 1},
				{Key: "date", Value: 1},
			},
			Options: options.Index().SetUnique(true),
		},
		{Keys: bson.D{{Key: "device_id", Value: 1}, {Key: "date", Value: -1}}},
	}
	db.Collection("app_usage_daily").Indexes().CreateMany(ctx, activityIdx)

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
