package main

import "testing"

func TestAppNamesMergeAcrossSpellings(t *testing.T) {
	for name, want := range map[string]string{
		"  Google-chrome ": "chrome", "google-chrome-stable": "chrome", "chrome": "chrome",
		"Firefox_firefox": "firefox", "org.mozilla.firefox": "firefox", "firefox": "firefox",
		"MongoDB Compass": "MongoDB Compass", "Code": "Code",
	} {
		if got := displayAppName(name); got != want {
			t.Errorf("displayAppName(%q) = %q, want %q", name, got, want)
		}
	}
	if appKey("Code") != appKey("code") || appKey("Google-chrome") != appKey("chrome") {
		t.Fatal("spellings of the same app must share a key")
	}
	if appKey("code") == appKey("codex") {
		t.Fatal("different apps must not merge")
	}
}

// testSession (activity_merge_test.go) builds the session maps used below.
func TestSummarizeMergesSpellingsOfOneApp(t *testing.T) {
	apps, total := summarizeActivitySessions([]interface{}{
		testSession("code", 0, 60), testSession("Code", 100, 40),
		testSession("Google-chrome", 200, 30), testSession("chrome", 300, 20),
	})
	if total != 150 || len(apps) != 2 {
		t.Fatalf("expected 2 apps and 150s, got %d apps, %vs: %#v", len(apps), total, apps)
	}
	top := apps[0].(map[string]interface{})
	if top["total_seconds"] != 100.0 || top["session_count"] != 2 {
		t.Fatalf("code rows not merged: %#v", top)
	}
}

func TestMergeSessionsJoinsFragmentsWhoseSpellingChanged(t *testing.T) {
	// The agent was updated mid-focus: same window, process name before and
	// window class after. They are one contiguous session.
	merged := mergeActivitySessions([]interface{}{
		testSession("code", 0, 30), testSession("Code", 30, 30),
	}, activitySessionMergeGap)
	if len(merged) != 1 {
		t.Fatalf("expected one session, got %d: %#v", len(merged), merged)
	}
	if dur, _ := toFloat(merged[0].(map[string]interface{})["duration_seconds"]); dur != 60 {
		t.Fatalf("duration = %v", dur)
	}
}

func TestFocusCacheMergesSpellings(t *testing.T) {
	fc := &FocusCache{data: map[string]map[string]*AppFocusEntry{}}
	fc.applyFocusSummaries("d", []interface{}{
		map[string]interface{}{"app_name": "code", "total_focus_seconds": 10.0, "session_count": 1.0},
		map[string]interface{}{"app_name": "Code", "total_focus_seconds": 5.0, "session_count": 1.0},
	})
	snap := fc.snapshot("d")
	if len(snap) != 1 || snap[0].TotalFocusS != 15 || snap[0].SessionCount != 2 {
		t.Fatalf("snapshot = %+v", snap)
	}
}
