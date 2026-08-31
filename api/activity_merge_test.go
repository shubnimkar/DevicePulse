package main

import (
	"sort"
	"testing"
	"time"
)

func testSession(app string, offsetSec, durSec float64) map[string]interface{} {
	base := time.Date(2026, 8, 26, 9, 0, 0, 0, time.UTC)
	start := base.Add(time.Duration(offsetSec * float64(time.Second)))
	end := start.Add(time.Duration(durSec * float64(time.Second)))
	return map[string]interface{}{
		"app_name":         app,
		"start_time":       start.Format(time.RFC3339Nano),
		"end_time":         end.Format(time.RFC3339Nano),
		"duration_seconds": durSec,
		"device_id":        "device-1",
		"username":         "suraj",
	}
}

func sortedMerged(sessions []interface{}) []interface{} {
	sorted := append([]interface{}(nil), sessions...)
	sort.Slice(sorted, func(i, j int) bool {
		return activitySessionStart(sorted[i]).Before(activitySessionStart(sorted[j]))
	})
	return mergeActivitySessions(sorted, activitySessionMergeGap)
}

// Simulates the agent soft-closing one focus period on every sync cycle: the
// fragments must collapse into a single session with the full duration and a
// session_count of 1 — not one row per sync cycle.
func TestMergeActivitySessionsContiguousFragments(t *testing.T) {
	sessions := []interface{}{
		testSession("pgadmin4", 0, 30),
		testSession("pgadmin4", 30, 30),
		testSession("pgadmin4", 60, 30),
	}
	got := sortedMerged(sessions)
	if len(got) != 1 {
		t.Fatalf("expected 1 merged session, got %d: %+v", len(got), got)
	}
	m := got[0].(map[string]interface{})
	if m["app_name"] != "pgadmin4" {
		t.Fatalf("unexpected app %v", m["app_name"])
	}
	if dur, _ := toFloat(m["duration_seconds"]); dur != 90 {
		t.Fatalf("expected 90s merged duration, got %v", dur)
	}
}

func TestMergeActivitySessionsRespectsAppSwitchesAndGaps(t *testing.T) {
	sessions := []interface{}{
		testSession("pgadmin4", 0, 30),
		testSession("code", 30, 60),     // app switch: never merged with pgadmin4
		testSession("chrome", 600, 30),  // 8.5 min gap from code: separate session
		testSession("chrome", 615, 30),  // overlap (duplicate delivery): merged
		testSession("chrome", 1200, 30), // > 90s gap from previous chrome: separate
	}
	got := sortedMerged(sessions)
	if len(got) != 4 {
		t.Fatalf("expected 4 merged sessions, got %d: %+v", len(got), got)
	}
	wantOrder := []string{"pgadmin4", "code", "chrome", "chrome"}
	for i, w := range wantOrder {
		m := got[i].(map[string]interface{})
		if m["app_name"] != w {
			t.Fatalf("session %d: expected %s, got %v", i, w, m["app_name"])
		}
	}
	chromeMerged := got[2].(map[string]interface{})
	if dur, _ := toFloat(chromeMerged["duration_seconds"]); dur != 45 {
		t.Fatalf("expected overlapping chrome fragments to span 45s, got %v", dur)
	}
}

func TestMergeActivitySessionsDropsSystemAppsAndJunk(t *testing.T) {
	sessions := []interface{}{
		testSession("fwupd", 0, 30),                  // filtered system daemon
		testSession("code", 0, 30),                   // valid
		"garbage",                                    // non-map entry
		map[string]interface{}{"app_name": "chrome"}, // unparsable timestamps
	}
	got := sortedMerged(sessions)
	if len(got) != 1 {
		t.Fatalf("expected only the code session to survive, got %d: %+v", len(got), got)
	}
	if m := got[0].(map[string]interface{}); m["app_name"] != "code" {
		t.Fatalf("expected the code session, got %+v", m)
	}
}

func TestNormalizeActivitySessionsClipsDeduplicatesAndRemovesOverlap(t *testing.T) {
	day := time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)
	session := func(app string, start, end time.Time) map[string]interface{} {
		return map[string]interface{}{
			"app_name":   app,
			"start_time": start.Format(time.RFC3339Nano),
			"end_time":   end.Format(time.RFC3339Nano),
		}
	}
	input := []interface{}{
		session("chrome", day.Add(-time.Hour), day.Add(2*time.Hour)),
		session("chrome", day.Add(-time.Hour), day.Add(2*time.Hour)),
		session("code", day.Add(time.Hour), day.Add(3*time.Hour)),
	}
	got := normalizeActivitySessions(input, "2026-08-31", time.UTC)
	_, total := summarizeActivitySessions(got)
	if total != 3*3600 {
		t.Fatalf("expected 3h non-overlapping total, got %v seconds (%#v)", total, got)
	}
	if len(got) != 2 {
		t.Fatalf("expected duplicate removal and overlap trimming to leave 2 sessions, got %d", len(got))
	}
}

func TestIsLiveCollectorPayloadPreservesQueuedTelemetry(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	if !isLiveCollectorPayload(map[string]interface{}{"timestamp": now.Add(-10 * time.Second).Format(time.RFC3339Nano)}, now) {
		t.Fatal("recent payload should be treated as live")
	}
	if isLiveCollectorPayload(map[string]interface{}{"timestamp": now.Add(-10 * time.Minute).Format(time.RFC3339Nano)}, now) {
		t.Fatal("queued payload should bypass the live collector lease")
	}
}
