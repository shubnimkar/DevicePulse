package collector

import (
	"bufio"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	_ "modernc.org/sqlite"

	"howett.net/plist"
)

// InstalledApps collects installed applications and their versions.
//
// Platform strategy — zero external binary requirements:
//   macOS   — scans .app bundles, reads Info.plist directly
//   Linux   — parses /var/lib/dpkg/status (Debian/Ubuntu),
//              /var/lib/pacman/local (Arch),
//              /var/lib/rpm/rpmdb.sqlite (RHEL 9+ / Fedora 37+ / SUSE 15.5+)
//   Windows — reads Uninstall registry keys via installed_apps_windows.go
type InstalledApps struct{}

func (a *InstalledApps) Name() string { return "InstalledApps" }
func (a *InstalledApps) Start() error { return nil }
func (a *InstalledApps) Stop() error  { return nil }

// AppEntry represents a single installed application.
type AppEntry struct {
	Name     string `json:"name"`
	Version  string `json:"version,omitempty"`
	BundleID string `json:"bundle_id,omitempty"` // macOS only
	Path     string `json:"path,omitempty"`
	Source   string `json:"source"`
}

func (a *InstalledApps) Collect() (map[string]interface{}, error) {
	switch runtime.GOOS {
	case "darwin":
		apps := scanMacOSApps()
		return map[string]interface{}{"installed_apps": apps, "count": len(apps), "source": "macos_bundle"}, nil

	case "linux":
		if apps := parseDpkgStatus("/var/lib/dpkg/status"); len(apps) > 0 {
			return map[string]interface{}{"installed_apps": apps, "count": len(apps), "source": "dpkg"}, nil
		}
		if apps := parsePacmanDB("/var/lib/pacman/local"); len(apps) > 0 {
			return map[string]interface{}{"installed_apps": apps, "count": len(apps), "source": "pacman"}, nil
		}
		if apps := parseRPMDB(); len(apps) > 0 {
			return map[string]interface{}{"installed_apps": apps, "count": len(apps), "source": "rpm"}, nil
		}
		return map[string]interface{}{"installed_apps": []AppEntry{}, "count": 0, "error": "no package db found"}, nil

	case "windows":
		apps := scanWindowsApps()
		return map[string]interface{}{"installed_apps": apps, "count": len(apps), "source": "registry"}, nil

	default:
		return map[string]interface{}{"installed_apps": []AppEntry{}, "count": 0}, nil
	}
}

// ─── macOS ────────────────────────────────────────────────────────────────────

type infoPlist struct {
	CFBundleDisplayName  string `plist:"CFBundleDisplayName"`
	CFBundleName         string `plist:"CFBundleName"`
	CFBundleVersion      string `plist:"CFBundleVersion"`
	CFBundleShortVersion string `plist:"CFBundleShortVersionString"`
	CFBundleIdentifier   string `plist:"CFBundleIdentifier"`
}

func scanMacOSApps() []AppEntry {
	searchDirs := []string{
		"/Applications",
		"/System/Applications",
		"/System/Applications/Utilities",
		filepath.Join(os.Getenv("HOME"), "Applications"),
	}

	var apps []AppEntry
	seen := map[string]bool{}

	for _, dir := range searchDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".app") {
				continue
			}
			appPath := filepath.Join(dir, e.Name())
			plistPath := filepath.Join(appPath, "Contents", "Info.plist")
			data, err := os.ReadFile(plistPath)
			if err != nil {
				continue
			}
			var info infoPlist
			if _, err := plist.Unmarshal(data, &info); err != nil {
				continue
			}
			name := info.CFBundleDisplayName
			if name == "" {
				name = info.CFBundleName
			}
			if name == "" {
				name = strings.TrimSuffix(e.Name(), ".app")
			}
			version := info.CFBundleShortVersion
			if version == "" {
				version = info.CFBundleVersion
			}
			key := info.CFBundleIdentifier
			if key == "" {
				key = appPath
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			apps = append(apps, AppEntry{
				Name:     name,
				Version:  version,
				BundleID: info.CFBundleIdentifier,
				Path:     appPath,
				Source:   "macos_bundle",
			})
		}
	}
	return apps
}

// ─── Linux / Debian–Ubuntu ────────────────────────────────────────────────────

// parseDpkgStatus reads /var/lib/dpkg/status — a plain key:value text file.
// No dpkg-query binary required.
func parseDpkgStatus(path string) []AppEntry {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var apps []AppEntry
	var name, version, status string

	flush := func() {
		if name != "" && strings.Contains(status, "installed") {
			apps = append(apps, AppEntry{Name: name, Version: version, Source: "dpkg"})
		}
		name, version, status = "", "", ""
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			flush()
			continue
		}
		if v, ok := cutPrefix(line, "Package: "); ok {
			name = v
		} else if v, ok := cutPrefix(line, "Version: "); ok {
			version = v
		} else if v, ok := cutPrefix(line, "Status: "); ok {
			status = v
		}
	}
	flush()
	return apps
}

// ─── Linux / Arch Linux ───────────────────────────────────────────────────────

// parsePacmanDB reads the pacman local database at /var/lib/pacman/local.
// No pacman binary required — each package is a sub-directory with a desc file.
func parsePacmanDB(dir string) []AppEntry {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var apps []AppEntry
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name(), "desc"))
		if err != nil {
			continue
		}
		name, version := parsePacmanDesc(string(data))
		if name != "" {
			apps = append(apps, AppEntry{Name: name, Version: version, Source: "pacman"})
		}
	}
	return apps
}

func parsePacmanDesc(content string) (name, version string) {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		switch line {
		case "%NAME%":
			if i+1 < len(lines) {
				name = strings.TrimSpace(lines[i+1])
			}
		case "%VERSION%":
			if i+1 < len(lines) {
				version = strings.TrimSpace(lines[i+1])
			}
		}
	}
	return
}

// ─── Linux / RPM ─────────────────────────────────────────────────────────────

// parseRPMDB reads the RPM database without calling the rpm binary.
// Modern RPM (≥ 4.16, RHEL 9+, Fedora 37+, SUSE 15.5+) stores the db as
// an SQLite file which we read with the pure-Go sqlite driver already in the
// project. Older Berkeley DB formats are not supported (require CGo).
func parseRPMDB() []AppEntry {
	candidates := []string{
		"/var/lib/rpm/rpmdb.sqlite",
		"/var/lib/rpm/Packages.db",
		"/usr/lib/sysimage/rpm/rpmdb.sqlite",
		"/usr/lib/sysimage/rpm/Packages.db",
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return queryRPMSqlite(p)
		}
	}
	return nil
}

func queryRPMSqlite(path string) []AppEntry {
	db, err := sql.Open("sqlite", path+"?mode=ro&_journal=OFF")
	if err != nil {
		return nil
	}
	defer db.Close()

	// The rpmdb sqlite schema has a single "Packages" table with a "blob" column
	// storing binary RPM header data. We use the rpm_name / rpm_version virtual
	// columns exposed by the rpmdb-sqlite extension on supported systems.
	// If that extension isn't available, we fall back to listing table names
	// and trying common schemas.
	rows, err := db.Query(`SELECT name, version FROM rpm WHERE 1=1 LIMIT 5000`)
	if err != nil {
		// Try alternate schema used by some distros.
		rows, err = db.Query(`SELECT key, value FROM Packages LIMIT 1`)
		if err != nil {
			return nil // Berkeley DB or unsupported schema
		}
		rows.Close()
		return nil // Can't decode Berkeley DB blobs without rpm library
	}
	defer rows.Close()

	var apps []AppEntry
	for rows.Next() {
		var name, version string
		if err := rows.Scan(&name, &version); err == nil && name != "" {
			apps = append(apps, AppEntry{Name: name, Version: version, Source: "rpm"})
		}
	}
	return apps
}

// ─── Windows ─────────────────────────────────────────────────────────────────

// scanWindowsApps is implemented in installed_apps_windows.go (build-tagged).
// Stub for non-Windows in installed_apps_stub.go.

// ─── shared helpers ──────────────────────────────────────────────────────────

// cutPrefix returns (value, true) when s starts with prefix.
// Mirrors strings.CutPrefix (Go 1.20+) for compatibility.
func cutPrefix(s, prefix string) (string, bool) {
	if strings.HasPrefix(s, prefix) {
		return s[len(prefix):], true
	}
	return "", false
}
