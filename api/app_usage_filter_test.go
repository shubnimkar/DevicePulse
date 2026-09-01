package main

import (
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

func TestSanitizeActiveWindowPayloadDropsIgnoredProcesses(t *testing.T) {
	payload := map[string]interface{}{
		"data": map[string]interface{}{
			"ActiveWindowTracker": map[string]interface{}{
				"current_app": "fwupd",
				"app_summaries": []interface{}{
					map[string]interface{}{"app_name": "fwupd", "total_focus_seconds": 30.0, "session_count": 1.0},
					map[string]interface{}{"app_name": "Chrome", "total_focus_seconds": 60.0, "session_count": 2.0},
				},
				"sessions": []interface{}{
					map[string]interface{}{"app_name": "fwupd", "duration_seconds": 30.0},
					map[string]interface{}{"app_name": "Chrome", "duration_seconds": 60.0},
				},
			},
		},
	}

	sanitizeActiveWindowPayload(payload)

	awt := payload["data"].(map[string]interface{})["ActiveWindowTracker"].(map[string]interface{})
	if awt["current_app"] != "" {
		t.Fatalf("expected ignored current_app to be cleared, got %q", awt["current_app"])
	}
	summaries := awt["app_summaries"].([]interface{})
	if len(summaries) != 1 || summaries[0].(map[string]interface{})["app_name"] != "Chrome" {
		t.Fatalf("expected only Chrome summary, got %#v", summaries)
	}
	sessions := awt["sessions"].([]interface{})
	if len(sessions) != 1 || sessions[0].(map[string]interface{})["app_name"] != "Chrome" {
		t.Fatalf("expected only Chrome session, got %#v", sessions)
	}
}

func TestFilterDailyAppUsageRowsRecomputesVisibleTotals(t *testing.T) {
	rows := []bson.M{{
		"username": "aditya",
		"top_apps": bson.A{
			bson.M{"app_name": "fwupd", "total_seconds": 30.0, "session_count": 1.0},
			bson.M{"app_name": "Chrome", "total_seconds": 60.0, "session_count": 2.0},
		},
	}}

	filtered := filterDailyAppUsageRows(rows)
	if len(filtered) != 1 {
		t.Fatalf("expected visible row, got %#v", filtered)
	}
	if got := filtered[0]["total_seconds"]; got != 60.0 {
		t.Fatalf("expected total_seconds 60, got %#v", got)
	}
	if got := filtered[0]["session_count"]; got != 2 {
		t.Fatalf("expected session_count 2, got %#v", got)
	}
	apps := filtered[0]["top_apps"].([]interface{})
	if len(apps) != 1 || apps[0].(bson.M)["app_name"] != "Chrome" {
		t.Fatalf("expected only Chrome app, got %#v", apps)
	}
}

func TestCapDailyAppUsageRowsToOnlineSecondsScalesFallbackSummary(t *testing.T) {
	rows := []bson.M{{
		"username":      "ashish",
		"total_seconds": 6 * 3600.0,
		"session_count": 300,
		"top_apps": []interface{}{
			bson.M{"app_name": "chrome", "total_seconds": 3 * 3600.0, "session_count": 150},
			bson.M{"app_name": "pgadmin4", "total_seconds": 2 * 3600.0, "session_count": 100},
			bson.M{"app_name": "code", "total_seconds": 1 * 3600.0, "session_count": 50},
		},
	}}

	capped := capDailyAppUsageRowsToOnlineSeconds(rows, 90*60)
	if len(capped) != 1 {
		t.Fatalf("expected one capped row, got %#v", capped)
	}
	if got := capped[0]["total_seconds"]; got != 90*60.0 {
		t.Fatalf("expected total capped to online seconds, got %#v", got)
	}
	if got := capped[0]["session_count"]; got != 76 {
		t.Fatalf("expected sessions scaled down, got %#v", got)
	}
	apps := capped[0]["top_apps"].([]interface{})
	chrome := apps[0].(map[string]interface{})
	if chrome["total_seconds"] != 45*60.0 {
		t.Fatalf("expected chrome scaled to 45m, got %#v", chrome)
	}
	if chrome["session_count"] != 38 {
		t.Fatalf("expected chrome sessions scaled with ceil, got %#v", chrome)
	}
}

func TestActiveWindowFreshnessFallsBackToLatestSessionEnd(t *testing.T) {
	now := time.Now()
	awt := map[string]interface{}{
		"sessions": []interface{}{
			map[string]interface{}{"end_time": now.Add(-time.Minute).Format(time.RFC3339Nano)},
		},
	}
	if !activeWindowSnapshotFreshAt(awt, now) {
		t.Fatalf("expected recent session end_time to keep snapshot fresh")
	}

	awt["sessions"] = []interface{}{
		map[string]interface{}{"end_time": now.Add(-activeWindowFreshnessWindow - time.Second).Format(time.RFC3339Nano)},
	}
	if activeWindowSnapshotFreshAt(awt, now) {
		t.Fatalf("expected old session end_time to mark snapshot stale")
	}
}

func TestSanitizeDeviceActiveWindowSnapshotBlanksStaleData(t *testing.T) {
	device := bson.M{
		"data": bson.M{
			"ActiveWindowTracker": bson.M{
				"sessions": bson.A{bson.M{"end_time": time.Now().Add(-time.Hour).Format(time.RFC3339Nano)}},
				"cumulative_summaries": bson.A{
					bson.M{"app_name": "Chrome", "total_focus_seconds": 5 * 3600.0},
				},
			},
		},
	}

	sanitizeDeviceActiveWindowSnapshot(device, time.Now())

	awt := device["data"].(bson.M)["ActiveWindowTracker"].(map[string]interface{})
	if stale, _ := awt["stale"].(bool); !stale {
		t.Fatalf("expected stale marker, got %#v", awt)
	}
	if summaries := awt["cumulative_summaries"].([]interface{}); len(summaries) != 0 {
		t.Fatalf("expected stale cumulative summaries to be blanked, got %#v", summaries)
	}
}
