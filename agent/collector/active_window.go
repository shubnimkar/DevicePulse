package collector

// ActiveWindowTracker tracks which application is in the foreground and
// accumulates per-app focus duration between Collect() calls.
//
// Platform support
//   macOS   – AppleScript via osascript (no CGo, no extra deps)
//   Linux   – Zero external binary dependencies:
//               X11 session:     pure-Go XCB via github.com/jezek/xgb
//                                reads _NET_ACTIVE_WINDOW + _NET_WM_NAME / WM_CLASS
//               Wayland/GNOME:   D-Bus org.gnome.Shell (via godbus/dbus/v5)
//               Wayland/KDE:     D-Bus org.kde.KWin activeClient
//               Wayland/generic: /proc scan for foreground process on active seat
//   Windows – Direct Win32 DLL calls via golang.org/x/sys/windows
//             (see active_window_windows.go — no PowerShell subprocess)

import (
	"log"
	"runtime"
	"sync"
	"time"

	"os/exec"
	"strings"
)

const (
	sampleInterval            = 2 * time.Second
	foregroundAppTimeout      = 10 * time.Second
	activeWindowSampleStaleAt = 5 * time.Minute
)

// focusSession is a single contiguous focus period for one app.
type focusSession struct {
	AppName   string    `json:"app_name"`
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
	DurationS float64   `json:"duration_seconds"`
	// Continuation marks a fragment of an already-counted focus period so the
	// per-cycle soft-close in Collect() does not inflate session counts.
	// Never serialized — it does not leave the agent.
	Continuation bool `json:"-"`
}

// AppFocusSummary is the per-app aggregated focus data returned by Collect().
type AppFocusSummary struct {
	AppName      string  `json:"app_name"`
	TotalFocusS  float64 `json:"total_focus_seconds"`
	SessionCount int     `json:"session_count"`
}

// ActiveWindowTracker implements the Collector interface.
type ActiveWindowTracker struct {
	mu sync.Mutex

	// live state
	currentApp     string
	sessionStart   time.Time
	sessionCounted bool // true once the open focus period has been counted as a session

	// accumulated since last Collect() — reset each cycle
	sessions []focusSession

	// cumulative totals since agent start — never reset
	cumulative map[string]*AppFocusSummary

	lastSampleAt time.Time

	stopCh chan struct{}
	wg     sync.WaitGroup
}

func (a *ActiveWindowTracker) Name() string { return "ActiveWindowTracker" }

func (a *ActiveWindowTracker) Start() error {
	a.cumulative = make(map[string]*AppFocusSummary)
	a.lastSampleAt = time.Now()
	a.stopCh = make(chan struct{})
	a.wg.Add(1)
	go a.runSampler()
	return nil
}

func (a *ActiveWindowTracker) Stop() error {
	close(a.stopCh)
	a.wg.Wait()
	return nil
}

// Collect returns a snapshot of focus data accumulated since the last call,
// then resets the per-cycle counters. Cumulative totals (since agent start)
// are never reset.
func (a *ActiveWindowTracker) Collect() (map[string]interface{}, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Soft-close the in-progress session so it appears in this snapshot.
	// The fragment is flagged as a continuation when this focus period was
	// already counted by a previous Collect() — only the first fragment of a
	// contiguous period counts as a session.
	now := time.Now()
	fresh := !a.lastSampleAt.IsZero() && now.Sub(a.lastSampleAt) <= activeWindowSampleStaleAt
	if a.currentApp != "" && fresh {
		dur := now.Sub(a.sessionStart).Seconds()
		if dur > 0 {
			a.sessions = append(a.sessions, focusSession{
				AppName:      a.currentApp,
				StartTime:    a.sessionStart,
				EndTime:      now,
				DurationS:    dur,
				Continuation: a.sessionCounted,
			})
			a.sessionStart = now
			a.sessionCounted = true
		}
	}

	// Aggregate per-cycle delta AND accumulate into cumulative totals.
	cycleTotals := map[string]*AppFocusSummary{}
	for _, s := range a.sessions {
		t, ok := cycleTotals[s.AppName]
		if !ok {
			t = &AppFocusSummary{AppName: s.AppName}
			cycleTotals[s.AppName] = t
		}
		t.TotalFocusS += s.DurationS

		c, ok := a.cumulative[s.AppName]
		if !ok {
			c = &AppFocusSummary{AppName: s.AppName}
			a.cumulative[s.AppName] = c
		}
		c.TotalFocusS += s.DurationS

		// Only the first fragment of a contiguous focus period counts as a
		// session — soft-closed continuations must not inflate the count.
		if !s.Continuation {
			t.SessionCount++
			c.SessionCount++
		}
	}

	cycleSummaries := make([]AppFocusSummary, 0, len(cycleTotals))
	for _, v := range cycleTotals {
		cycleSummaries = append(cycleSummaries, *v)
	}

	cumSummaries := make([]AppFocusSummary, 0, len(a.cumulative))
	for _, v := range a.cumulative {
		cumSummaries = append(cumSummaries, *v)
	}

	current := a.currentApp
	if !fresh {
		current = ""
	}
	sessions := append([]focusSession(nil), a.sessions...)
	a.sessions = nil // reset per-cycle buffer only

	return map[string]interface{}{
		"collected_at":         now,
		"last_sample_at":       a.lastSampleAt,
		"tracker_fresh":        fresh,
		"current_app":          current,
		"sessions":             sessions,
		"app_summaries":        cycleSummaries,
		"cumulative_summaries": cumSummaries,
	}, nil
}

// runSampler keeps the sampling loop alive even if a platform-specific active
// window backend panics.
func (a *ActiveWindowTracker) runSampler() {
	defer a.wg.Done()

	for {
		stopped := a.sample()
		if stopped {
			return
		}
		select {
		case <-a.stopCh:
			return
		case <-time.After(time.Second):
			log.Printf("ActiveWindowTracker: restarting sampler after failure")
		}
	}
}

// sample polls the active window at sampleInterval. It returns false when the
// loop exited because of a recovered panic and should be restarted.
func (a *ActiveWindowTracker) sample() (stopped bool) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("ActiveWindowTracker: recovered sampler panic: %v", r)
			stopped = false
		}
	}()

	ticker := time.NewTicker(sampleInterval)
	defer ticker.Stop()

	for {
		select {
		case <-a.stopCh:
			return true
		case t := <-ticker.C:
			app, ok := foregroundAppWithTimeout(foregroundAppTimeout)
			if !ok {
				log.Printf("ActiveWindowTracker: active-window sample failed or timed out")
				continue
			}

			a.mu.Lock()
			a.lastSampleAt = t
			if app == "" {
				if a.currentApp != "" {
					dur := t.Sub(a.sessionStart).Seconds()
					if dur > 0 {
						a.sessions = append(a.sessions, focusSession{
							AppName:      a.currentApp,
							StartTime:    a.sessionStart,
							EndTime:      t,
							DurationS:    dur,
							Continuation: a.sessionCounted,
						})
					}
					a.currentApp = ""
					a.sessionStart = time.Time{}
					a.sessionCounted = false
				}
				a.mu.Unlock()
				continue
			}

			if app != a.currentApp {
				if a.currentApp != "" {
					dur := t.Sub(a.sessionStart).Seconds()
					if dur > 0 {
						a.sessions = append(a.sessions, focusSession{
							AppName:      a.currentApp,
							StartTime:    a.sessionStart,
							EndTime:      t,
							DurationS:    dur,
							Continuation: a.sessionCounted,
						})
					}
				}
				a.currentApp = app
				a.sessionStart = t
				a.sessionCounted = false
			}
			a.mu.Unlock()
		}
	}
}

func foregroundAppWithTimeout(timeout time.Duration) (string, bool) {
	type foregroundResult struct {
		app string
		ok  bool
	}
	result := make(chan foregroundResult, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("ActiveWindowTracker: recovered foreground app panic: %v", r)
				result <- foregroundResult{ok: false}
			}
		}()
		result <- foregroundResult{app: getForegroundApp(), ok: true}
	}()

	select {
	case res := <-result:
		return res.app, res.ok
	case <-time.After(timeout):
		return "", false
	}
}

// getForegroundApp returns the name of the currently focused application.
// Returns an empty string on error or unsupported platform.
func getForegroundApp() string {
	switch runtime.GOOS {
	case "darwin":
		return getForegroundAppMacOS()
	case "linux":
		return getForegroundAppLinux()
	case "windows":
		return getForegroundAppWindows()
	default:
		return ""
	}
}

// ─── macOS ───────────────────────────────────────────────────────────────────

// getForegroundAppMacOS uses AppleScript to query System Events.
// Requires "Automation" permission for the process running the agent.
func getForegroundAppMacOS() string {
	script := `tell application "System Events" to get name of first application process whose frontmost is true`
	out, err := exec.Command("osascript", "-e", script).Output()
	if err != nil {
		log.Printf("ActiveWindowTracker (macOS): osascript error: %v", err)
		return ""
	}
	return strings.TrimSpace(string(out))
}

// ─── Linux ───────────────────────────────────────────────────────────────────
//
// getForegroundAppLinux is implemented in active_window_linux.go (build-tagged).
// It tries X11 → Wayland/GNOME → Wayland/KDE → Sway → /proc fallback, and
// when running as root also probes /proc/<pid>/environ to inject the user's
// display session before attempting X11/Wayland queries.

// ─── Linux / X11 (pure-Go XCB) ───────────────────────────────────────────────

func getActiveWindowX11() string {
	return linuxX11ActiveWindow() // implemented in active_window_linux_x11.go
}

// ─── Linux / Wayland – GNOME (D-Bus) ─────────────────────────────────────────

func getActiveWindowWaylandGNOME() string {
	return linuxWaylandGNOMEActiveWindow() // active_window_linux_wayland.go
}

// ─── Linux / Wayland – KDE (D-Bus) ───────────────────────────────────────────

func getActiveWindowWaylandKDE() string {
	return linuxWaylandKDEActiveWindow() // active_window_linux_wayland.go
}

// ─── Linux / Wayland – Sway IPC ───────────────────────────────────────────────

func getActiveWindowSway() string {
	return linuxWaylandSwayActiveWindow() // active_window_linux_wayland.go
}

// ─── Linux / /proc fallback ───────────────────────────────────────────────────

func getActiveWindowProcFallback() string {
	return linuxProcFallbackActiveWindow() // active_window_linux_proc.go
}

// ─── Windows ─────────────────────────────────────────────────────────────────
// getForegroundAppWindows is implemented in active_window_windows.go using
// direct Win32 DLL calls (no PowerShell subprocess).
