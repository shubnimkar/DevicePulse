//go:build linux
// +build linux

package collector

// linuxX11ActiveWindow returns the focused application name by speaking the
// X11 wire protocol directly via github.com/jezek/xgb (pure-Go XCB).
//
// Algorithm:
//  1. Connect to the X server at $DISPLAY.
//  2. Read the root window property _NET_ACTIVE_WINDOW → get the active window ID.
//  3. Read _NET_WM_NAME (UTF-8) on that window; fall back to WM_NAME (latin-1).
//  4. If the name is a long path or looks like a title bar string, extract just
//     the application name from WM_CLASS (instance / class pair).
//  5. Return a clean, human-readable app name.
//
// This requires no external binaries whatsoever — only the X socket at
// /tmp/.X11-unix/X<n> which is always present in an X11 session.

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// linuxX11ActiveWindow is the entry point called from active_window.go.
func linuxX11ActiveWindow() string {
	display := os.Getenv("DISPLAY")
	if display == "" {
		return ""
	}

	conn, err := xgb.NewConnDisplay(display)
	if err != nil {
		// X server not reachable (e.g. running as a different user without
		// XAUTHORITY). Silently skip — other strategies will be tried.
		return ""
	}
	defer conn.Close()

	setup := xproto.Setup(conn)
	if setup == nil || len(setup.Roots) == 0 {
		return ""
	}
	root := setup.Roots[0].Root

	// ── Intern atoms ─────────────────────────────────────────────────────────
	atoms, err := internAtoms(conn, []string{
		"_NET_ACTIVE_WINDOW",
		"_NET_WM_NAME",
		"WM_NAME",
		"WM_CLASS",
		"UTF8_STRING",
	})
	if err != nil {
		log.Printf("ActiveWindowTracker (X11): intern atoms: %v", err)
		return ""
	}
	netActiveWindow := atoms["_NET_ACTIVE_WINDOW"]
	netWMName       := atoms["_NET_WM_NAME"]
	wmName          := atoms["WM_NAME"]
	wmClass         := atoms["WM_CLASS"]
	utf8String      := atoms["UTF8_STRING"]

	// ── Step 1: read root _NET_ACTIVE_WINDOW ─────────────────────────────────
	activeWinID, err := readCardinal(conn, root, netActiveWindow)
	if err != nil || activeWinID == 0 {
		return ""
	}
	win := xproto.Window(activeWinID)

	// ── Step 2: try _NET_WM_NAME (UTF-8) ─────────────────────────────────────
	name, err := readStringProp(conn, win, netWMName, utf8String)
	if err != nil || name == "" {
		// Fallback: WM_NAME (ISO-8859-1 / STRING)
		name, _ = readStringProp(conn, win, wmName, xproto.AtomString)
	}

	// ── Step 3: extract clean app name from WM_CLASS ──────────────────────────
	// WM_CLASS contains two NUL-terminated strings: instance name and class name.
	// The class name is the most reliable human-readable identifier.
	class, classErr := readStringProp(conn, win, wmClass, xproto.AtomString)
	appName := extractAppName(name, class, classErr)

	return appName
}

// ── helpers ──────────────────────────────────────────────────────────────────

// internAtoms interns a list of atom names in one round-trip batch and returns
// a map of name → xproto.Atom.
func internAtoms(conn *xgb.Conn, names []string) (map[string]xproto.Atom, error) {
	cookies := make([]xproto.InternAtomCookie, len(names))
	for i, name := range names {
		cookies[i] = xproto.InternAtomUnchecked(conn, false, uint16(len(name)), name)
	}

	result := make(map[string]xproto.Atom, len(names))
	for i, name := range names {
		reply, err := cookies[i].Reply()
		if err != nil {
			return nil, fmt.Errorf("intern atom %q: %w", name, err)
		}
		result[name] = reply.Atom
	}
	return result, nil
}

// readCardinal reads a single CARDINAL (uint32) window property.
func readCardinal(conn *xgb.Conn, win xproto.Window, atom xproto.Atom) (uint32, error) {
	reply, err := xproto.GetProperty(conn, false, win, atom,
		xproto.AtomCardinal, 0, 1).Reply()
	if err != nil {
		return 0, err
	}
	if reply.Format != 32 || len(reply.Value) < 4 {
		return 0, fmt.Errorf("unexpected format %d or length %d", reply.Format, len(reply.Value))
	}
	// Little-endian uint32
	v := uint32(reply.Value[0]) |
		uint32(reply.Value[1])<<8 |
		uint32(reply.Value[2])<<16 |
		uint32(reply.Value[3])<<24
	return v, nil
}

// readStringProp reads a string property from a window.
// typ should be xproto.AtomString or the UTF8_STRING atom.
func readStringProp(conn *xgb.Conn, win xproto.Window,
	prop xproto.Atom, typ xproto.Atom) (string, error) {

	reply, err := xproto.GetProperty(conn, false, win, prop,
		typ, 0, 512).Reply()
	if err != nil {
		return "", err
	}
	if len(reply.Value) == 0 {
		return "", nil
	}
	return string(reply.Value), nil
}

// extractAppName derives a clean application name from the window title and
// WM_CLASS value.
//
// WM_CLASS stores two NUL-separated strings: "<instance>\x00<class>\x00".
// The class string (second part) is the canonical application identifier and
// is preferred over the title which usually contains document/tab names.
func extractAppName(title, wmClassStr string, wmClassErr error) string {
	// Parse WM_CLASS: two NUL-terminated strings.
	if wmClassErr == nil && wmClassStr != "" {
		parts := strings.SplitN(wmClassStr, "\x00", 3)
		// parts[0] = instance (e.g. "firefox"), parts[1] = class (e.g. "Firefox")
		if len(parts) >= 2 && parts[1] != "" {
			return strings.TrimSpace(parts[1])
		}
		if len(parts) >= 1 && parts[0] != "" {
			return strings.TrimSpace(parts[0])
		}
	}

	// Fall back to the window title, but strip trailing " - AppName" suffixes
	// commonly used by browsers/IDEs.
	if title != "" {
		// Many apps use "Document – App Name" or "Document - App Name"
		for _, sep := range []string{" – ", " — ", " - "} {
			if idx := strings.LastIndex(title, sep); idx > 0 {
				candidate := strings.TrimSpace(title[idx+len(sep):])
				if candidate != "" {
					return candidate
				}
			}
		}
		return strings.TrimSpace(title)
	}
	return ""
}
