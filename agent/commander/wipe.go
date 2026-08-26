package commander

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// wipeDataDir is resolved by the agent main package via SetDataDir.
var wipeDataDir string

// SetDataDir tells the commander where the agent keeps its state so the
// corporate wipe can purge it.
func SetDataDir(dir string) {
	wipeDataDir = dir
}

// runWipe performs a CORPORATE WIPE of the agent itself:
//
//  1. Purge all local agent state (credentials, telemetry queue, history
//     cursors, logs) — device user data is NOT touched; this is not a
//     factory reset.
//  2. Stop + disable the agent services so nothing restarts it.
//  3. Remove the LaunchDaemon plist / systemd units where possible.
//  4. Best-effort delete of the agent binary (deferred on Windows because a
//     running image cannot be unlinked).
//
// The server revokes the device credential when the success result for this
// command is recorded, so a wiped endpoint can never ingest again.
func runWipe() {
	var notes []string

	dir := wipeDataDir
	if dir == "" {
		switch runtime.GOOS {
		case "windows":
			dir = filepath.Join(os.Getenv("ProgramData"), "DevicePulse", "Agent")
		default:
			dir = "/var/lib/devicepulse"
		}
	}

	// 1. Purge local state.
	for _, name := range []string{
		"registration.json",
		"browser_history_state.json",
		"devicepulse.db",
		"devicepulse.db-wal",
		"devicepulse.db-shm",
		"agent.log",
	} {
		if err := os.Remove(filepath.Join(dir, name)); err == nil {
			notes = append(notes, "removed "+name)
		}
	}
	os.Remove(dir) // remove the directory too if now empty

	// 2+3. Disable services / launch jobs per platform.
	notes = append(notes, wipeDisableServices()...)

	// 4. Remove our own binary (best effort).
	if exePath, err := os.Executable(); err == nil {
		exePath, _ = filepath.EvalSymlinks(exePath)
		switch runtime.GOOS {
		case "windows":
			// A running PE image cannot be deleted; schedule removal shortly
			// after this process exits via a detached cmd window-less task.
			scheduleWindowsDelete(exePath)
			notes = append(notes, "binary deletion scheduled: "+exePath)
		default:
			if err := os.Remove(exePath); err == nil {
				notes = append(notes, "removed binary "+exePath)
			} else {
				notes = append(notes, fmt.Sprintf("binary removal failed (%v) — uninstall via package manager", err))
			}
		}
	}

	logLine("corporate wipe complete: " + strings.Join(notes, "; "))
}

func wipeDisableServices() []string {
	var notes []string
	try := func(label string, name string, args ...string) {
		if out, err := execCommand(name, args...); err != nil {
			notes = append(notes, fmt.Sprintf("%s: %s", label, firstLineOr(out, "failed")))
		} else {
			notes = append(notes, label+" ok")
		}
	}

	switch runtime.GOOS {
	case "linux":
		for _, unit := range []string{"devicepulse-agent.service", "devicepulse-agent-window.service"} {
			try("disable "+unit, "systemctl", "disable", "--now", unit)
		}
	case "darwin":
		const plist = "/Library/LaunchDaemons/io.devicepulse.agent.plist"
		if _, err := execCommand("launchctl", "bootout", "system/io.devicepulse.agent"); err != nil {
			execCommand("launchctl", "unload", "-w", plist) // legacy fallback
		}
		if err := os.Remove(plist); err == nil {
			notes = append(notes, "removed "+plist)
		}
	case "windows":
		for _, svc := range []string{"DevicePulse Agent", "devicepulse-agent", "devicepulse-agent-window"} {
			try("stop "+svc, "cmd", "/c", "sc", "stop", svc)
			try("delete "+svc, "cmd", "/c", "sc", "delete", svc)
		}
	}
	return notes
}

// scheduleWindowsDelete removes the binary after a short delay using a hidden,
// detached cmd instance that outlives this process.
func scheduleWindowsDelete(path string) {
	go func() {
		time.Sleep(2 * time.Second)
		execCommand("cmd", "/c", "ping", "-n", "4", "127.0.0.1", ">nul", "&", "del", "/f", "/q", path)
	}()
}

func firstLineOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return firstLine(s)
}
