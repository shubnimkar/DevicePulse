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
	"os"
	"runtime"
	"sync"
	"time"

	"os/exec"
	"strings"
)

const sampleInterval = 2 * time.Second

// focusSession is a single contiguous focus period for one app.
type focusSession struct {
	AppName   string    `json:"app_name"`
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
	DurationS float64   `json:"duration_seconds"`
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
	currentApp   string
	sessionStart time.Time

	// accumulated since last Collect() — reset each cycle
	sessions []focusSession

	// cumulative totals since agent start — never reset
	cumulative map[string]*AppFocusSummary

	stopCh chan struct{}
	wg     sync.WaitGroup
}

func (a *ActiveWindowTracker) Name() string { return "ActiveWindowTracker" }

func (a *ActiveWindowTracker) Start() error {
	a.cumulative = make(map[string]*AppFocusSummary)
	a.stopCh = make(chan struct{})
	a.wg.Add(1)
	go a.sample()
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
	now := time.Now()
	if a.currentApp != "" {
		dur := now.Sub(a.sessionStart).Seconds()
		if dur > 0 {
			a.sessions = append(a.sessions, focusSession{
				AppName:   a.currentApp,
				StartTime: a.sessionStart,
				EndTime:   now,
				DurationS: dur,
			})
			a.sessionStart = now
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
		t.SessionCount++

		c, ok := a.cumulative[s.AppName]
		if !ok {
			c = &AppFocusSummary{AppName: s.AppName}
			a.cumulative[s.AppName] = c
		}
		c.TotalFocusS += s.DurationS
		c.SessionCount++
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
	sessions := append([]focusSession(nil), a.sessions...)
	a.sessions = nil // reset per-cycle buffer only

	return map[string]interface{}{
		"current_app":          current,
		"sessions":             sessions,
		"app_summaries":        cycleSummaries,
		"cumulative_summaries": cumSummaries,
	}, nil
}

// sample polls the active window at sampleInterval.
func (a *ActiveWindowTracker) sample() {
	defer a.wg.Done()

	ticker := time.NewTicker(sampleInterval)
	defer ticker.Stop()

	for {
		select {
		case <-a.stopCh:
			return
		case t := <-ticker.C:
			app := getForegroundApp()

			a.mu.Lock()
			if app == "" {
				if a.currentApp != "" {
					dur := t.Sub(a.sessionStart).Seconds()
					if dur > 0 {
						a.sessions = append(a.sessions, focusSession{
							AppName:   a.currentApp,
							StartTime: a.sessionStart,
							EndTime:   t,
							DurationS: dur,
						})
					}
					a.currentApp = ""
					a.sessionStart = time.Time{}
				}
				a.mu.Unlock()
				continue
			}

			if app != a.currentApp {
				if a.currentApp != "" {
					dur := t.Sub(a.sessionStart).Seconds()
					if dur > 0 {
						a.sessions = append(a.sessions, focusSession{
							AppName:   a.currentApp,
							StartTime: a.sessionStart,
							EndTime:   t,
							DurationS: dur,
						})
					}
				}
				a.currentApp = app
				a.sessionStart = t
			}
			a.mu.Unlock()
		}
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
// Strategy (tried in order, first success wins):
//
//  1. X11  – pure-Go XCB via github.com/jezek/xgb
//             Reads _NET_ACTIVE_WINDOW then _NET_WM_NAME / WM_CLASS.
//             Works for any X11 desktop (GNOME-X, KDE-X, XFCE, i3, …).
//             No xdotool, no xprop, no shell commands.
//
//  2. Wayland/GNOME  – D-Bus: org.gnome.Shell → org.gnome.Shell.FocusedWindow
//                      Falls back to org.gnome.Shell.Eval for older GNOME.
//
//  3. Wayland/KDE    – D-Bus: org.kde.KWin → activeClient → caption
//
//  4. Wayland/Sway   – swaymsg -t get_tree (JSON) parsed purely in Go.
//                      swaymsg is Sway's own IPC binary; it ships with Sway.
//
//  5. /proc fallback – enumerate /proc/[pid]/stat looking for the process that
//                      has the same session ID as the user's login session and
//                      is in the foreground process group.  Works on any kernel
//                      without a display server requirement.

func getForegroundAppLinux() string {
	// Prefer X11 when DISPLAY is set; fall back to Wayland paths.
	hasDisplay := os.Getenv("DISPLAY") != ""
	hasWayland := os.Getenv("WAYLAND_DISPLAY") != "" || os.Getenv("XDG_SESSION_TYPE") == "wayland"

	if hasDisplay {
		if name := getActiveWindowX11(); name != "" {
			return cleanLinuxForegroundApp(name)
		}
	}

	// Wayland paths.
	if hasWayland {
		// Try GNOME first (most deployed enterprise Linux desktop).
		if name := getActiveWindowWaylandGNOME(); name != "" {
			return cleanLinuxForegroundApp(name)
		}
		// Try KDE Plasma.
		if name := getActiveWindowWaylandKDE(); name != "" {
			return cleanLinuxForegroundApp(name)
		}
		// Try Sway / wlroots compositors.
		if name := getActiveWindowSway(); name != "" {
			return cleanLinuxForegroundApp(name)
		}
	}

	// Without a display session, /proc can only tell us about background
	// processes, not the GUI app the person is using.
	if !hasDisplay && !hasWayland {
		return ""
	}

	// /proc-based fallback — best effort for broken display-server access.
	return cleanLinuxForegroundApp(getActiveWindowProcFallback())
}

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
