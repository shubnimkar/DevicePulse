package main

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

// When the full telemetry payload is not archived to S3, every sync used to be
// stored whole in MongoDB: about 31 KB per system document (services, installed
// apps, process list, ports, ...) every 10 seconds per device. Nothing in the
// product reads those copies; the dashboard shows the latest values from the
// devices collection, and the telemetry collection is only used for timestamps
// (presence, online time), per-app focus totals and the browser-history pointer.
//
// So each sync is now stored slim (about 1 KB), plus one full snapshot per device
// and collector role per TELEMETRY_SNAPSHOT_MINUTES (default 60) so past state can
// still be inspected at that granularity. The devices collection, app-usage
// summaries and S3 archives are not affected.
//
//	TELEMETRY_STORE_FULL=true       keep storing every sync in full (previous behaviour)
//	TELEMETRY_SNAPSHOT_MINUTES=N    full-snapshot interval, 0 = never (default 60)

func telemetryStoreFull() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("TELEMETRY_STORE_FULL")), "true")
}

func telemetrySnapshotInterval() time.Duration {
	return time.Duration(envInt("TELEMETRY_SNAPSHOT_MINUTES", 60)) * time.Minute
}

// snapshotTracker remembers when each device/role last stored a full snapshot. It
// is per process: after an API restart the next sync of each device is a snapshot.
type snapshotTracker struct {
	mu   sync.Mutex
	last map[string]time.Time
}

var telemetrySnapshots = &snapshotTracker{last: map[string]time.Time{}}

func (s *snapshotTracker) due(key string, now time.Time, every time.Duration) bool {
	if every <= 0 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if last, ok := s.last[key]; ok && now.Sub(last) < every {
		return false
	}
	s.last[key] = now
	return true
}

// slimTelemetryFields are copied into a slim document when present. Missing
// fields are omitted rather than stored as null: the unique sparse
// (device_id, event_id) index would treat a null event_id as a value and reject
// the second document from an agent that sends no event ids.
var slimTelemetryFields = []string{
	"device_id", "event_id", "agent_session_id", "boot_id", "sequence", "collector_role",
	"timestamp", "ingested_at", "telemetry_expires_at", "agent_version", "agent_os",
	"agent_arch", "username", "app_usage_archive", "browser_history_archive",
}

// slimTelemetryDoc keeps what the API reads back from the telemetry collection:
// identity and timestamps, per-cycle app focus summaries (focus-cache rebuild),
// and the browser-history archive pointer (weekly report counts).
func slimTelemetryDoc(payload map[string]interface{}) bson.M {
	doc := bson.M{"slim": true}
	for _, key := range slimTelemetryFields {
		if v, ok := payload[key]; ok && v != nil {
			doc[key] = v
		}
	}
	data := bson.M{}
	if summaries := focusSummariesFromPayload(payload); len(summaries) > 0 {
		data["ActiveWindowTracker"] = bson.M{"app_summaries": summaries}
	}
	if archive, ok := payload["browser_history_archive"]; ok && archive != nil {
		data["BrowserHistory"] = bson.M{"archive": archive}
	}
	if len(data) > 0 {
		doc["data"] = data
	}
	return doc
}

// telemetryDocForStorage decides what goes into the telemetry collection for one sync.
func telemetryDocForStorage(payload map[string]interface{}, archive *TelemetryArchiveResult, now time.Time) bson.M {
	if archive != nil {
		return telemetryMetadata(payload, archive) // full payload lives in S3
	}
	if telemetryStoreFull() {
		return bson.M(payload)
	}
	key := fmt.Sprint(payload["device_id"]) + "|" + fmt.Sprint(payload["collector_role"])
	if telemetrySnapshots.due(key, now, telemetrySnapshotInterval()) {
		doc := make(bson.M, len(payload)+1)
		for k, v := range payload {
			doc[k] = v
		}
		doc["snapshot"] = true
		return doc
	}
	return slimTelemetryDoc(payload)
}
