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
//
// When running as root (e.g. installed as a systemd service), os.UserHomeDir()
// returns /root which has no browser profiles. Instead we enumerate all real
// human user home directories from /etc/passwd (UID >= 1000, shell not nologin)
// and scan each one.

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type BrowserHistory struct {
	statePath      string
	stateLoaded    bool
	sentEntries    map[string]int64
	pendingEntries []HistoryEntry
}

func (b *BrowserHistory) Name() string { return "BrowserHistory" }
func (b *BrowserHistory) Start() error { return nil }
func (b *BrowserHistory) Stop() error  { return nil }

const maxBrowserHistoryScanEntries = 5000
const maxBrowserHistoryRecentSnapshotEntries = 200
const maxBrowserHistoryUploadEntries = 5000
const maxBrowserHistorySentStateEntries = 10000

type browserHistoryState struct {
	LastSyncedVisit int64            `json:"last_synced_visit,omitempty"`
	SentEntries     map[string]int64 `json:"sent_entries,omitempty"`
}

type HistoryEntry struct {
	URL           string `json:"url"`
	Title         string `json:"title"`
	LastVisitTime int64  `json:"last_visit_time"`
	Browser       string `json:"browser"`
}

func NewBrowserHistory(statePath string) *BrowserHistory {
	return &BrowserHistory{statePath: statePath}
}

func (b *BrowserHistory) Collect() (map[string]interface{}, error) {
	b.loadState()

	homeDirs := resolveHomeDirs()
	if len(homeDirs) == 0 {
		b.pendingEntries = nil
		return map[string]interface{}{
			"top_recent_urls":     []HistoryEntry{},
			"new_history_entries": []HistoryEntry{},
			"sync_type":           "incremental",
		}, nil
	}

	var allEntries []HistoryEntry

	for _, homeDir := range homeDirs {
		// ── Chromium-based browsers ───────────────────────────────────────────
		for _, spec := range chromiumProfileDirs(homeDir) {
			allEntries = append(allEntries, fetchChromiumHistory(spec.path, spec.name)...)
		}

		// ── Firefox ───────────────────────────────────────────────────────────
		for _, profilesDir := range firefoxProfilesDirs(homeDir) {
			allEntries = append(allEntries, fetchFirefoxHistory(profilesDir)...)
		}

		// ── Safari (macOS only) ───────────────────────────────────────────────
		if runtime.GOOS == "darwin" {
			safariPath := filepath.Join(homeDir, "Library", "Safari", "History.db")
			allEntries = append(allEntries, fetchSafariHistory(safariPath)...)
		}
	}

	if len(allEntries) == 0 {
		b.pendingEntries = nil
		return map[string]interface{}{
			"top_recent_urls":     []HistoryEntry{},
			"new_history_entries": []HistoryEntry{},
			"sync_type":           "incremental",
		}, nil
	}

	sort.Slice(allEntries, func(i, j int) bool {
		return allEntries[i].LastVisitTime > allEntries[j].LastVisitTime
	})
	recentSnapshot := append([]HistoryEntry(nil), allEntries...)
	if len(recentSnapshot) > maxBrowserHistoryRecentSnapshotEntries {
		recentSnapshot = recentSnapshot[:maxBrowserHistoryRecentSnapshotEntries]
	}

	filtered := allEntries[:0]
	for _, entry := range allEntries {
		if !b.hasSent(entry) {
			filtered = append(filtered, entry)
		}
	}
	allEntries = filtered

	sort.Slice(allEntries, func(i, j int) bool {
		return allEntries[i].LastVisitTime < allEntries[j].LastVisitTime
	})
	if len(allEntries) > maxBrowserHistoryUploadEntries {
		allEntries = allEntries[:maxBrowserHistoryUploadEntries]
	}

	b.pendingEntries = append([]HistoryEntry(nil), allEntries...)
	sort.Slice(allEntries, func(i, j int) bool {
		return allEntries[i].LastVisitTime > allEntries[j].LastVisitTime
	})

	return map[string]interface{}{
		"top_recent_urls":     recentSnapshot,
		"new_history_entries": allEntries,
		"sync_type":           "incremental",
	}, nil
}

func (b *BrowserHistory) Commit() error {
	if len(b.pendingEntries) == 0 {
		return nil
	}
	if b.sentEntries == nil {
		b.sentEntries = map[string]int64{}
	}
	for _, entry := range b.pendingEntries {
		b.sentEntries[b.sentKey(entry)] = entry.LastVisitTime
	}
	b.pendingEntries = nil
	b.pruneSentEntries()
	if b.statePath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(b.statePath), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(browserHistoryState{SentEntries: b.sentEntries}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(b.statePath, data, 0600)
}

func (b *BrowserHistory) hasSent(entry HistoryEntry) bool {
	if b.sentEntries == nil {
		return false
	}
	_, ok := b.sentEntries[b.sentKey(entry)]
	return ok
}

func (b *BrowserHistory) sentKey(entry HistoryEntry) string {
	return strings.ToLower(entry.Browser) + "\x00" + entry.URL + "\x00" + strconv.FormatInt(entry.LastVisitTime, 10)
}

func (b *BrowserHistory) pruneSentEntries() {
	if len(b.sentEntries) <= maxBrowserHistorySentStateEntries {
		return
	}
	type stateEntry struct {
		key       string
		visitTime int64
	}
	entries := make([]stateEntry, 0, len(b.sentEntries))
	for key, visitTime := range b.sentEntries {
		entries = append(entries, stateEntry{key: key, visitTime: visitTime})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].visitTime > entries[j].visitTime })
	keep := map[string]int64{}
	for i, entry := range entries {
		if i >= maxBrowserHistorySentStateEntries {
			break
		}
		keep[entry.key] = entry.visitTime
	}
	b.sentEntries = keep
}

func (b *BrowserHistory) loadState() {
	if b.stateLoaded {
		return
	}
	b.stateLoaded = true
	if b.statePath == "" {
		return
	}
	data, err := os.ReadFile(b.statePath)
	if err != nil {
		return
	}
	var state browserHistoryState
	if err := json.Unmarshal(data, &state); err != nil {
		return
	}
	if state.SentEntries != nil {
		b.sentEntries = state.SentEntries
	} else {
		b.sentEntries = map[string]int64{}
	}
}

// resolveHomeDirs returns the list of home directories to scan for browser history.
//
// Strategy:
//   - Always include the current user's home directory (covers dev/local runs).
//   - On Linux/macOS, when running as root (e.g. systemd service), also parse
//     /etc/passwd to find all real human accounts (UID >= 1000, login shell
//     that isn't nologin/false) and add their home directories.
//   - Deduplicates the result so we never scan the same directory twice.
func resolveHomeDirs() []string {
	seen := map[string]bool{}
	var dirs []string

	add := func(dir string) {
		if dir == "" || seen[dir] {
			return
		}
		if _, err := os.Stat(dir); err == nil {
			seen[dir] = true
			dirs = append(dirs, dir)
		}
	}

	// Always start with the process owner's home dir.
	if h, err := os.UserHomeDir(); err == nil {
		add(h)
	}

	// On Linux/macOS running as root, also scan real user home dirs.
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		if os.Getuid() == 0 {
			for _, dir := range humanHomeDirsFromPasswd() {
				add(dir)
			}
		}
	}

	return dirs
}

// humanHomeDirsFromPasswd parses /etc/passwd and returns the home directories
// of accounts with UID >= 1000 (real human users on Linux/macOS) whose login
// shell is not a nologin/false variant.
func humanHomeDirsFromPasswd() []string {
	f, err := os.Open("/etc/passwd")
	if err != nil {
		return nil
	}
	defer f.Close()

	var dirs []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// passwd format: username:password:uid:gid:comment:home:shell
		fields := strings.Split(line, ":")
		if len(fields) < 7 {
			continue
		}
		uid, err := strconv.Atoi(fields[2])
		if err != nil || uid < 1000 {
			continue // skip system accounts
		}
		shell := fields[6]
		if strings.Contains(shell, "nologin") || strings.Contains(shell, "false") {
			continue // skip non-interactive accounts
		}
		homeDir := fields[5]
		if homeDir != "" {
			dirs = append(dirs, homeDir)
		}
	}
	return dirs
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
	defer removeSQLiteTempFiles(tmpPath)

	if err := copySQLiteDatabase(historyPath, tmpPath); err != nil {
		return nil
	}

	db, err := sql.Open("sqlite", "file:"+tmpPath+"?mode=ro")
	if err != nil {
		return nil
	}
	defer db.Close()

	rows, err := db.Query(`SELECT url, title, last_visit_time FROM urls ORDER BY last_visit_time DESC LIMIT ?`, maxBrowserHistoryScanEntries)
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
	if err := rows.Err(); err != nil {
		return nil
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
	defer removeSQLiteTempFiles(tmpPath)

	if err := copySQLiteDatabase(historyPath, tmpPath); err != nil {
		return nil
	}

	db, err := sql.Open("sqlite", "file:"+tmpPath+"?mode=ro")
	if err != nil {
		return nil
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT url, title, last_visit_date
		FROM moz_places
		WHERE last_visit_date IS NOT NULL
		ORDER BY last_visit_date DESC
			LIMIT ?`, maxBrowserHistoryScanEntries)
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
	if err := rows.Err(); err != nil {
		return nil
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
	defer removeSQLiteTempFiles(tmpPath)

	if err := copySQLiteDatabase(safariPath, tmpPath); err != nil {
		return nil
	}

	db, err := sql.Open("sqlite", "file:"+tmpPath+"?mode=ro")
	if err != nil {
		return nil
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT i.url, v.title, v.visit_time
		FROM history_items i
		JOIN history_visits v ON i.id = v.history_item
		ORDER BY v.visit_time DESC
		LIMIT ?`, maxBrowserHistoryScanEntries)
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
	if err := rows.Err(); err != nil {
		return nil
	}
	return entries
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func copySQLiteDatabase(src, dst string) error {
	if err := copyFile(src, dst); err != nil {
		return err
	}

	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(src + suffix); err == nil {
			if err := copyFile(src+suffix, dst+suffix); err != nil {
				return err
			}
		}
	}
	return nil
}

func removeSQLiteTempFiles(path string) {
	_ = os.Remove(path)
	_ = os.Remove(path + "-wal")
	_ = os.Remove(path + "-shm")
}

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
