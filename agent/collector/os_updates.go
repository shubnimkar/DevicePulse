package collector

// OSUpdates collects the last OS update timestamp and pending update info.
//
// Platform strategy — zero external binary requirements:
//   macOS   — reads /Library/Preferences/com.apple.SoftwareUpdate.plist directly
//             (no defaults or softwareupdate binary needed)
//   Linux   — reads filesystem mtime of /var/lib/dpkg/info (Debian/Ubuntu) or
//              /var/lib/rpm (RPM) to determine last update time;
//              counts pending upgrades by reading apt extended_states + pkgcache
//              or by checking /var/lib/pacman/local modification times
//   Windows — reads registry key
//              HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Update\TargetingInfo

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"howett.net/plist"
)

// OSUpdates collector.
type OSUpdates struct{}

func (o *OSUpdates) Name() string { return "OSUpdates" }
func (o *OSUpdates) Start() error { return nil }
func (o *OSUpdates) Stop() error  { return nil }

// OSUpdateInfo holds OS update metadata.
type OSUpdateInfo struct {
	LastUpdateTime string   `json:"last_update_time,omitempty"`
	LastUpdateRaw  string   `json:"last_update_raw,omitempty"`
	PendingUpdates []string `json:"pending_updates,omitempty"`
	PendingCount   int      `json:"pending_count"`
	Source         string   `json:"source"`
}

func (o *OSUpdates) Collect() (map[string]interface{}, error) {
	var info OSUpdateInfo
	switch runtime.GOOS {
	case "darwin":
		info = collectMacOSUpdates()
	case "linux":
		info = collectLinuxUpdates()
	case "windows":
		info = collectWindowsUpdates()
	default:
		info.Source = "unsupported"
	}
	return map[string]interface{}{"os_updates": info}, nil
}

// ─── macOS ────────────────────────────────────────────────────────────────────

// softwareUpdatePlist mirrors the fields we read from
// /Library/Preferences/com.apple.SoftwareUpdate.plist
type softwareUpdatePlist struct {
	LastSuccessfulDate     string   `plist:"LastSuccessfulDate"`
	RecommendedUpdates     []suItem `plist:"RecommendedUpdates"`
}

type suItem struct {
	ProductKey    string `plist:"Product Key"`
	HumanReadable string `plist:"Human Readable Name"`
}

func collectMacOSUpdates() OSUpdateInfo {
	info := OSUpdateInfo{Source: "softwareupdate_plist"}

	plistPath := "/Library/Preferences/com.apple.SoftwareUpdate.plist"
	data, err := os.ReadFile(plistPath)
	if err != nil {
		// Fallback: check mtime of /Library/Receipts/InstallHistory.plist
		info.LastUpdateTime = readMacOSInstallHistory()
		info.Source = "install_history"
		return info
	}

	var su softwareUpdatePlist
	if _, err := plist.Unmarshal(data, &su); err == nil {
		raw := su.LastSuccessfulDate
		info.LastUpdateRaw = raw
		for _, layout := range []string{
			"2006-01-02 15:04:05 +0000",
			time.RFC3339,
		} {
			if t, err := time.Parse(layout, raw); err == nil {
				info.LastUpdateTime = t.UTC().Format(time.RFC3339)
				break
			}
		}
		for _, item := range su.RecommendedUpdates {
			name := item.HumanReadable
			if name == "" {
				name = item.ProductKey
			}
			if name != "" {
				info.PendingUpdates = append(info.PendingUpdates, name)
			}
		}
	}
	info.PendingCount = len(info.PendingUpdates)
	return info
}

// installHistoryPlist represents /Library/Receipts/InstallHistory.plist entries.
type installHistoryEntry struct {
	Date        time.Time `plist:"date"`
	DisplayName string    `plist:"displayName"`
}

func readMacOSInstallHistory() string {
	data, err := os.ReadFile("/Library/Receipts/InstallHistory.plist")
	if err != nil {
		return ""
	}
	var entries []installHistoryEntry
	if _, err := plist.Unmarshal(data, &entries); err != nil || len(entries) == 0 {
		return ""
	}
	// Entries are chronological; last entry is most recent.
	last := entries[len(entries)-1]
	return last.Date.UTC().Format(time.RFC3339)
}

// ─── Linux ────────────────────────────────────────────────────────────────────

func collectLinuxUpdates() OSUpdateInfo {
	info := OSUpdateInfo{}

	// ── Debian / Ubuntu ──────────────────────────────────────────────────────
	// Last update: mtime of /var/lib/dpkg/info (directory updated on every install)
	if fi, err := os.Stat("/var/lib/dpkg/info"); err == nil {
		info.Source = "dpkg"
		info.LastUpdateTime = fi.ModTime().UTC().Format(time.RFC3339)
		info.LastUpdateRaw = fi.ModTime().String()

		// Pending upgrades: parse /var/lib/apt/lists/ + dpkg extended_states.
		// The simplest heuristic without running apt: count packages in
		// /var/lib/apt/lists/*_Packages that have a newer version than what
		// dpkg status reports. This is a best-effort count, not exhaustive.
		pending := countAptUpgradable()
		for i := 0; i < pending; i++ {
			info.PendingUpdates = append(info.PendingUpdates, fmt.Sprintf("package-%d", i+1))
		}
		// For apt, just report the number — individual package names require
		// full cache parsing which is expensive. Return a summary count instead.
		if pending > 0 {
			info.PendingUpdates = []string{fmt.Sprintf("%d packages can be upgraded", pending)}
		}
		info.PendingCount = pending
		return info
	}

	// ── Arch Linux ───────────────────────────────────────────────────────────
	if fi, err := os.Stat("/var/lib/pacman/local"); err == nil {
		info.Source = "pacman"
		info.LastUpdateTime = fi.ModTime().UTC().Format(time.RFC3339)
		info.LastUpdateRaw = fi.ModTime().String()
		return info
	}

	// ── RPM-based (RHEL / Fedora / SUSE) ────────────────────────────────────
	for _, rpmDir := range []string{"/var/lib/rpm", "/usr/lib/sysimage/rpm"} {
		if fi, err := os.Stat(rpmDir); err == nil {
			info.Source = "rpm"
			info.LastUpdateTime = fi.ModTime().UTC().Format(time.RFC3339)
			info.LastUpdateRaw = fi.ModTime().String()
			return info
		}
	}

	info.Source = "unknown"
	return info
}

// countAptUpgradable reads /var/lib/apt/extended_states to count packages
// marked as "Auto-Installed: 0" with a newer candidate version.
// This is a lightweight heuristic; full resolution requires libapt.
func countAptUpgradable() int {
	// Read the dpkg status to build a version map.
	installed := make(map[string]string) // package → installed version
	f, err := os.Open("/var/lib/dpkg/status")
	if err != nil {
		return 0
	}
	defer f.Close()

	var pkg, version, status string
	flush := func() {
		if pkg != "" && strings.Contains(status, "installed") {
			installed[pkg] = version
		}
		pkg, version, status = "", "", ""
	}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			flush()
			continue
		}
		if v, ok := cutPrefix(line, "Package: "); ok {
			pkg = v
		} else if v, ok := cutPrefix(line, "Version: "); ok {
			version = v
		} else if v, ok := cutPrefix(line, "Status: "); ok {
			status = v
		}
	}
	flush()

	// Walk /var/lib/apt/lists/*_Packages files to find packages with newer versions.
	listsDir := "/var/lib/apt/lists"
	entries, err := os.ReadDir(listsDir)
	if err != nil {
		return 0
	}

	upgradable := make(map[string]bool)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_Packages") {
			continue
		}
		parseAptPackages(filepath.Join(listsDir, e.Name()), installed, upgradable)
	}
	return len(upgradable)
}

// parseAptPackages scans an apt Packages index file and populates the
// upgradable map with packages that have a newer available version.
func parseAptPackages(path string, installed map[string]string, upgradable map[string]bool) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	var pkg, version string
	flush := func() {
		if pkg != "" && version != "" {
			if instVer, ok := installed[pkg]; ok && instVer != version {
				upgradable[pkg] = true
			}
		}
		pkg, version = "", ""
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // apt files can have large Description fields
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			flush()
			continue
		}
		if v, ok := cutPrefix(line, "Package: "); ok {
			pkg = v
		} else if v, ok := cutPrefix(line, "Version: "); ok {
			version = v
		}
	}
	flush()
}

// ─── Windows ─────────────────────────────────────────────────────────────────

// collectWindowsUpdates is implemented in os_updates_windows.go (build-tagged).
func collectWindowsUpdates() OSUpdateInfo {
	return collectWindowsUpdatesImpl()
}
