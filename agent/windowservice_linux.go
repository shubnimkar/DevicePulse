//go:build linux

package main

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const windowUnitPath = "/usr/lib/systemd/user/devicepulse-agent-window.service"

// The unit ties itself to the graphical session. It used to be WantedBy
// default.target with PartOf=graphical-session.target: the logout stopped it, but
// a user manager that outlives the logout never reaches default.target again, so
// the service stayed dead until reboot. WantedBy=graphical-session.target starts it
// at every login, with the session environment already in place.
const windowUnitTemplate = `[Unit]
Description=DevicePulse Agent — Active Window Tracker
After=graphical-session.target
PartOf=graphical-session.target

[Service]
Type=simple
ExecStart=%s
Environment=DEVICEPULSE_MODE=window_only
Environment=DEVICEPULSE_DATA_DIR=/var/lib/devicepulse
Restart=on-failure
RestartSec=5s
NoNewPrivileges=true

[Install]
WantedBy=graphical-session.target
`

func windowUnitContent(exe string) string { return fmt.Sprintf(windowUnitTemplate, exe) }

// maintainWindowService keeps the per-user window unit installed, enabled for all
// users and running in every logged-in graphical session. Only the root service
// can do this, and agents arrive by self-update, not by reinstalling the package.
func maintainWindowService() {
	if os.Geteuid() != 0 {
		return
	}
	for {
		if err := ensureWindowService(); err != nil {
			log.Printf("Window service: %v", err)
		}
		time.Sleep(10 * time.Minute)
	}
}

func ensureWindowService() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return nil // not a systemd machine
	}

	want := []byte(windowUnitContent(exe))
	changed := false
	if have, err := os.ReadFile(windowUnitPath); err != nil || !bytes.Equal(have, want) {
		tmp := windowUnitPath + ".tmp"
		if err := os.WriteFile(tmp, want, 0o644); err != nil {
			return fmt.Errorf("write unit: %w", err)
		}
		if err := os.Rename(tmp, windowUnitPath); err != nil {
			os.Remove(tmp)
			return fmt.Errorf("install unit: %w", err)
		}
		changed = true
		log.Printf("Window service: installed %s", windowUnitPath)
	}

	if changed {
		// Drop the old default.target link, then link the unit under its new target.
		runSystemctl("--global", "disable", "devicepulse-agent-window")
	}
	if out, err := runSystemctl("--global", "enable", "devicepulse-agent-window"); err != nil {
		return fmt.Errorf("enable globally: %v: %s", err, out)
	}

	for _, user := range loggedInUsers() {
		machine := user + "@.host"
		if changed {
			runSystemctl("--user", "-M", machine, "daemon-reload")
		}
		// Only users in a graphical session get it; an ssh-only user has no window to track.
		if out, _ := runSystemctl("--user", "-M", machine, "is-active", "graphical-session.target"); out != "active" {
			continue
		}
		if out, err := runSystemctl("--user", "-M", machine, "start", "devicepulse-agent-window"); err != nil {
			log.Printf("Window service: start for %s: %v: %s", user, err, out)
		}
	}
	return nil
}

func runSystemctl(args ...string) (string, error) {
	out, err := exec.Command("systemctl", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// loggedInUsers returns human users (UID >= 1000) with a login session, from
// `loginctl list-users` lines of the form "UID USER LINGER STATE".
func loggedInUsers() []string {
	out, err := exec.Command("loginctl", "list-users", "--no-legend").Output()
	if err != nil {
		return nil
	}
	return parseLoginctlUsers(string(out))
}

func parseLoginctlUsers(out string) []string {
	var users []string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		var uid int
		if _, err := fmt.Sscanf(f[0], "%d", &uid); err != nil || uid < 1000 {
			continue
		}
		users = append(users, f[1])
	}
	return users
}
