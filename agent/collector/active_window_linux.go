//go:build linux
// +build linux

package collector

// active_window_linux.go — Linux active-window detection dispatcher.
//
// Strategy (tried in order, first non-empty result wins):
//
//  1. X11  – pure-Go XCB: reads _NET_ACTIVE_WINDOW + _NET_WM_NAME / WM_CLASS.
//
//  2. Wayland/GNOME  – D-Bus org.gnome.Shell.Eval (JS in shell process).
//
//  3. Wayland/KDE    – D-Bus org.kde.KWin.activeClient.
//
//  4. Wayland/Sway   – Sway IPC socket (SWAYSOCK).
//
//  5. Root-service session injection — when the agent runs as a root systemd
//     service it has no $DISPLAY / $WAYLAND_DISPLAY of its own.  We walk
//     /proc/<pid>/environ for all human user processes (UID >= 1000) to find
//     their display session env vars, temporarily inject them, and retry the
//     X11/Wayland probes above.  This lets the root service see the logged-in
//     user's active window without deploying a separate per-user service.
//
//  6. /proc fallback — scans /proc for the highest-RSS desktop app owned by
//     a human user.  Last resort; less accurate than display-server queries.

import (
	"os"
	"strconv"
	"strings"
)

// getForegroundAppLinux is the Linux entry point called from active_window.go.
func getForegroundAppLinux() string {
	hasDisplay := os.Getenv("DISPLAY") != ""
	hasWayland := os.Getenv("WAYLAND_DISPLAY") != "" || os.Getenv("XDG_SESSION_TYPE") == "wayland"

	// ── Strategy 1: X11 ──────────────────────────────────────────────────────
	if hasDisplay {
		if name := getActiveWindowX11(); name != "" {
			return cleanLinuxForegroundApp(name)
		}
	}

	// ── Strategy 2-4: Wayland ────────────────────────────────────────────────
	if hasWayland {
		if name := getActiveWindowWaylandGNOME(); name != "" {
			return cleanLinuxForegroundApp(name)
		}
		if name := getActiveWindowWaylandKDE(); name != "" {
			return cleanLinuxForegroundApp(name)
		}
		if name := getActiveWindowSway(); name != "" {
			return cleanLinuxForegroundApp(name)
		}
	}

	// ── Strategy 5: Root-service session injection ───────────────────────────
	// When neither DISPLAY nor WAYLAND_DISPLAY is set (root systemd service),
	// probe /proc/<pid>/environ of human user processes to find their display
	// session and attempt X11/Wayland queries on their behalf.
	if !hasDisplay && !hasWayland {
		if name := getActiveWindowFromUserSessions(); name != "" {
			return cleanLinuxForegroundApp(name)
		}
		// Still nothing — return empty rather than surfacing a background daemon.
		return ""
	}

	// ── Strategy 6: /proc fallback ───────────────────────────────────────────
	// Reaches here only when we have a display env but the display-server
	// queries all failed (e.g. XAUTHORITY permission error).
	return cleanLinuxForegroundApp(getActiveWindowProcFallback())
}

// getActiveWindowFromUserSessions probes /proc/<pid>/environ for all human user
// processes (UID >= 1000) to discover their display session environment, then
// retries X11/Wayland queries inside each discovered session.
//
// This is the critical path that lets the root systemd service correctly report
// what the logged-in user is doing without a separate per-user service.
func getActiveWindowFromUserSessions() string {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return ""
	}

	type sessionEnv struct {
		display        string
		waylandDisplay string
		dbusAddr       string
		xauthority     string
		xdgSessionType string
		swaySocket     string
		i3Socket       string
	}

	seen := map[string]bool{}
	var sessions []sessionEnv

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid := e.Name()
		if pid == "" || pid[0] < '0' || pid[0] > '9' {
			continue
		}

		// Only consider human user processes (UID >= 1000).
		uid := getProcUID("/proc/" + pid + "/status")
		if uid == "" {
			continue
		}
		uidInt, err := strconv.Atoi(uid)
		if err != nil || uidInt < 1000 {
			continue
		}

		// Read the NUL-separated process environment.
		envData, err := os.ReadFile("/proc/" + pid + "/environ")
		if err != nil {
			continue
		}

		var env sessionEnv
		for _, kv := range strings.Split(string(envData), "\x00") {
			switch {
			case strings.HasPrefix(kv, "DISPLAY="):
				env.display = kv[len("DISPLAY="):]
			case strings.HasPrefix(kv, "WAYLAND_DISPLAY="):
				env.waylandDisplay = kv[len("WAYLAND_DISPLAY="):]
			case strings.HasPrefix(kv, "DBUS_SESSION_BUS_ADDRESS="):
				env.dbusAddr = kv[len("DBUS_SESSION_BUS_ADDRESS="):]
			case strings.HasPrefix(kv, "XAUTHORITY="):
				env.xauthority = kv[len("XAUTHORITY="):]
			case strings.HasPrefix(kv, "XDG_SESSION_TYPE="):
				env.xdgSessionType = kv[len("XDG_SESSION_TYPE="):]
			case strings.HasPrefix(kv, "SWAYSOCK="):
				env.swaySocket = kv[len("SWAYSOCK="):]
			case strings.HasPrefix(kv, "I3SOCK="):
				env.i3Socket = kv[len("I3SOCK="):]
			}
		}

		if env.display == "" && env.waylandDisplay == "" && env.swaySocket == "" {
			continue
		}

		// Deduplicate sessions by their socket/display identity.
		key := env.display + "|" + env.waylandDisplay + "|" + env.swaySocket
		if seen[key] {
			continue
		}
		seen[key] = true
		sessions = append(sessions, env)
	}

	for _, env := range sessions {
		// Inject session env vars so X11/Wayland library calls can connect.
		if env.display != "" {
			os.Setenv("DISPLAY", env.display)
		}
		if env.waylandDisplay != "" {
			os.Setenv("WAYLAND_DISPLAY", env.waylandDisplay)
		}
		if env.dbusAddr != "" {
			os.Setenv("DBUS_SESSION_BUS_ADDRESS", env.dbusAddr)
		}
		if env.xauthority != "" {
			os.Setenv("XAUTHORITY", env.xauthority)
		}
		if env.swaySocket != "" {
			os.Setenv("SWAYSOCK", env.swaySocket)
		}
		if env.i3Socket != "" {
			os.Setenv("I3SOCK", env.i3Socket)
		}

		var name string

		if env.display != "" {
			name = getActiveWindowX11()
		}
		if name == "" && (env.waylandDisplay != "" || env.xdgSessionType == "wayland") {
			name = getActiveWindowWaylandGNOME()
			if name == "" {
				name = getActiveWindowWaylandKDE()
			}
			if name == "" {
				name = getActiveWindowSway()
			}
		}
		if name == "" && (env.swaySocket != "" || env.i3Socket != "") {
			name = getActiveWindowSway()
		}

		// Always clean up injected env vars so they don't bleed into later calls.
		os.Unsetenv("DISPLAY")
		os.Unsetenv("WAYLAND_DISPLAY")
		os.Unsetenv("DBUS_SESSION_BUS_ADDRESS")
		os.Unsetenv("XAUTHORITY")
		os.Unsetenv("SWAYSOCK")
		os.Unsetenv("I3SOCK")

		if name != "" {
			return name
		}
	}

	// Last resort: /proc-based desktop app scan across all user processes.
	return getActiveWindowProcFallback()
}
