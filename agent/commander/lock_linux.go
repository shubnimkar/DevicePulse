//go:build linux

package commander

import (
	"fmt"
	"os/exec"
	"strings"
)

// lockScreen locks every local graphical session via systemd-logind.
//
//	loginctl lock-sessions — works from the root system service and signals
//	the desktop locker (gnome-screensaver / kscreensaver / swaylock ...) for
//	every active session via logind's Lock signal.
func lockScreen() (string, string) {
	if _, err := exec.LookPath("loginctl"); err == nil {
		if out, err := execCommand("loginctl", "lock-sessions"); err == nil {
			detail := "logind lock-sessions"
			if out != "" {
				detail += ": " + out
			}
			return StatusSuccess, detail
		}
		if out, err := execCommand("loginctl", "lock-session"); err == nil {
			return StatusSuccess, "logind lock-session: " + out
		}
	}

	// Fallback for desktop environments without logind locker integration.
	if _, err := exec.LookPath("xdg-screensaver"); err == nil {
		if _, err := exec.Command("xdg-screensaver", "lock").CombinedOutput(); err == nil {
			return StatusSuccess, "xdg-screensaver lock"
		}
	}
	if _, err := exec.LookPath("gnome-screensaver-command"); err == nil {
		if _, err := exec.Command("gnome-screensaver-command", "-l").CombinedOutput(); err == nil {
			return StatusSuccess, "gnome-screensaver lock"
		}
	}

	return StatusFailed,
		fmt.Sprintf("no working lock mechanism found (loginctl: %s)",
			func() string { out, _ := execCommand("loginctl", "lock-sessions"); return strings.TrimSpace(out) }())
}
