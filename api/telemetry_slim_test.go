package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

func resetSnapshots(t *testing.T) {
	t.Helper()
	old := telemetrySnapshots
	telemetrySnapshots = &snapshotTracker{last: map[string]time.Time{}}
	t.Cleanup(func() { telemetrySnapshots = old })
}

// heavyPayload mimics a real Linux system sync (~30 KB of collector data).
func heavyPayload(ts time.Time) map[string]interface{} {
	filler := func(n int) []interface{} {
		out := make([]interface{}, n)
		for i := range out {
			out[i] = map[string]interface{}{"name": fmt.Sprintf("service-%d.service", i), "status": "running", "pid": "1234"}
		}
		return out
	}
	stamp := ts.UTC().Format(time.RFC3339Nano)
	return map[string]interface{}{
		"device_id": "d1", "event_id": "e-" + stamp, "agent_session_id": "s", "boot_id": "b", "sequence": 1.0,
		"collector_role": "system", "timestamp": stamp, "ingested_at": ts, "telemetry_expires_at": ts.AddDate(0, 0, 30),
		"agent_version": "1.0.0", "agent_os": "linux", "agent_arch": "amd64", "username": "alice",
		"data": map[string]interface{}{
			"Services":       map[string]interface{}{"services": filler(400)},
			"InstalledApps":  map[string]interface{}{"installed_apps": filler(150)},
			"ProcessMonitor": map[string]interface{}{"top_processes": filler(80)},
			"NetworkPorts":   map[string]interface{}{"open_ports": filler(60)},
			"HardwareStats":  map[string]interface{}{"cpu": map[string]interface{}{"usage_percent": 12.0}},
			"ActiveWindowTracker": map[string]interface{}{
				"collected_at":         stamp,
				"app_summaries":        []interface{}{map[string]interface{}{"app_name": "Code", "total_focus_seconds": 7.0, "session_count": 1.0}},
				"cumulative_summaries": filler(30),
				"sessions":             filler(30),
			},
			"BrowserHistory": map[string]interface{}{"archive": map[string]interface{}{"bucket": "b", "key": "k", "entry_count": 3.0}},
		},
		"browser_history_archive": map[string]interface{}{"bucket": "b", "key": "k", "entry_count": 3.0},
	}
}

func TestSlimTelemetryDocKeepsOnlyWhatIsRead(t *testing.T) {
	now := time.Now()
	doc := slimTelemetryDoc(heavyPayload(now))

	for _, key := range []string{"device_id", "event_id", "timestamp", "ingested_at", "telemetry_expires_at", "collector_role", "username", "browser_history_archive"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("slim document lost %q", key)
		}
	}
	if doc["slim"] != true {
		t.Error("slim documents must be marked")
	}
	data := doc["data"].(bson.M)
	awt := data["ActiveWindowTracker"].(bson.M)
	if _, ok := awt["app_summaries"]; !ok || len(awt) != 1 {
		t.Errorf("only app_summaries may remain of the active window data: %#v", awt)
	}
	if _, ok := data["BrowserHistory"].(bson.M)["archive"]; !ok {
		t.Error("browser archive pointer must remain")
	}
	for _, heavy := range []string{"Services", "InstalledApps", "ProcessMonitor", "NetworkPorts", "HardwareStats"} {
		if _, ok := data[heavy]; ok {
			t.Errorf("%s must not be stored in a slim document", heavy)
		}
	}
	// The focus-cache rebuild reads documents back from MongoDB, so round-trip
	// through BSON like the real read path does.
	raw, err := bson.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var stored bson.M
	if err := bson.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if got := extractFocusSummaries(stored); len(got) != 1 {
		t.Errorf("focus summaries unreadable from a stored slim doc: %#v", got)
	}

	full, _ := bson.Marshal(bson.M(heavyPayload(now)))
	slim, _ := bson.Marshal(doc)
	if len(slim) > 3*1024 || len(slim)*10 > len(full) {
		t.Errorf("slim doc is %d bytes against %d full; expected a >90%% reduction and under 3 KB", len(slim), len(full))
	}
}

func TestSlimTelemetryDocOmitsMissingFieldsInsteadOfNull(t *testing.T) {
	// An agent that sends no event_id must not get event_id:null, which the
	// unique sparse (device_id, event_id) index would treat as a duplicate value.
	doc := slimTelemetryDoc(map[string]interface{}{"device_id": "d", "timestamp": time.Now().Format(time.RFC3339)})
	for k, v := range doc {
		if v == nil {
			t.Errorf("%s stored as null", k)
		}
	}
	if _, ok := doc["event_id"]; ok {
		t.Error("absent event_id must stay absent")
	}
}

func TestSnapshotTrackerDue(t *testing.T) {
	s := &snapshotTracker{last: map[string]time.Time{}}
	t0 := time.Now()
	if !s.due("d|system", t0, time.Hour) {
		t.Fatal("first sync is a snapshot")
	}
	if s.due("d|system", t0.Add(59*time.Minute), time.Hour) {
		t.Fatal("no snapshot within the interval")
	}
	if !s.due("d|system", t0.Add(61*time.Minute), time.Hour) {
		t.Fatal("snapshot after the interval")
	}
	if !s.due("d|active_window", t0, time.Hour) {
		t.Fatal("roles are tracked separately")
	}
	if s.due("x", t0, 0) {
		t.Fatal("interval 0 disables snapshots")
	}
}

func TestTelemetryDocForStorageModes(t *testing.T) {
	now := time.Now()
	t.Setenv("TELEMETRY_STORE_FULL", "")
	t.Setenv("TELEMETRY_SNAPSHOT_MINUTES", "")
	resetSnapshots(t)

	first := telemetryDocForStorage(heavyPayload(now), nil, now)
	if first["snapshot"] != true || first["data"].(map[string]interface{})["Services"] == nil {
		t.Fatal("first sync must be a full snapshot")
	}
	if telemetryDocForStorage(heavyPayload(now), nil, now.Add(10*time.Second))["slim"] != true {
		t.Fatal("following syncs must be slim")
	}
	if telemetryDocForStorage(heavyPayload(now), nil, now.Add(61*time.Minute))["snapshot"] != true {
		t.Fatal("a new hour starts with a snapshot")
	}

	// S3 archive wins: unchanged behaviour (metadata + pointer).
	archived := telemetryDocForStorage(heavyPayload(now), &TelemetryArchiveResult{Bucket: "b", Key: "k"}, now)
	if archived["telemetry_archive_key"] != "k" {
		t.Fatalf("archived mode changed: %#v", archived)
	}

	// Opt-out keeps the previous behaviour for every sync.
	t.Setenv("TELEMETRY_STORE_FULL", "true")
	if d := telemetryDocForStorage(heavyPayload(now), nil, now.Add(time.Second)); d["slim"] != nil || d["snapshot"] != nil || d["data"] == nil {
		t.Fatal("TELEMETRY_STORE_FULL must store the payload unchanged")
	}

	// Snapshots off: slim only.
	t.Setenv("TELEMETRY_STORE_FULL", "")
	t.Setenv("TELEMETRY_SNAPSHOT_MINUTES", "0")
	resetSnapshots(t)
	if telemetryDocForStorage(heavyPayload(now), nil, now)["slim"] != true {
		t.Fatal("with snapshots disabled every sync is slim")
	}
}

func TestIngestStoresSlimButKeepsEverythingTheDashboardReads(t *testing.T) {
	useTestMongo(t)
	resetSnapshots(t)
	t.Setenv("TELEMETRY_STORE_FULL", "")
	t.Setenv("TELEMETRY_SNAPSHOT_MINUTES", "")
	seedDevice(t, "1.0.0")

	base := time.Now().UTC().Truncate(time.Second)
	for i := 0; i < 3; i++ {
		ts := base.Add(time.Duration(i) * 10 * time.Second)
		p := heavyPayload(ts)
		p["event_id"] = fmt.Sprintf("slim-e%d", i)
		p["timestamp"] = ts.Format(time.RFC3339)
		p["data"].(map[string]interface{})["ActiveWindowTracker"].(map[string]interface{})["collected_at"] = ts.Format(time.RFC3339)
		body := jsonString(t, p)
		if w := call(ingestHandler, http.MethodPost, "/ingest", body, "agent-key"); w.Code != 200 {
			t.Fatalf("ingest %d: %d %s", i, w.Code, w.Body.String())
		}
	}

	ctx := context.Background()
	db := mongoClient.Database(dbName)

	var docs []bson.M
	cur, err := db.Collection("telemetry").Find(ctx, bson.M{"device_id": "d1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := cur.All(ctx, &docs); err != nil || len(docs) != 3 {
		t.Fatalf("expected 3 telemetry docs: %v %d", err, len(docs))
	}
	snapshots, slims := 0, 0
	for _, d := range docs {
		if d["snapshot"] == true {
			snapshots++
			if d["data"].(bson.M)["Services"] == nil {
				t.Error("snapshot must hold the full payload")
			}
		}
		if d["slim"] == true {
			slims++
			if d["data"].(bson.M)["Services"] != nil {
				t.Error("slim document still holds Services")
			}
		}
	}
	if snapshots != 1 || slims != 2 {
		t.Fatalf("snapshots=%d slims=%d, want 1 and 2", snapshots, slims)
	}

	// Current state shown by the dashboard is untouched: every sync still updates devices.
	var dev bson.M
	if err := db.Collection("devices").FindOne(ctx, bson.M{"device_id": "d1"}).Decode(&dev); err != nil {
		t.Fatal(err)
	}
	if dev["data"].(bson.M)["Services"] == nil || dev["data"].(bson.M)["InstalledApps"] == nil {
		t.Fatal("devices document must keep the full current state")
	}

	// Connection history: every sync's timestamp is still there.
	points, err := dailyPresencePoints(ctx, "d1", base.Format("2006-01-02"))
	if err != nil || len(points) != 3 {
		t.Fatalf("presence points = %d (%v), want 3", len(points), err)
	}
	if got := presenceOnlineSeconds(points); got != 20 {
		t.Fatalf("online seconds = %v, want 20", got)
	}

	// App focus totals rebuild from slim documents.
	globalFocusCache.mu.Lock()
	globalFocusCache.data = map[string]map[string]*AppFocusEntry{}
	globalFocusCache.mu.Unlock()
	buildFocusCacheFromMongo()
	var total float64
	for _, e := range globalFocusCache.snapshot("d1") {
		if e.AppName == "Code" {
			total = e.TotalFocusS
		}
	}
	if total != 21 { // 3 syncs x 7s
		t.Fatalf("focus total rebuilt from stored docs = %v, want 21", total)
	}
}

func TestIngestStoreFullOptOutKeepsPreviousBehaviour(t *testing.T) {
	useTestMongo(t)
	resetSnapshots(t)
	t.Setenv("TELEMETRY_STORE_FULL", "true")
	seedDevice(t, "1.0.0")
	for i := 0; i < 2; i++ {
		p := heavyPayload(time.Now().UTC())
		p["event_id"] = fmt.Sprintf("full-e%d", i)
		if w := call(ingestHandler, http.MethodPost, "/ingest", jsonString(t, p), "agent-key"); w.Code != 200 {
			t.Fatalf("ingest: %d %s", w.Code, w.Body.String())
		}
	}
	n, err := mongoClient.Database(dbName).Collection("telemetry").CountDocuments(context.Background(),
		bson.M{"data.Services": bson.M{"$exists": true}})
	if err != nil || n != 2 {
		t.Fatalf("full docs = %d (%v), want 2", n, err)
	}
}

func jsonString(t *testing.T, v interface{}) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
