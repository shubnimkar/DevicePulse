//go:build darwin

package commander

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const macCGSessionPath = "/System/Library/CoreServices/Menu Extras/User.menu/Contents/Resources/CGSession"

// lockScreen locks the console session on macOS.
//
// The agent normally runs as a root LaunchDaemon, so we resolve the console
// user from /dev/console and run CGSession -suspend inside their GUI session
// via launchctl asuser. That shows the login window / screensaver immediately.
// If that fails (or no user is logged in) we fall back to pmset displaysleepnow,
// which locks only when "require password after sleep" is enabled.
func lockScreen() (string, string) {
	user, _ := exec.Command("stat", "-f", "%Su", "/dev/console").Output()
	uid, _ := exec.Command("stat", "-f", "%u", "/dev/console").Output()
	consoleUser := strings.TrimSpace(string(user))
	consoleUID := strings.TrimSpace(string(uid))

	if consoleUser != "" && consoleUser != "root" && consoleUID != "" && consoleUID != "0" {
		if _, err := execCommand("launchctl", "asuser", consoleUID, "sudo", "-u", consoleUser, macCGSessionPath, "-suspend"); err == nil {
			return StatusSuccess, fmt.Sprintf("CGSession -suspend for %s (uid %s)", consoleUser, consoleUID)
		}
	}
	// Agent running inside a user session already — call directly.
	if _, err := os.Stat(macCGSessionPath); err == nil {
		if _, err := execCommand(macCGSessionPath, "-suspend"); err == nil {
			return StatusSuccess, "CGSession -suspend (direct)"
		}
	}

	if _, err := execCommand("pmset", "displaysleepnow"); err == nil {
		return StatusSuccess, "display slept via pmset (screen locks only if 'require password after sleep' is enabled)"
	}
	return StatusFailed, "no working macOS lock mechanism (no console user, CGSession and pmset failed)"
}
