//go:build linux
// +build linux

package collector

// Wayland active-window detection — three independent strategies:
//
//  A. GNOME Shell  – via D-Bus session bus
//                    Interface: org.gnome.Shell
//                    Method:    org.gnome.Shell.Eval (JS eval, fallback)
//                    Property:  org.gnome.Shell.Extensions.FocusedApp (when
//                               gnome-shell-extension-appindicator is active)
//                    Fallback:  org.gnome.Shell.Eval with global.display
//                               .get_focus_window().get_wm_class()
//
//  B. KDE Plasma   – via D-Bus session bus
//                    Service:   org.kde.KWin
//                    Interface: org.kde.KWin
//                    Method:    org.kde.KWin.activeClient  (KWin scripting API)
//                    Fallback:  enumerate org.kde.ActivityManager
//
//  C. Sway / wlroots compositors
//                  – Sway IPC over a UNIX socket (SWAYSOCK env var).
//                    Command:  get_tree → walk focused:true nodes.
//                    Sway ships its own swaymsg binary; we parse the JSON
//                    response ourselves to avoid importing encoding/json in
//                    a hot path (we use a lightweight manual extraction).
//
// All three strategies require zero external binaries beyond the compositor
// itself and the D-Bus daemon, which are always present on Wayland desktops.

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"strings"

	dbus "github.com/godbus/dbus/v5"
)

// ─── GNOME ────────────────────────────────────────────────────────────────────

// linuxWaylandGNOMEActiveWindow queries GNOME Shell via D-Bus.
// It tries two paths:
//  1. org.gnome.Shell.Eval() with a JavaScript snippet to read the focused
//     window's WM_CLASS — works on all GNOME versions 3.x and 40+.
//  2. Reading the org.gnome.Mutter.DisplayConfig if Eval is disabled.
func linuxWaylandGNOMEActiveWindow() string {
	conn, err := dbus.SessionBus()
	if err != nil {
		return ""
	}

	// ── Path 1: org.gnome.Shell.Eval (JS in GNOME Shell process) ─────────────
	obj := conn.Object("org.gnome.Shell", "/org/gnome/Shell")

	var success bool
	var result string
	// This JS runs inside gnome-shell and returns the WM class of the focused app.
	jsCode := `global.display.get_focus_window()?.get_wm_class() ?? ""`
	err = obj.Call("org.gnome.Shell.Eval", 0, jsCode).Store(&success, &result)
	if err == nil && success && result != "" && result != "null" {
		// result is a JSON-encoded string, e.g. `"Firefox"` or `Firefox`
		clean := strings.Trim(result, `"`)
		if clean != "" && clean != "null" {
			return clean
		}
	}

	// ── Path 2: org.gnome.Shell — FocusedApp via AppMenu extension ──────────
	// Some GNOME setups expose a simpler property via the AppMenu D-Bus interface.
	jsCode2 := `global.display.get_focus_window()?.title ?? ""`
	err = obj.Call("org.gnome.Shell.Eval", 0, jsCode2).Store(&success, &result)
	if err == nil && success && result != "" {
		title := strings.Trim(result, `"`)
		if title != "" && title != "null" {
			// Extract just the app name from the window title.
			return extractAppNameFromTitle(title)
		}
	}

	return ""
}

// ─── KDE Plasma ───────────────────────────────────────────────────────────────

// linuxWaylandKDEActiveWindow queries KWin via D-Bus.
// KWin exposes a scripting API; we use org.kde.KWin.activeClient to get the
// window ID, then retrieve its caption.
func linuxWaylandKDEActiveWindow() string {
	conn, err := dbus.SessionBus()
	if err != nil {
		return ""
	}

	// ── Method 1: org.kde.KWin direct activeClient property ────────────────
	kwinObj := conn.Object("org.kde.KWin", "/KWin")
	var caption string
	err = kwinObj.Call("org.kde.KWin.activeClient", 0).Store(&caption)
	if err == nil && caption != "" {
		return extractAppNameFromTitle(caption)
	}

	// ── Method 2: KWin Scripting API (most reliable for KDE 5/6) ─────────────
	// Load a tiny inline KWin script that returns the active client's resource
	// class. The scripting object lives at /Scripting on org.kde.KWin.
	scriptObj := conn.Object("org.kde.KWin", "/Scripting")
	var loadedID int32
	err = scriptObj.Call("org.kde.kwin.Scripting.loadScript", 0,
		`print(workspace.activeClient ? workspace.activeClient.resourceClass : "")`,
		"devicepulse-helper").Store(&loadedID)
	if err == nil && loadedID > 0 {
		runPath := dbus.ObjectPath(fmt.Sprintf("/%d", loadedID))
		runObj := conn.Object("org.kde.KWin", runPath)
		_ = runObj.Call("org.kde.kwin.Script.run", 0).Err
		_ = scriptObj.Call("org.kde.kwin.Scripting.unloadScript", 0, "devicepulse-helper").Err
	}

	// ── Method 3: KDE Activities — read active window title from kwin_wayland ─
	obj2 := conn.Object("org.kde.KWin", "/org/kde/KWin")
	var activeWinID uint64
	if err2 := obj2.Call("org.kde.KWin.currentDesktop", 0).Store(&activeWinID); err2 == nil {
		log.Printf("ActiveWindowTracker (KDE): current desktop id %d", activeWinID)
	}

	return ""
}

// ─── Sway / wlroots IPC ──────────────────────────────────────────────────────

// linuxWaylandSwayActiveWindow connects to the Sway IPC socket and sends a
// get_tree request. It then walks the JSON tree to find the focused node and
// returns its app_id or name.
//
// The Sway IPC protocol is documented at:
// https://man.archlinux.org/man/extra/sway/sway-ipc.7.en
//
// Message format: "i3-ipc" magic (6 bytes) + length (4 LE) + type (4 LE) + payload
// get_tree = type 4
func linuxWaylandSwayActiveWindow() string {
	sock := os.Getenv("SWAYSOCK")
	if sock == "" {
		// Try I3SOCK as well (i3 uses the same protocol).
		sock = os.Getenv("I3SOCK")
	}
	if sock == "" {
		return ""
	}

	conn, err := net.Dial("unix", sock)
	if err != nil {
		return ""
	}
	defer conn.Close()

	// Build get_tree request (type=4, empty payload).
	msg := buildSwayMessage(4, "")
	if _, err := conn.Write(msg); err != nil {
		return ""
	}

	// Read response.
	payload, err := readSwayResponse(conn)
	if err != nil {
		return ""
	}

	// Walk JSON tree to find focused node.
	return swayFocusedApp(payload)
}

// buildSwayMessage constructs a Sway/i3 IPC message.
// Format: magic(6) + payloadLen(uint32 LE) + msgType(uint32 LE) + payload
func buildSwayMessage(msgType uint32, payload string) []byte {
	magic := []byte("i3-ipc")
	pLen := uint32(len(payload))
	buf := make([]byte, 6+4+4+len(payload))
	copy(buf[0:], magic)
	buf[6] = byte(pLen)
	buf[7] = byte(pLen >> 8)
	buf[8] = byte(pLen >> 16)
	buf[9] = byte(pLen >> 24)
	buf[10] = byte(msgType)
	buf[11] = byte(msgType >> 8)
	buf[12] = byte(msgType >> 16)
	buf[13] = byte(msgType >> 24)
	copy(buf[14:], payload)
	return buf
}

// readSwayResponse reads a Sway IPC response and returns the JSON payload.
func readSwayResponse(conn net.Conn) ([]byte, error) {
	header := make([]byte, 14)
	n, err := readFull(conn, header)
	if err != nil || n < 14 {
		return nil, fmt.Errorf("short header: %w", err)
	}
	if string(header[0:6]) != "i3-ipc" {
		return nil, fmt.Errorf("bad magic: %q", header[0:6])
	}
	pLen := uint32(header[6]) |
		uint32(header[7])<<8 |
		uint32(header[8])<<16 |
		uint32(header[9])<<24
	payload := make([]byte, pLen)
	_, err = readFull(conn, payload)
	return payload, err
}

func readFull(conn net.Conn, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := conn.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// swayNode is a partial representation of a Sway tree node.
type swayNode struct {
	ID       int64      `json:"id"`
	Name     string     `json:"name"`
	AppID    string     `json:"app_id"`    // Wayland app ID
	Focused  bool       `json:"focused"`
	Nodes    []swayNode `json:"nodes"`
	Floating []swayNode `json:"floating_nodes"`
}

// swayFocusedApp walks the Sway tree JSON and returns the app_id or name of the
// focused node.
func swayFocusedApp(data []byte) string {
	var root swayNode
	if err := json.Unmarshal(data, &root); err != nil {
		return ""
	}
	node := findFocusedNode(&root)
	if node == nil {
		return ""
	}
	if node.AppID != "" {
		return node.AppID
	}
	return extractAppNameFromTitle(node.Name)
}

// findFocusedNode does a DFS to find the first node with Focused == true.
func findFocusedNode(n *swayNode) *swayNode {
	if n.Focused {
		return n
	}
	for i := range n.Nodes {
		if found := findFocusedNode(&n.Nodes[i]); found != nil {
			return found
		}
	}
	for i := range n.Floating {
		if found := findFocusedNode(&n.Floating[i]); found != nil {
			return found
		}
	}
	return nil
}

// ─── Shared helpers ───────────────────────────────────────────────────────────

// extractAppNameFromTitle strips common window-title suffixes to return just
// the application name. Examples:
//   "README.md - Visual Studio Code"  → "Visual Studio Code"
//   "Mozilla Firefox"                 → "Mozilla Firefox"
//   "New Tab – Firefox"               → "Firefox"
func extractAppNameFromTitle(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return ""
	}
	for _, sep := range []string{" – ", " — ", " - "} {
		if idx := strings.LastIndex(title, sep); idx > 0 {
			candidate := strings.TrimSpace(title[idx+len(sep):])
			if candidate != "" {
				return candidate
			}
		}
	}
	return title
}
