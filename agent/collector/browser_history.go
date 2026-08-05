package collector

// BrowserHistory reads browser SQLite databases directly.
// No external binaries required — uses the pure-Go sqlite driver.
//
// Supported browsers (all platforms):
//   Chrome / Chromium — History SQLite db
//   Microsoft Edge    — Chromium-based, same format
//   Firefox           — places.sqlite
//   Safari            — History.db (macOS only)
//
// Cross-platform profile paths:
//   macOS:   ~/Library/Application Support/{browser}
//   Linux:   ~/.config/{browser}  or  ~/.mozilla/firefox
//   Windows: %LOCALAPPDATA%\{browser}\User Data

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type BrowserHistory struct{}

func (b *BrowserHistory) Name() string { return "BrowserHistory" }
func (b *BrowserHistory) Start() error { return nil }
func (b *BrowserHistory) Stop() error  { return nil }

type HistoryEntry struct {
	URL           string `json:"url"`
	Title         string `json:"title"`
	LastVisitTime int64  `json:"last_visit_time"`
	Browser       string `json:"browser"`
}

func (b *BrowserHistory) Collect() (map[string]interface{}, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("could not get home dir: %w", err)
	}

	var allEntries []HistoryEntry

	// ── Chromium-based browsers ───────────────────────────────────────────────
	for _, spec := range chromiumProfileDirs(homeDir) {
		allEntries = append(allEntries, fetchChromiumHistory(spec.path, spec.name)...)
	}

	// ── Firefox ───────────────────────────────────────────────────────────────
	for _, profilesDir := range firefoxProfilesDirs(homeDir) {
		allEntries = append(allEntries, fetchFirefoxHistory(profilesDir)...)
	}

	// ── Safari (macOS only) ───────────────────────────────────────────────────
	if runtime.GOOS == "darwin" {
		safariPath := filepath.Join(homeDir, "Library", "Safari", "History.db")
		allEntries = append(allEntries, fetchSafariHistory(safariPath)...)
	}

	if len(allEntries) == 0 {
		return map[string]interface{}{"top_recent_urls": []HistoryEntry{}}, nil
	}

	sort.Slice(allEntries, func(i, j int) bool {
		return allEntries[i].LastVisitTime > allEntries[j].LastVisitTime
	})
	if len(allEntries) > 10 {
		allEntries = allEntries[:10]
	}

	return map[string]interface{}{"top_recent_urls": allEntries}, nil
}

// ─── cross-platform profile path resolution ───────────────────────────────────

type browserSpec struct {
	name string
	path string // base directory (parent of Default/, Profile N/)
}

// chromiumProfileDirs returns base profile directories for all Chromium-based
// browsers on the current platform.
func chromiumProfileDirs(home string) []browserSpec {
	switch runtime.GOOS {
	case "darwin":
		base := filepath.Join(home, "Library", "Application Support")
		return []browserSpec{
			{name: "Chrome", path: filepath.Join(base, "Google", "Chrome")},
			{name: "Edge", path: filepath.Join(base, "Microsoft Edge")},
			{name: "Brave", path: filepath.Join(base, "BraveSoftware", "Brave-Browser")},
			{name: "Chromium", path: filepath.Join(base, "Chromium")},
			{name: "Opera", path: filepath.Join(base, "com.operasoftware.Opera")},
			{name: "Vivaldi", path: filepath.Join(base, "Vivaldi")},
		}
	case "linux":
		config := filepath.Join(home, ".config")
		snap := filepath.Join(home, "snap")
		return []browserSpec{
			{name: "Chrome", path: filepath.Join(config, "google-chrome")},
			{name: "Chromium", path: filepath.Join(config, "chromium")},
			{name: "Edge", path: filepath.Join(config, "microsoft-edge")},
			{name: "Brave", path: filepath.Join(config, "BraveSoftware", "Brave-Browser")},
			{name: "Opera", path: filepath.Join(config, "opera")},
			{name: "Vivaldi", path: filepath.Join(config, "vivaldi")},
			// Snap-installed Chrome
			{name: "Chrome (snap)", path: filepath.Join(snap, "chromium", "current", ".config", "chromium")},
		}
	case "windows":
		local := os.Getenv("LOCALAPPDATA")
		roaming := os.Getenv("APPDATA")
		_ = roaming
		return []browserSpec{
			{name: "Chrome", path: filepath.Join(local, "Google", "Chrome", "User Data")},
			{name: "Edge", path: filepath.Join(local, "Microsoft", "Edge", "User Data")},
			{name: "Brave", path: filepath.Join(local, "BraveSoftware", "Brave-Browser", "User Data")},
			{name: "Chromium", path: filepath.Join(local, "Chromium", "User Data")},
			{name: "Opera", path: filepath.Join(local, "Opera Software", "Opera Stable")},
			{name: "Vivaldi", path: filepath.Join(local, "Vivaldi", "User Data")},
		}
	default:
		return nil
	}
}

// firefoxProfilesDirs returns the Profiles directory for Firefox on each platform.
func firefoxProfilesDirs(home string) []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{
			filepath.Join(home, "Library", "Application Support", "Firefox", "Profiles"),
		}
	case "linux":
		return []string{
			filepath.Join(home, ".mozilla", "firefox"),
			filepath.Join(home, "snap", "firefox", "common", ".mozilla", "firefox"),
		}
	case "windows":
		roaming := os.Getenv("APPDATA")
		return []string{
			filepath.Join(roaming, "Mozilla", "Firefox", "Profiles"),
		}
	default:
		return nil
	}
}

// ─── Chromium history reader ──────────────────────────────────────────────────

func fetchChromiumHistory(baseDir, browserName string) []HistoryEntry {
	dirEntries, err := os.ReadDir(baseDir)
	if err != nil {
		return nil
	}

	type profileInfo struct {
		path    string
		modTime int64
	}
	var profiles []profileInfo

	for _, e := range dirEntries {
		if !e.IsDir() {
			continue
		}
		n := e.Name()
		if n != "Default" && !strings.HasPrefix(n, "Profile ") && !strings.HasPrefix(n, "Guest Profile") {
			continue
		}
		histPath := filepath.Join(baseDir, n, "History")
		if info, err := os.Stat(histPath); err == nil {
			profiles = append(profiles, profileInfo{path: histPath, modTime: info.ModTime().UnixNano()})
		}
	}

	sort.Slice(profiles, func(i, j int) bool { return profiles[i].modTime > profiles[j].modTime })
	if len(profiles) > 3 {
		profiles = profiles[:3]
	}

	var results []HistoryEntry
	for _, p := range profiles {
		results = append(results, queryChromiumDB(p.path, browserName)...)
	}
	return results
}

func queryChromiumDB(historyPath, browserName string) []HistoryEntry {
	tmp, err := os.CreateTemp("", "dp_chrome_*.db")
	if err != nil {
		return nil
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	if err := copyFile(historyPath, tmpPath); err != nil {
		return nil
	}

	db, err := sql.Open("sqlite", tmpPath+"?mode=ro&immutable=1")
	if err != nil {
		return nil
	}
	defer db.Close()

	rows, err := db.Query(`SELECT url, title, last_visit_time FROM urls ORDER BY last_visit_time DESC LIMIT 10`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var entries []HistoryEntry
	for rows.Next() {
		var entry HistoryEntry
		var title sql.NullString
		var chromiumTime int64
		if err := rows.Scan(&entry.URL, &title, &chromiumTime); err != nil {
			continue
		}
		if title.Valid {
			entry.Title = title.String
		}
		entry.Browser = browserName
		// Chromium stores microseconds since Windows FILETIME epoch (1601-01-01).
		unixMicros := chromiumTime - 11644473600000000
		if unixMicros < 0 {
			unixMicros = 0
		}
		entry.LastVisitTime = unixMicros * 1000
		entries = append(entries, entry)
	}
	return entries
}

// ─── Firefox history reader ───────────────────────────────────────────────────

func fetchFirefoxHistory(baseDir string) []HistoryEntry {
	dirEntries, err := os.ReadDir(baseDir)
	if err != nil {
		return nil
	}

	type profileInfo struct {
		path    string
		modTime int64
	}
	var profiles []profileInfo

	for _, e := range dirEntries {
		if !e.IsDir() {
			continue
		}
		dbPath := filepath.Join(baseDir, e.Name(), "places.sqlite")
		if info, err := os.Stat(dbPath); err == nil {
			profiles = append(profiles, profileInfo{path: dbPath, modTime: info.ModTime().UnixNano()})
		}
	}

	sort.Slice(profiles, func(i, j int) bool { return profiles[i].modTime > profiles[j].modTime })
	if len(profiles) > 2 {
		profiles = profiles[:2]
	}

	var results []HistoryEntry
	for _, p := range profiles {
		results = append(results, queryFirefoxDB(p.path)...)
	}
	return results
}

func queryFirefoxDB(historyPath string) []HistoryEntry {
	tmp, err := os.CreateTemp("", "dp_firefox_*.db")
	if err != nil {
		return nil
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	if err := copyFile(historyPath, tmpPath); err != nil {
		return nil
	}

	db, err := sql.Open("sqlite", tmpPath+"?mode=ro&immutable=1")
	if err != nil {
		return nil
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT url, title, last_visit_date
		FROM moz_places
		WHERE last_visit_date IS NOT NULL
		ORDER BY last_visit_date DESC
		LIMIT 10`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var entries []HistoryEntry
	for rows.Next() {
		var entry HistoryEntry
		var title sql.NullString
		var ffMicros int64
		if err := rows.Scan(&entry.URL, &title, &ffMicros); err != nil {
			continue
		}
		if title.Valid {
			entry.Title = title.String
		}
		entry.Browser = "Firefox"
		entry.LastVisitTime = ffMicros * 1000
		entries = append(entries, entry)
	}
	return entries
}

// ─── Safari history reader (macOS only) ───────────────────────────────────────

func fetchSafariHistory(safariPath string) []HistoryEntry {
	if _, err := os.Stat(safariPath); err != nil {
		return nil
	}

	tmp, err := os.CreateTemp("", "dp_safari_*.db")
	if err != nil {
		return nil
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	if err := copyFile(safariPath, tmpPath); err != nil {
		return nil
	}

	db, err := sql.Open("sqlite", tmpPath+"?mode=ro&immutable=1")
	if err != nil {
		return nil
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT i.url, v.title, v.visit_time
		FROM history_items i
		JOIN history_visits v ON i.id = v.history_item
		ORDER BY v.visit_time DESC
		LIMIT 10`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var entries []HistoryEntry
	for rows.Next() {
		var entry HistoryEntry
		var title sql.NullString
		var safariTime float64
		if err := rows.Scan(&entry.URL, &title, &safariTime); err != nil {
			continue
		}
		if title.Valid {
			entry.Title = title.String
		}
		entry.Browser = "Safari"
		// Safari: seconds since Jan 1 2001 (Cocoa epoch).
		unixSeconds := safariTime + 978307200
		entry.LastVisitTime = int64(unixSeconds * float64(time.Second))
		entries = append(entries, entry)
	}
	return entries
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	if err != nil {
		return err
	}
	return out.Sync()
}
