package collector

import (
	"testing"
	"time"
)

func newTestTracker(t0 time.Time) *ActiveWindowTracker {
	return &ActiveWindowTracker{cumulative: map[string]*AppFocusSummary{}, lastSampleAt: t0}
}

func sessionSummary(a *ActiveWindowTracker) map[string]float64 {
	out := map[string]float64{}
	for _, s := range a.sessions {
		out[s.AppName] += s.DurationS
	}
	return out
}

func TestObserveSwitchesApps(t *testing.T) {
	t0 := time.Now()
	a := newTestTracker(t0)
	a.observe(t0, "firefox", "gnome-extension", false)
	a.observe(t0.Add(2*time.Second), "firefox", "gnome-extension", false)
	a.observe(t0.Add(4*time.Second), "code", "gnome-extension", false)
	a.observe(t0.Add(6*time.Second), "code", "gnome-extension", false)
	a.observe(t0.Add(8*time.Second), "", "gnome-extension", false) // nothing focused

	got := sessionSummary(a)
	if got["firefox"] != 4 || got["code"] != 4 || len(a.sessions) != 2 {
		t.Fatalf("sessions = %+v", a.sessions)
	}
	if a.currentApp != "" {
		t.Fatal("no app is focused any more")
	}
}

// A suspend (or stalled sampler) must not be credited to the app that was open:
// this produced a 13-hour session on a laptop that was asleep overnight.
func TestObserveDoesNotCreditAGapToTheOpenApp(t *testing.T) {
	t0 := time.Now()
	a := newTestTracker(t0)
	a.observe(t0, "claude-desktop", "gnome-extension", false)
	a.observe(t0.Add(2*time.Second), "claude-desktop", "gnome-extension", false)
	// Laptop suspended; the next sample arrives 13 hours later, same app.
	resume := t0.Add(13 * time.Hour)
	a.observe(resume, "claude-desktop", "gnome-extension", false)
	a.observe(resume.Add(2*time.Second), "claude-desktop", "gnome-extension", false)
	a.endSession(resume.Add(4 * time.Second))

	var total float64
	for _, s := range a.sessions {
		total += s.DurationS
		if s.DurationS > 5 {
			t.Fatalf("a session spans the suspend gap: %+v", s)
		}
	}
	if total != 6 { // 2s before the suspend + 4s after
		t.Fatalf("total credited = %vs, want 6s (sessions %+v)", total, a.sessions)
	}
}

func TestObserveIdleAndLockEndSessions(t *testing.T) {
	t0 := time.Now()
	a := newTestTracker(t0)
	a.observe(t0, "firefox", "x11", false)
	a.observe(t0.Add(10*time.Second), "firefox", "x11", false)
	a.observe(t0.Add(12*time.Second), "firefox", "x11", true) // away: locked or idle
	a.observe(t0.Add(14*time.Second), "firefox", "x11", true)
	a.observe(t0.Add(3600*time.Second), "firefox", "x11", true) // an hour away
	if got := sessionSummary(a)["firefox"]; got != 12 {
		t.Fatalf("only the 12s at the keyboard may count, got %vs", got)
	}
	if !a.idle {
		t.Fatal("idle state must be reported")
	}
	a.observe(t0.Add(3602*time.Second), "firefox", "x11", false) // back at the keyboard
	if a.currentApp != "firefox" || a.sessionStart != t0.Add(3602*time.Second) {
		t.Fatal("a new session must start when the user returns")
	}
}

func TestCollectSoftCloseNeverCreditsPastTheLastSample(t *testing.T) {
	now := time.Now()
	a := newTestTracker(now.Add(-30 * time.Second))
	a.currentApp = "firefox"
	a.sessionStart = now.Add(-40 * time.Second)
	a.focusSource = "gnome-extension"

	snap, err := a.Collect()
	if err != nil {
		t.Fatal(err)
	}
	sessions := snap["sessions"].([]focusSession)
	if len(sessions) != 1 || sessions[0].DurationS != 10 {
		t.Fatalf("expected 10s up to the last sample, got %+v", sessions)
	}
	if snap["focus_source"] != "gnome-extension" || snap["focus_quality"] != "accurate" {
		t.Fatalf("source/quality not reported: %v / %v", snap["focus_source"], snap["focus_quality"])
	}
}

func TestFocusQualityLevels(t *testing.T) {
	for source, want := range map[string]string{
		"gnome-extension": "accurate", "x11": "accurate", "kde": "accurate", "sway": "accurate", "macos": "accurate",
		sourceProcGuess: "estimated",
		sourceNone:      "unavailable", "": "unavailable",
	} {
		if got := focusQuality(source); got != want {
			t.Errorf("focusQuality(%q) = %q, want %q", source, got, want)
		}
	}
}

func TestSourceStaysWhenASampleIdentifiesNothing(t *testing.T) {
	t0 := time.Now()
	a := newTestTracker(t0)
	a.observe(t0, "code", "gnome-extension", false)
	a.observe(t0.Add(2*time.Second), "", "", false) // e.g. nothing focused, source unknown
	if a.focusSource != "gnome-extension" {
		t.Fatalf("source = %q", a.focusSource)
	}
}

func TestSetFocusNoteIsReported(t *testing.T) {
	a := newTestTracker(time.Now())
	a.SetFocusNote("extension installed; log out and back in")
	snap, _ := a.Collect()
	if snap["focus_note"] != "extension installed; log out and back in" {
		t.Fatalf("note missing: %v", snap["focus_note"])
	}
	if snap["focus_quality"] != "unavailable" {
		t.Fatalf("a tracker with no source must report unavailable, got %v", snap["focus_quality"])
	}
}

func TestIdleThresholdAndProcGuessSettings(t *testing.T) {
	t.Setenv("DEVICEPULSE_IDLE_SECONDS", "")
	if idleThreshold() != 180*time.Second {
		t.Fatalf("default = %v", idleThreshold())
	}
	t.Setenv("DEVICEPULSE_IDLE_SECONDS", "60")
	if idleThreshold() != 60*time.Second {
		t.Fatal("env not honoured")
	}
	t.Setenv("DEVICEPULSE_IDLE_SECONDS", "0")
	if idleThreshold() != 0 {
		t.Fatal("0 must disable idle detection")
	}
	t.Setenv("DEVICEPULSE_IDLE_SECONDS", "-5")
	if idleThreshold() != 180*time.Second {
		t.Fatal("invalid values fall back to the default")
	}

	t.Setenv("DEVICEPULSE_ALLOW_PROC_GUESS", "")
	if allowProcGuess() {
		t.Fatal("guessing must be off by default")
	}
	t.Setenv("DEVICEPULSE_ALLOW_PROC_GUESS", "true")
	if !allowProcGuess() {
		t.Fatal("opt-in not honoured")
	}
}
