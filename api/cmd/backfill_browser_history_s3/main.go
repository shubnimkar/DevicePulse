package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/joho/godotenv"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const dbName = "devicepulse"

type archiveResult struct {
	Bucket     string   `json:"bucket" bson:"bucket"`
	Key        string   `json:"key,omitempty" bson:"key,omitempty"`
	Keys       []string `json:"keys" bson:"keys"`
	EntryCount int      `json:"entry_count" bson:"entry_count"`
}

type archiveObject struct {
	DeviceID    string        `json:"device_id"`
	TelemetryID string        `json:"telemetry_id"`
	Timestamp   interface{}   `json:"timestamp"`
	IngestedAt  interface{}   `json:"ingested_at"`
	SyncType    string        `json:"sync_type"`
	EntryCount  int           `json:"entry_count"`
	Entries     []interface{} `json:"entries"`
}

func main() {
	limit := flag.Int64("limit", 0, "maximum telemetry documents to migrate; 0 means all")
	dryRun := flag.Bool("dry-run", false, "count and measure without uploading or updating")
	flag.Parse()

	_ = godotenv.Load(".env")

	mongoURI := strings.TrimSpace(os.Getenv("MONGO_URI"))
	bucket := strings.TrimSpace(os.Getenv("BROWSER_HISTORY_S3_BUCKET"))
	if bucket == "" {
		bucket = strings.TrimSpace(os.Getenv("S3_BUCKET"))
	}
	if mongoURI == "" || bucket == "" {
		log.Fatal("MONGO_URI and BROWSER_HISTORY_S3_BUCKET/S3_BUCKET are required")
	}
	prefix := strings.Trim(strings.TrimSpace(os.Getenv("BROWSER_HISTORY_S3_PREFIX")), "/")
	if prefix == "" {
		prefix = "browser-history"
	}

	ctx := context.Background()
	log.Printf("starting browser-history backfill dry_run=%v bucket=%s prefix=%s", *dryRun, bucket, prefix)

	connectCtx, connectCancel := context.WithTimeout(ctx, 15*time.Second)
	defer connectCancel()
	mongoClient, err := mongo.Connect(connectCtx, options.Client().
		ApplyURI(mongoURI).
		SetServerSelectionTimeout(15*time.Second))
	if err != nil {
		log.Fatalf("mongo connect: %v", err)
	}
	defer mongoClient.Disconnect(ctx)
	if err := mongoClient.Ping(connectCtx, nil); err != nil {
		log.Fatalf("mongo ping: %v", err)
	}
	log.Printf("connected to MongoDB")

	var s3Client *s3.Client
	if !*dryRun {
		cfg, err := config.LoadDefaultConfig(ctx)
		if err != nil {
			log.Fatalf("aws config: %v", err)
		}
		endpoint := strings.TrimSpace(os.Getenv("BROWSER_HISTORY_S3_ENDPOINT"))
		usePathStyle := strings.EqualFold(os.Getenv("BROWSER_HISTORY_S3_PATH_STYLE"), "true") || endpoint != ""
		s3Client = s3.NewFromConfig(cfg, func(o *s3.Options) {
			if endpoint != "" {
				o.BaseEndpoint = aws.String(endpoint)
			}
			o.UsePathStyle = usePathStyle
		})
	}

	coll := mongoClient.Database(dbName).Collection("telemetry")
	filter := bson.M{}
	opts := options.Find().
		SetBatchSize(25).
		SetProjection(bson.M{
			"device_id":           1,
			"timestamp":           1,
			"ingested_at":         1,
			"data.BrowserHistory": 1,
		})
	if *limit > 0 {
		opts.SetLimit(*limit)
	}

	findCtx, findCancel := context.WithTimeout(ctx, 30*time.Minute)
	defer findCancel()
	cursor, err := coll.Find(findCtx, filter, opts)
	if err != nil {
		log.Fatalf("find telemetry: %v", err)
	}
	defer cursor.Close(ctx)

	var scanned, migrated int64
	var archivedEntries int
	var removedBytes int
	for cursor.Next(ctx) {
		var doc bson.M
		if err := cursor.Decode(&doc); err != nil {
			log.Fatalf("decode telemetry: %v", err)
		}
		scanned++
		if scanned%25 == 0 {
			log.Printf("scanned=%d migrated=%d entries=%d", scanned, migrated, archivedEntries)
		}

		id, _ := doc["_id"].(primitive.ObjectID)
		deviceID, _ := doc["device_id"].(string)
		if deviceID == "" || id.IsZero() {
			continue
		}

		browserPayload, entries := extractBrowserHistoryEntries(doc)
		if len(entries) == 0 {
			continue
		}
		beforeSize, _ := bson.Marshal(doc)
		removedBytes += browserArraysSize(browserPayload)

		if *dryRun {
			archivedEntries += len(entries)
			migrated++
			log.Printf("dry-run telemetry=%s entries=%d approx_doc_kb=%.1f", id.Hex(), len(entries), float64(len(beforeSize))/1024)
			continue
		}

		result, err := archive(ctx, s3Client, bucket, prefix, id, deviceID, doc, browserPayload, entries)
		if err != nil {
			log.Fatalf("archive telemetry=%s: %v", id.Hex(), err)
		}

		update := bson.M{
			"$set": bson.M{
				"browser_history_archive":     result,
				"data.BrowserHistory.archive": result,
			},
			"$unset": bson.M{
				"data.BrowserHistory.new_history_entries": "",
				"data.BrowserHistory.top_recent_urls":     "",
			},
		}
		if _, err := coll.UpdateByID(ctx, id, update); err != nil {
			log.Fatalf("update telemetry=%s: %v", id.Hex(), err)
		}

		archivedEntries += len(entries)
		migrated++
		if migrated%100 == 0 {
			log.Printf("migrated=%d scanned=%d entries=%d", migrated, scanned, archivedEntries)
		}
	}
	if err := cursor.Err(); err != nil {
		log.Fatalf("cursor: %v", err)
	}

	if !*dryRun {
		devices := mongoClient.Database(dbName).Collection("devices")
		_, err := devices.UpdateMany(ctx, bson.M{}, bson.M{"$unset": bson.M{
			"data.BrowserHistory.new_history_entries": "",
			"data.BrowserHistory.top_recent_urls":     "",
		}})
		if err != nil {
			log.Fatalf("cleanup devices: %v", err)
		}
	}

	log.Printf("done dry_run=%v scanned=%d migrated=%d archived_entries=%d approx_removed_mb=%.2f",
		*dryRun, scanned, migrated, archivedEntries, float64(removedBytes)/1024/1024)
}

func archive(ctx context.Context, client *s3.Client, bucket, prefix string, id primitive.ObjectID, deviceID string, doc bson.M, browserPayload map[string]interface{}, entries []interface{}) (*archiveResult, error) {
	syncType, _ := browserPayload["sync_type"].(string)
	eventTime := payloadTime(doc["timestamp"])
	groups := map[string][]interface{}{}
	for _, entry := range entries {
		day := browserHistoryEntryVisitTime(entry, eventTime).Format("2006-01-02")
		groups[day] = append(groups[day], entry)
	}

	result := &archiveResult{Bucket: bucket, EntryCount: len(entries)}
	for day, group := range groups {
		obj := archiveObject{
			DeviceID:    deviceID,
			TelemetryID: id.Hex(),
			Timestamp:   doc["timestamp"],
			IngestedAt:  doc["ingested_at"],
			SyncType:    syncType,
			EntryCount:  len(group),
			Entries:     group,
		}
		body, err := json.Marshal(obj)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(body)
		key := fmt.Sprintf("%s/device_id=%s/date=%s/backfill-%s-%x.json",
			prefix,
			safeS3PathSegment(deviceID),
			day,
			id.Hex(),
			sum[:8],
		)
		if _, err := client.PutObject(ctx, &s3.PutObjectInput{
			Bucket:      aws.String(bucket),
			Key:         aws.String(key),
			Body:        bytes.NewReader(body),
			ContentType: aws.String("application/json"),
		}); err != nil {
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

func extractBrowserHistoryEntries(payload map[string]interface{}) (map[string]interface{}, []interface{}) {
	data, ok := asMap(payload["data"])
	if !ok {
		return nil, nil
	}
	browserPayload, ok := asMap(data["BrowserHistory"])
	if !ok {
		return nil, nil
	}
	seen := map[string]struct{}{}
	entries := []interface{}{}
	appendEntries(&entries, seen, browserPayload["new_history_entries"])
	appendEntries(&entries, seen, browserPayload["top_recent_urls"])
	return browserPayload, entries
}

func asMap(v interface{}) (map[string]interface{}, bool) {
	switch m := v.(type) {
	case map[string]interface{}:
		return m, true
	case bson.M:
		return map[string]interface{}(m), true
	default:
		return nil, false
	}
}

func appendEntries(entries *[]interface{}, seen map[string]struct{}, raw interface{}) {
	items, ok := raw.(primitive.A)
	if !ok {
		if fallback, ok := raw.([]interface{}); ok {
			items = primitive.A(fallback)
		} else {
			return
		}
	}
	for _, entry := range items {
		if m, ok := asMap(entry); ok {
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

func browserArraysSize(browserPayload map[string]interface{}) int {
	size := 0
	for _, key := range []string{"new_history_entries", "top_recent_urls"} {
		if wrapped, err := bson.Marshal(bson.M{"v": browserPayload[key]}); err == nil {
			size += len(wrapped)
		}
	}
	return size
}

func payloadTime(v interface{}) time.Time {
	if s, ok := v.(string); ok {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t.UTC()
		}
	}
	if t, ok := v.(time.Time); ok {
		return t.UTC()
	}
	return time.Now().UTC()
}

func browserHistoryEntryVisitTime(entry interface{}, fallback time.Time) time.Time {
	nanos := browserHistoryVisitNanos(entry)
	if nanos <= 0 {
		return fallback.UTC()
	}
	return time.Unix(0, nanos).UTC()
}

func browserHistoryVisitNanos(entry interface{}) int64 {
	m, ok := entry.(map[string]interface{})
	if !ok {
		return 0
	}
	switch v := m["last_visit_time"].(type) {
	case int64:
		return v
	case int32:
		return int64(v)
	case int:
		return int64(v)
	case float64:
		return int64(v)
	case primitive.DateTime:
		return int64(v) * int64(time.Millisecond)
	default:
		return 0
	}
}

func browserHistoryDedupeKey(entry map[string]interface{}) string {
	rawURL, _ := entry["url"].(string)
	title, _ := entry["title"].(string)
	browser, _ := entry["browser"].(string)
	if rawURL == "" {
		return ""
	}
	identity := strings.TrimSpace(strings.ToLower(title))
	if identity == "" {
		identity = normalizedBrowserHistoryURL(rawURL)
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

func safeS3PathSegment(s string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", " ", "_", ":", "_")
	return replacer.Replace(s)
}
