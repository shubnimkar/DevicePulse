package main

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func TestBucketWindows(t *testing.T) {
	base := time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)
	if bucket(base, 60) != bucket(base.Add(59*time.Minute), 60) {
		t.Fatal("same hour must share a window")
	}
	if bucket(base, 60) == bucket(base.Add(61*time.Minute), 60) {
		t.Fatal("next hour must be a new window")
	}
}

func testColl(t *testing.T) *mongo.Collection {
	t.Helper()
	uri := os.Getenv("TEST_MONGO_URI")
	if uri == "" {
		t.Skip("TEST_MONGO_URI not set")
	}
	ctx := context.Background()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		t.Skipf("MongoDB unreachable: %v", err)
	}
	db := client.Database(fmt.Sprintf("devicepulse_slimtest_%d", time.Now().UnixNano()))
	t.Cleanup(func() { _ = db.Drop(ctx); _ = client.Disconnect(ctx) })
	return db.Collection("telemetry")
}

func fullDoc(device, role string, at time.Time) bson.M {
	return bson.M{
		"device_id": device, "collector_role": role, "ingested_at": at,
		"timestamp": at.Format(time.RFC3339), "username": "alice",
		"data": bson.M{
			"Services":      bson.M{"services": bson.A{"a", "b"}},
			"InstalledApps": bson.M{"installed_apps": bson.A{"x"}},
			"HardwareStats": bson.M{"cpu": 1},
			"ActiveWindowTracker": bson.M{
				"app_summaries":        bson.A{bson.M{"app_name": "Code", "total_focus_seconds": 5.0}},
				"cumulative_summaries": bson.A{bson.M{"app_name": "Code", "total_focus_seconds": 999.0}},
				"sessions":             bson.A{bson.M{"app_name": "Code"}},
			},
			"BrowserHistory": bson.M{
				"archive":             bson.M{"bucket": "b", "key": "k"},
				"top_recent_urls":     bson.A{bson.M{"url": "https://x"}},
				"new_history_entries": bson.A{bson.M{"url": "https://y"}},
			},
		},
	}
}

func count(t *testing.T, c *mongo.Collection, filter bson.M) int64 {
	t.Helper()
	n, err := c.CountDocuments(context.Background(), filter)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSlimTelemetryDryRunThenApply(t *testing.T) {
	c := testColl(t)
	ctx := context.Background()
	hour := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)

	// Device A system: 5 full docs in one hour, 3 in the next.
	for i := 0; i < 5; i++ {
		c.InsertOne(ctx, fullDoc("A", "system", hour.Add(time.Duration(i)*time.Minute)))
	}
	for i := 0; i < 3; i++ {
		c.InsertOne(ctx, fullDoc("A", "system", hour.Add(time.Hour+time.Duration(i)*time.Minute)))
	}
	// A different device and role in the same hour get their own snapshot.
	c.InsertOne(ctx, fullDoc("B", "system", hour))
	c.InsertOne(ctx, fullDoc("A", "active_window", hour))
	// Already handled documents are left alone.
	c.InsertOne(ctx, bson.M{"device_id": "A", "collector_role": "system", "ingested_at": hour, "slim": true})
	c.InsertOne(ctx, bson.M{"device_id": "A", "collector_role": "system", "ingested_at": hour, "telemetry_archive": bson.M{"key": "k"}, "data": bson.M{"Services": bson.A{"s3"}}})
	snap := fullDoc("A", "system", hour.Add(2*time.Hour))
	snap["snapshot"] = true
	c.InsertOne(ctx, snap)
	// Full doc in the same window as an existing snapshot is slimmed.
	c.InsertOne(ctx, fullDoc("A", "system", hour.Add(2*time.Hour+time.Minute)))

	total := count(t, c, bson.M{})
	withServices := count(t, c, bson.M{"data.Services": bson.M{"$exists": true}})

	// Dry run: reports, changes nothing.
	res, err := run(ctx, c, options_{SnapshotMinutes: 60, Batch: 3})
	if err != nil {
		t.Fatal(err)
	}
	if res.Slimmed != 7 || res.Kept != 4 || res.Scanned != total {
		t.Fatalf("dry run counts wrong: %+v (total %d)", res, total)
	}
	if res.BytesBefore <= 0 {
		t.Fatal("dry run must report the size of what it would slim")
	}
	if got := count(t, c, bson.M{"data.Services": bson.M{"$exists": true}}); got != withServices {
		t.Fatalf("dry run modified documents: %d -> %d", withServices, got)
	}
	if count(t, c, bson.M{"slim": true}) != 1 {
		t.Fatal("dry run must not mark documents")
	}

	// Apply.
	res, err = run(ctx, c, options_{Apply: true, SnapshotMinutes: 60, Batch: 3})
	if err != nil || res.Slimmed != 7 {
		t.Fatalf("apply: %+v %v", res, err)
	}
	if got := count(t, c, bson.M{"data.Services": bson.M{"$exists": true}}); got != withServices-7 {
		t.Fatalf("expected %d docs to keep Services, got %d", withServices-7, got)
	}

	// What the API still reads survives on a slimmed document.
	var d bson.M
	if err := c.FindOne(ctx, bson.M{"slim": true, "timestamp": hour.Add(time.Minute).Format(time.RFC3339)}).Decode(&d); err != nil {
		t.Fatal(err)
	}
	data := d["data"].(bson.M)
	awt := data["ActiveWindowTracker"].(bson.M)
	if awt["app_summaries"] == nil || awt["sessions"] != nil || awt["cumulative_summaries"] != nil {
		t.Fatalf("active window data wrong after slimming: %#v", awt)
	}
	bh := data["BrowserHistory"].(bson.M)
	if bh["archive"] == nil || bh["top_recent_urls"] != nil || bh["new_history_entries"] != nil {
		t.Fatalf("browser history data wrong after slimming: %#v", bh)
	}
	if d["timestamp"] == nil || d["ingested_at"] == nil || d["username"] != "alice" {
		t.Fatalf("identity and timestamps must survive: %#v", d)
	}
	for _, heavy := range []string{"Services", "InstalledApps", "HardwareStats"} {
		if data[heavy] != nil {
			t.Fatalf("%s still present", heavy)
		}
	}

	// Idempotent.
	res, err = run(ctx, c, options_{Apply: true, SnapshotMinutes: 60, Batch: 3})
	if err != nil || res.Slimmed != 0 {
		t.Fatalf("second run must find nothing to slim: %+v %v", res, err)
	}
}

func TestSlimTelemetryLimit(t *testing.T) {
	c := testColl(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 10; i++ {
		c.InsertOne(ctx, fullDoc("A", "system", at.Add(time.Duration(i)*time.Minute)))
	}
	res, err := run(ctx, c, options_{Apply: true, SnapshotMinutes: 60, Batch: 100, Limit: 4})
	if err != nil || res.Slimmed != 4 {
		t.Fatalf("limit not honoured: %+v %v", res, err)
	}
	if got := count(t, c, bson.M{"slim": true}); got != 4 {
		t.Fatalf("slimmed documents = %d, want 4", got)
	}
}
