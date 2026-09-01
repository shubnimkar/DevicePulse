package collector

import (
	"testing"
	"time"
)

func TestCollectDoesNotExtendStaleActiveWindowSession(t *testing.T) {
	tracker := &ActiveWindowTracker{
		currentApp:   "Chrome",
		sessionStart: time.Now().Add(-time.Hour),
		cumulative:   map[string]*AppFocusSummary{},
		lastSampleAt: time.Now().Add(-activeWindowSampleStaleAt - time.Second),
	}

	snapshot, err := tracker.Collect()
	if err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}
	if current, _ := snapshot["current_app"].(string); current != "" {
		t.Fatalf("expected stale current_app to be blanked, got %q", current)
	}
	if fresh, _ := snapshot["tracker_fresh"].(bool); fresh {
		t.Fatalf("expected tracker_fresh=false for stale sampler")
	}
	sessions := snapshot["sessions"].([]focusSession)
	if len(sessions) != 0 {
		t.Fatalf("expected stale session not to be soft-closed, got %#v", sessions)
	}
}
