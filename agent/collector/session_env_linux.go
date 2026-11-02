//go:build linux

package collector

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	adoptMu   sync.Mutex
	adoptLast time.Time
)

// sessionEnvKeys are what a desktop session exports and a systemd user service
// started at default.target (before the session exists) does not have.
var sessionEnvKeys = []string{
	"XDG_CURRENT_DESKTOP", "XDG_SESSION_TYPE", "WAYLAND_DISPLAY", "DISPLAY", "XAUTHORITY",
}

// AdoptSessionEnv fills in missing desktop-session variables from another process
// of the same user that has them (gnome-shell, the session's apps). Without this
// the per-user window service sees no desktop at all: the GNOME extension is never
// installed and no focus source is found. It never overrides a variable that is
// already set, and does nothing once the environment is complete.
func AdoptSessionEnv() {
	if os.Getenv("XDG_CURRENT_DESKTOP") != "" && (os.Getenv("WAYLAND_DISPLAY") != "" || os.Getenv("DISPLAY") != "") {
		return
	}
	// The focus sampler calls this every 2 s; do not rescan /proc that often.
	adoptMu.Lock()
	if time.Since(adoptLast) < 15*time.Second {
		adoptMu.Unlock()
		return
	}
	adoptLast = time.Now()
	adoptMu.Unlock()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return
	}
	self := strconv.Itoa(os.Getuid())
	for _, e := range entries {
		pid := e.Name()
		if !e.IsDir() || pid == "" || pid[0] < '0' || pid[0] > '9' {
			continue
		}
		if getProcUID("/proc/"+pid+"/status") != self {
			continue
		}
		data, err := os.ReadFile("/proc/" + pid + "/environ")
		if err != nil {
			continue
		}
		env := parseEnviron(data)
		// XDG_CURRENT_DESKTOP is set by the session only, so it marks a session process.
		if env["XDG_CURRENT_DESKTOP"] == "" {
			continue
		}
		for _, k := range sessionEnvKeys {
			if os.Getenv(k) == "" && env[k] != "" {
				os.Setenv(k, env[k])
			}
		}
		return
	}
}

// parseEnviron parses the NUL-separated contents of /proc/<pid>/environ.
func parseEnviron(data []byte) map[string]string {
	out := map[string]string{}
	for _, kv := range strings.Split(string(data), "\x00") {
		if k, v, ok := strings.Cut(kv, "="); ok {
			out[k] = v
		}
	}
	return out
}
