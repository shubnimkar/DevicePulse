package commander

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Quarantine blocks the host's network. If the API's addresses change, or the
// release command is lost, an isolated host could never be released remotely
// (and on Windows and macOS the rules survive a reboot). So every quarantine
// carries a deadline: the agent releases itself when it passes, including after a
// restart. The server sends the duration in params.duration_minutes.
const quarantineStateFile = "quarantine_until"

var (
	quarantineMu    sync.Mutex
	quarantineTimer *time.Timer
)

func quarantineStatePath() string {
	if wipeDataDir == "" {
		return ""
	}
	return filepath.Join(wipeDataDir, quarantineStateFile)
}

// quarantineDuration reads params.duration_minutes (bounded 1 minute to 30 days).
func quarantineDuration(params map[string]interface{}) time.Duration {
	minutes, ok := params["duration_minutes"].(float64)
	if !ok || minutes < 1 {
		return 24 * time.Hour
	}
	if minutes > 30*24*60 {
		minutes = 30 * 24 * 60
	}
	return time.Duration(minutes * float64(time.Minute))
}

// armQuarantineDeadline records the deadline on disk and schedules the release.
func armQuarantineDeadline(d time.Duration) {
	quarantineMu.Lock()
	defer quarantineMu.Unlock()
	if quarantineTimer != nil {
		quarantineTimer.Stop()
	}
	if path := quarantineStatePath(); path != "" {
		_ = os.WriteFile(path, []byte(time.Now().Add(d).UTC().Format(time.RFC3339)), 0600)
	}
	quarantineTimer = time.AfterFunc(d, autoReleaseQuarantine)
}

func clearQuarantineDeadline() {
	quarantineMu.Lock()
	defer quarantineMu.Unlock()
	if quarantineTimer != nil {
		quarantineTimer.Stop()
		quarantineTimer = nil
	}
	if path := quarantineStatePath(); path != "" {
		_ = os.Remove(path)
	}
}

func autoReleaseQuarantine() {
	status, detail := quarantineRelease()
	logLine(fmt.Sprintf("quarantine deadline reached, auto-release: %s (%s)", status, detail))
	if status == StatusSuccess {
		clearQuarantineDeadline()
	}
}

// ResumeQuarantineDeadline re-arms (or fires) a deadline that survived a restart.
// Call it once at startup, after SetDataDir.
func ResumeQuarantineDeadline() {
	path := quarantineStatePath()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	until, err := time.Parse(time.RFC3339, strings.TrimSpace(string(data)))
	if err != nil {
		_ = os.Remove(path) // unreadable deadline: do not leave a permanent marker
		return
	}
	quarantineMu.Lock()
	defer quarantineMu.Unlock()
	if quarantineTimer != nil {
		quarantineTimer.Stop()
	}
	quarantineTimer = time.AfterFunc(time.Until(until), autoReleaseQuarantine) // fires at once if already past
}
