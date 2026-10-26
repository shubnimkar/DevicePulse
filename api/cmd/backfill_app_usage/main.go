package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"math/big"
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

func main() {
	dryRun := flag.Bool("dry-run", false, "report affected rows without changing S3 or MongoDB")
	apply := flag.Bool("apply", false, "write normalized sessions to S3 and MongoDB")
	limit := flag.Int64("limit", 0, "maximum affected rows; 0 means all")
	tz := flag.String("tz", "", "IANA timezone of the devices' local day, e.g. Asia/Kolkata (required with --apply)")
	flag.Parse()
	if *dryRun == *apply {
		log.Fatal("specify exactly one of --dry-run or --apply")
	}
	// Live ingest buckets sessions by the device's LOCAL day, but archived
	// sessions are stored in UTC, so the zone cannot be recovered from the data.
	// Clipping to a UTC day would cut off hours of any non-UTC device's day.
	loc := time.UTC
	if *tz != "" {
		var err error
		if loc, err = time.LoadLocation(*tz); err != nil {
			log.Fatalf("invalid --tz %q: %v", *tz, err)
		}
	} else if *apply {
		log.Fatal("--apply requires --tz (the devices' local timezone, e.g. Asia/Kolkata)")
	} else {
		log.Printf("WARNING: no --tz given; dry-run assumes UTC days, which will misreport non-UTC devices")
	}
	_ = godotenv.Load(".env")

	mongoURI := strings.TrimSpace(os.Getenv("MONGO_URI"))
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
	if mongoURI == "" || bucket == "" {
		log.Fatal("MONGO_URI and ACTIVITY_S3_BUCKET/TELEMETRY_S3_BUCKET/BROWSER_HISTORY_S3_BUCKET/S3_BUCKET are required")
	}
	prefix := strings.Trim(strings.TrimSpace(os.Getenv("ACTIVITY_S3_PREFIX")), "/")
	if prefix == "" {
		prefix = "app-usage"
	}

	ctx := context.Background()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI).SetServerSelectionTimeout(15*time.Second))
	if err != nil {
		log.Fatal(err)
	}
	defer client.Disconnect(ctx)
	if err := client.Ping(ctx, nil); err != nil {
		log.Fatal(err)
	}

	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatal(err)
	}
	endpoint := strings.TrimSpace(os.Getenv("ACTIVITY_S3_ENDPOINT"))
	if endpoint == "" {
		endpoint = strings.TrimSpace(os.Getenv("TELEMETRY_S3_ENDPOINT"))
	}
	if endpoint == "" {
		endpoint = strings.TrimSpace(os.Getenv("S3_ENDPOINT"))
	}
	s3Client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
		o.UsePathStyle = endpoint != "" || strings.EqualFold(os.Getenv("ACTIVITY_S3_PATH_STYLE"), "true")
	})

	findOpts := options.Find().SetSort(bson.D{{Key: "total_seconds", Value: -1}})
	cursor, err := client.Database(dbName).Collection("app_usage_daily").Find(ctx, bson.M{"total_seconds": bson.M{"$gt": 86400}}, findOpts)
	if err != nil {
		log.Fatal(err)
	}
	defer cursor.Close(ctx)

	var scanned, repaired int64
	for cursor.Next(ctx) {
		var row bson.M
		if err := cursor.Decode(&row); err != nil {
			log.Fatal(err)
		}
		scanned++
		if *limit > 0 && repaired >= *limit {
			break
		}
		deviceID, _ := row["device_id"].(string)
		username, _ := row["username"].(string)
		date, _ := row["date"].(string)
		key := activityKey(prefix, deviceID, username, date)
		if rawKey, ok := row["archive_key"].(string); ok && rawKey != "" {
			key = rawKey
		}
		doc, etag, err := readObject(ctx, s3Client, bucket, key)
		if err != nil {
			log.Printf("skip device=%s user=%s date=%s key=%s: %v", deviceID, username, date, key, err)
			continue
		}
		sessions, _ := doc["sessions"].([]interface{})
		normalized := normalize(sessions, date, loc)
		apps, total, count := summarize(normalized)
		log.Printf("device=%s user=%s date=%s %.2fh -> %.2fh (%d sessions)", deviceID, username, date, number(row["total_seconds"])/3600, total/3600, count)
		repaired++
		if *dryRun {
			continue
		}
		doc["sessions"], doc["apps"], doc["total_seconds"], doc["session_count"], doc["updated_at"] = normalized, apps, total, count, time.Now().UTC()
		body, _ := json.Marshal(doc)
		// Conditional write: if live ingest touched the object since we read it,
		// skip this row instead of overwriting the newer sessions.
		put := &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader(body), ContentType: aws.String("application/json"), IfMatch: etag}
		if _, err := s3Client.PutObject(ctx, put); err != nil {
			var coded interface{ ErrorCode() string }
			if errors.As(err, &coded) && (coded.ErrorCode() == "PreconditionFailed" || coded.ErrorCode() == "ConditionalRequestConflict") {
				log.Printf("skip device=%s user=%s date=%s: object changed during repair; re-run", deviceID, username, date)
				repaired--
				continue
			}
			log.Fatal(err)
		}
		_, err = client.Database(dbName).Collection("app_usage_daily").UpdateOne(ctx, bson.M{"_id": row["_id"]}, bson.M{"$set": bson.M{"total_seconds": total, "session_count": count, "top_apps": apps, "updated_at": time.Now()}})
		if err != nil {
			log.Fatal(err)
		}
	}
	if err := cursor.Err(); err != nil {
		log.Fatal(err)
	}
	log.Printf("done dry_run=%v scanned=%d repaired=%d", *dryRun, scanned, repaired)
}

func activityKey(prefix, deviceID, username, date string) string {
	return fmt.Sprintf("%s/year=%s/month=%s/day=%s/device_id=%s/user=%s/activity.json", prefix, date[:4], date[5:7], date[8:10], pathPart(deviceID), pathPart(username))
}

func pathPart(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "/", "_")
	return strings.ReplaceAll(value, "\\", "_")
}

func readObject(ctx context.Context, client *s3.Client, bucket, key string) (map[string]interface{}, *string, error) {
	res, err := client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		return nil, nil, err
	}
	defer res.Body.Close()
	var doc map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&doc); err != nil {
		return nil, nil, err
	}
	return doc, res.ETag, nil
}

func normalize(raw []interface{}, date string, loc *time.Location) []interface{} {
	day, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		return nil
	}
	endDay := day.Add(24 * time.Hour)
	type item struct {
		app        string
		start, end time.Time
	}
	items := []item{}
	seen := map[string]bool{}
	for _, value := range raw {
		m, ok := value.(map[string]interface{})
		if !ok {
			continue
		}
		app, _ := m["app_name"].(string)
		start, _ := time.Parse(time.RFC3339Nano, fmt.Sprint(m["start_time"]))
		finish, _ := time.Parse(time.RFC3339Nano, fmt.Sprint(m["end_time"]))
		if app == "" || start.IsZero() || !finish.After(start) {
			continue
		}
		if start.Before(day) {
			start = day
		}
		if finish.After(endDay) {
			finish = endDay
		}
		if !finish.After(start) {
			continue
		}
		key := fmt.Sprintf("%s|%d|%d", app, start.UnixNano(), finish.UnixNano())
		if seen[key] {
			continue
		}
		seen[key] = true
		items = append(items, item{app, start, finish})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].start.Before(items[j].start) })
	out := []interface{}{}
	var occupied time.Time
	for _, item := range items {
		if item.start.Before(occupied) {
			item.start = occupied
		}
		if !item.end.After(item.start) {
			continue
		}
		out = append(out, map[string]interface{}{"app_name": item.app, "start_time": item.start.UTC().Format(time.RFC3339Nano), "end_time": item.end.UTC().Format(time.RFC3339Nano), "duration_seconds": item.end.Sub(item.start).Seconds()})
		occupied = item.end
	}
	return out
}

func summarize(raw []interface{}) ([]interface{}, float64, int) {
	totals := map[string]float64{}
	count := map[string]int{}
	for _, value := range raw {
		m := value.(map[string]interface{})
		seconds := number(m["duration_seconds"])
		totals[m["app_name"].(string)] += seconds
		count[m["app_name"].(string)]++
	}
	apps := []interface{}{}
	for app, seconds := range totals {
		apps = append(apps, map[string]interface{}{"app_name": app, "total_seconds": seconds, "session_count": count[app]})
	}
	sort.Slice(apps, func(i, j int) bool {
		return number(apps[i].(map[string]interface{})["total_seconds"]) > number(apps[j].(map[string]interface{})["total_seconds"])
	})
	total := 0.0
	for _, seconds := range totals {
		total += seconds
	}
	return apps, total, len(raw)
}

func number(value interface{}) float64 {
	switch n := value.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case primitive.Decimal128:
		v, _, err := n.BigInt()
		if err != nil {
			return 0
		}
		f, _ := new(big.Float).SetInt(v).Float64()
		return f
	}
	return 0
}
