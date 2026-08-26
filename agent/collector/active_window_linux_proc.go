//go:build linux
// +build linux

package collector

// linuxProcFallbackActiveWindow provides a best-effort active process name
// without any display server requirement.
//
// Strategy:
//  1. Read /proc/self/loginuid to get the current user's UID.
//  2. Walk /proc/[pid]/status to find all processes owned by that UID.
//  3. Of those, find the one that is:
//       a. NOT a kernel thread (has a /proc/[pid]/exe link).
//       b. In state "S" (sleeping/interactive) or "R" (running).
//       c. Belongs to the foreground process group of some TTY
//          (read /proc/[pid]/stat field 8 = tpgid).
//       d. Has the highest OOM score adjustment or resident set size as a
//          heuristic for "the thing the user is currently using".
//  4. Return the process name from /proc/[pid]/comm (trimmed).
//
// This is a last resort and will return the name of the most likely
// interactive process, not necessarily the exact focused GUI window.
// It works on any Linux kernel >= 2.6.26.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// procCPUSampleWindow is the interval between the two /proc CPU samples used to
// compute recent-CPU deltas in the fallback path below.
const procCPUSampleWindow = 250 * time.Millisecond

// candidate represents a process being evaluated as the active window.
type candidate struct {
	pid          string
	name         string
	rss          int64 // resident set size in pages (higher = more likely "active")
	cpuTotal     int64 // lifetime utime+stime jiffies captured at pass 1
	cpuDelta     int64 // CPU jiffies burned during the sample window (recent activity)
	tty          int   // controlling tty (0 = no tty / daemon)
	pgid         int   // process group ID
	tpgid        int   // foreground process group of the tty
	isForeground bool  // true when pgid == tpgid and tty != 0
	isDesktopApp bool
}

// linuxProcFallbackActiveWindow is the entry point called from active_window.go.
//
// Fix 1: when the agent runs as root (uid=0) it scans ALL real user sessions
// (UID >= 1000) instead of only root-owned processes. Root processes like
// xdelta3 or appstreamcli used to "win" the RSS race and pollute app-usage data.
//
// Fix 2: desktop candidates are ranked by RECENT CPU usage — the delta between
// two /proc samples taken procCPUSampleWindow apart — instead of lifetime CPU
// totals. Lifetime jiffies only ever grow, so one long-running process (e.g.
// pgadmin4 or a busy Chrome renderer) used to win every single poll and the
// whole day's app-usage collapsed onto that one app even while the user was
// working in VS Code or another browser.
func linuxProcFallbackActiveWindow() string {
	selfUID := getSelfUID()

	// Build the set of UIDs we care about.
	// If we are root, scan all human user UIDs (>= 1000).
	// Otherwise only scan our own UID.
	targetUIDs := map[string]struct{}{}
	if selfUID == "0" {
		for _, uid := range humanUserUIDs() {
			targetUIDs[uid] = struct{}{}
		}
	}
	if len(targetUIDs) == 0 && selfUID != "" {
		targetUIDs[selfUID] = struct{}{}
	}

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return ""
	}

	// ── Pass 1: collect candidates and their cumulative CPU counters ─────────
	cands := make([]candidate, 0, 128)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid := e.Name()
		if pid == "" || pid[0] < '0' || pid[0] > '9' {
			continue
		}

		// Filter by target UID set.
		if len(targetUIDs) > 0 {
			procUID := getProcUID("/proc/" + pid + "/status")
			if _, ok := targetUIDs[procUID]; !ok {
				continue
			}
		}

		// Skip kernel threads (no /proc/[pid]/exe).
		exePath := "/proc/" + pid + "/exe"
		if _, err := os.Readlink(exePath); err != nil {
			continue
		}

		stat := readProcStat("/proc/" + pid + "/stat")
		if stat == nil {
			continue
		}

		// stat fields (1-indexed as per man 5 proc):
		//  1  pid        2  comm     3  state   4  ppid
		//  5  pgrp       6  session  7  tty_nr  8  tpgid
		// ... 24 rss
		if len(stat) < 24 {
			continue
		}

		state := stat[2]
		// Only consider interactive processes (running or sleeping).
		if state != "S" && state != "R" && state != "D" {
			continue
		}

		ttyNr, _ := strconv.Atoi(stat[6])
		tpgid, _ := strconv.Atoi(stat[7])
		pgid, _ := strconv.Atoi(stat[4])
		rss, _ := strconv.ParseInt(stat[23], 10, 64)
		utime, _ := strconv.ParseInt(stat[13], 10, 64)
		stime, _ := strconv.ParseInt(stat[14], 10, 64)

		// A foreground process has pgid == tpgid of its tty.
		isForeground := ttyNr != 0 && pgid == tpgid

		comm := readComm("/proc/" + pid + "/comm")
		if comm == "" {
			comm = filepath.Base(readCmdline("/proc/" + pid + "/cmdline"))
		}
		if comm == "" {
			continue
		}
		if cleanLinuxForegroundApp(comm) == "" {
			continue
		}

		cands = append(cands, candidate{
			pid:          pid,
			name:         comm,
			rss:          rss,
			cpuTotal:     utime + stime,
			tty:          ttyNr,
			pgid:         pgid,
			tpgid:        tpgid,
			isForeground: isForeground,
			isDesktopApp: isLikelyLinuxDesktopApp(comm),
		})
	}

	if len(cands) == 0 {
		return ""
	}

	// ── Pass 2: re-sample the counters and keep only the recent delta ────────
	time.Sleep(procCPUSampleWindow)
	for i := range cands {
		stat := readProcStat("/proc/" + cands[i].pid + "/stat")
		if stat == nil || len(stat) < 24 {
			// Process exited between the samples — cannot be the active app.
			cands[i].cpuDelta = -1
			continue
		}
		utime, _ := strconv.ParseInt(stat[13], 10, 64)
		stime, _ := strconv.ParseInt(stat[14], 10, 64)
		delta := utime + stime - cands[i].cpuTotal
		if delta < 0 {
			delta = 0
		}
		cands[i].cpuDelta = delta
	}

	var best candidate
	var bestDesktop candidate

	for _, c := range cands {
		if c.cpuDelta < 0 {
			continue // vanished between the two samples
		}

		// Desktop apps always beat non-desktop apps regardless of RSS.
		// Among desktop apps, pick the one with the highest recent CPU
		// (RSS breaks ties between equally idle apps).
		if c.isDesktopApp && betterLinuxDesktopCandidate(c, bestDesktop) {
			bestDesktop = c
		}

		// Among all candidates: prefer foreground > highest RSS.
		if c.isForeground && (!best.isForeground || c.rss > best.rss) {
			best = c
		} else if !best.isForeground && c.rss > best.rss {
			best = c
		}
	}

	// Desktop app match always wins over generic highest-RSS process.
	if bestDesktop.name != "" {
		return bestDesktop.name
	}
	if best.isForeground {
		return best.name
	}
	if best.name != "" && best.tty != 0 {
		return best.name
	}
	// No user-facing app found — return nothing rather than a background daemon.
	return ""
}

// humanUserUIDs reads /etc/passwd and returns all UIDs >= 1000 with login shells.
// Used when the agent runs as root to scope /proc scanning to real users.
func humanUserUIDs() []string {
	data, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return nil
	}
	var uids []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// passwd: username:password:uid:gid:comment:home:shell
		fields := strings.Split(line, ":")
		if len(fields) < 7 {
			continue
		}
		uid, err := strconv.Atoi(fields[2])
		if err != nil || uid < 1000 {
			continue
		}
		shell := fields[6]
		if strings.Contains(shell, "nologin") || strings.Contains(shell, "false") {
			continue
		}
		uids = append(uids, fields[2])
	}
	return uids
}

// getProcUID reads the real UID from /proc/[pid]/status.
func getProcUID(statusPath string) string {
	data, err := os.ReadFile(statusPath)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "Uid:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				return fields[1] // real UID
			}
			return ""
		}
	}
	return ""
}

func isLikelyLinuxDesktopApp(name string) bool {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return false
	}
	if linuxDesktopAppIndex().has(key) {
		return true
	}

	// ── Exact-match allowlist ─────────────────────────────────────────────────
	// Safety net for portable/AppImage/manual installs that do not register a
	// desktop launcher.
	known := map[string]struct{}{
		// Browsers
		"chrome":                {},
		"google-chrome":         {},
		"google-chrome-stable":  {},
		"chromium":              {},
		"chromium-browser":      {},
		"firefox":               {},
		"firefox-esr":           {},
		"brave":                 {},
		"brave-browser":         {},
		"microsoft-edge":        {},
		"microsoft-edge-stable": {},
		"opera":                 {},
		"vivaldi":               {},
		"vivaldi-stable":        {},
		"epiphany":              {},
		"epiphany-browser":      {},
		"midori":                {},
		"waterfox":              {},
		"librewolf":             {},
		"tor browser":           {},
		"torbrowser-launcher":   {},

		// Code editors / IDEs
		"code":                  {},
		"code-insiders":         {},
		"codium":                {},
		"vscodium":              {},
		"cursor":                {},        // Cursor AI editor
		"windsurf":              {},        // Windsurf AI editor
		"antigravity":           {},
		"antigravity-ide":       {},
		"zed":                   {},        // Zed editor
		"zed-editor":            {},
		"sublime_text":          {},
		"sublime-text":          {},
		"subl":                  {},
		"atom":                  {},
		"pulsar":                {},
		"gedit":                 {},
		"geany":                 {},
		"kate":                  {},
		"kwrite":                {},
		"mousepad":              {},
		"xed":                   {},
		"pluma":                 {},
		"idea":                  {},
		"idea.sh":               {},
		"goland":                {},
		"goland.sh":             {},
		"pycharm":               {},
		"pycharm.sh":            {},
		"clion":                 {},
		"webstorm":              {},
		"phpstorm":              {},
		"rubymine":              {},
		"datagrip":              {},
		"rider":                 {},
		"eclipse":               {},
		"netbeans":              {},
		"netbeans64":            {},
		"android-studio":        {},
		"studio.sh":             {},
		"vim":                   {},
		"nvim":                  {},
		"neovim":                {},
		"emacs":                 {},
		"emacs-gtk":             {},

		// AI agent / assistant apps
		"claude":                {},  // Claude Desktop
		"claude-desktop":        {},
		"chatgpt":               {},  // ChatGPT desktop
		"perplexity":            {},
		"copilot":               {},
		"github-copilot":        {},
		"ollama":                {},
		"lm-studio":             {},
		"lmstudio":              {},
		"jan":                   {},  // Jan.ai desktop
		"gpt4all":               {},
		"open-webui":            {},
		"koboldcpp":             {},

		// Communication / collaboration
		"slack":                 {},
		"teams":                 {},
		"microsoft-teams":       {},
		"zoom":                  {},
		"zoom.us":               {},
		"discord":               {},
		"telegram-desktop":      {},
		"telegram":              {},
		"signal-desktop":        {},
		"signal":                {},
		"whatsapp-for-linux":    {},
		"whatsapp":              {},
		"element":               {},
		"element-desktop":       {},
		"skype":                 {},
		"skypeforlinux":         {},
		"thunderbird":           {},
		"evolution":             {},
		"geary":                 {},
		"mailspring":            {},
		"protonmail-bridge":     {},
		"nylas-mail":            {},
		"mattermost-desktop":    {},
		"mattermost":            {},
		"rocketchat-desktop":    {},
		"webex":                 {},
		"webex meetings":        {},

		// Productivity / office
		"notion":                {},
		"notion-app":            {},
		"obsidian":              {},
		"logseq":                {},
		"joplin":                {},
		"zettlr":                {},
		"libreoffice":           {},
		"libreoffice-writer":    {},
		"libreoffice-calc":      {},
		"libreoffice-impress":   {},
		"soffice":               {},
		"soffice.bin":           {},
		"onlyoffice-desktopeditors": {},
		"wps":                   {},
		"etherpad":              {},
		"xournalpp":             {},
		"okular":                {},
		"evince":                {},
		"zathura":               {},

		// Developer tools
		"postman":               {},
		"insomnia":              {},
		"hoppscotch":            {},
		"dbeaver":               {},
		"dbeaver-ce":            {},
		"beekeeper-studio":      {},
		"tableplus":             {},
		"pgadmin4":              {},
		"pgadmin":               {},
		"mongodb compass":       {},
		"mongodb-compass":       {},
		"robo-3t":               {},
		"robo3t":                {},
		"redis-insight":         {},
		"redisinsight":          {},
		"responsively":          {},
		"stoplight-studio":      {},
		"pico8":                 {},
		"docker desktop":        {},
		"docker-desktop":        {},
		"rancher-desktop":       {},
		"podman-desktop":        {},
		"helm-dashboard":        {},

		// Terminals
		"gnome-terminal":        {},
		"gnome-terminal-server": {},
		"konsole":               {},
		"xterm":                 {},
		"rxvt":                  {},
		"urxvt":                 {},
		"tilix":                 {},
		"terminator":            {},
		"alacritty":             {},
		"kitty":                 {},
		"wezterm":               {},
		"wezterm-gui":           {},
		"hyper":                 {},
		"tabby":                 {},
		"tabby-terminal":        {},
		"yakuake":               {},
		"guake":                 {},
		"xfce4-terminal":        {},
		"mate-terminal":         {},
		"lxterminal":            {},
		"st":                    {},
		"foot":                  {},
		"ghostty":               {},

		// Design / creative
		"figma":                 {},
		"figma-linux":           {},
		"gimp":                  {},
		"inkscape":              {},
		"blender":               {},
		"darktable":             {},
		"rawtherapee":           {},
		"krita":                 {},
		"kdenlive":              {},
		"openshot":              {},
		"shotcut":               {},
		"obs":                   {},
		"obs-studio":            {},
		"audacity":              {},
		"vlc":                   {},
		"mpv":                   {},
		"spotify":               {},
		"rhythmbox":             {},
		"clementine":            {},
		"strawberry":            {},
		"lollypop":              {},

		// System / utility (user-facing)
		"thunar":                {},
		"nautilus":              {},
		"nemo":                  {},
		"pcmanfm":               {},
		"dolphin":               {},
		"ranger":                {},
		"gnome-system-monitor":  {},
		"gnome-disk-utility":    {},
		"baobab":                {},
		"gparted":               {},
		"virtualbox":            {},
		"vmware":                {},
		"virt-manager":          {},
		"remmina":               {},
		"vinagre":               {},
		"wireshark":             {},
		"zenmap":                {},
		"burpsuite":             {},
		"ghidra":                {},
		"1password":             {},
		"bitwarden":             {},
		"keepassxc":             {},
		"seahorse":              {},
		"calibre":               {},
	}

	if _, ok := known[key]; ok {
		return true
	}

	// ── Substring patterns for app families ───────────────────────────────────
	// These catch variant binary names, Electron app helpers, snap/flatpak
	// wrappers, and version-suffixed binaries.
	return strings.Contains(key, "chrome") ||
		strings.Contains(key, "chromium") ||
		strings.Contains(key, "firefox") ||
		strings.Contains(key, "electron") ||
		strings.Contains(key, "mongodb") ||
		strings.Contains(key, "cursor") ||
		strings.Contains(key, "windsurf") ||
		strings.Contains(key, "antigravity") ||
		strings.Contains(key, "copilot") ||
		strings.Contains(key, "claude") ||
		strings.Contains(key, "chatgpt") ||
		strings.Contains(key, "ollama") ||
		strings.Contains(key, "vscode") ||
		strings.Contains(key, "codium") ||
		strings.Contains(key, "jetbrains") ||
		strings.Contains(key, "intellij") ||
		strings.Contains(key, "pycharm") ||
		strings.Contains(key, "goland") ||
		strings.Contains(key, "webstorm") ||
		strings.Contains(key, "postman") ||
		strings.Contains(key, "insomnia") ||
		strings.Contains(key, "slack") ||
		strings.Contains(key, "discord") ||
		strings.Contains(key, "zoom") ||
		strings.Contains(key, "teams") ||
		strings.Contains(key, "notion") ||
		strings.Contains(key, "obsidian") ||
		strings.Contains(key, "figma") ||
		strings.Contains(key, "1password") ||
		strings.Contains(key, "bitwarden") ||
		strings.Contains(key, "keepass") ||
		strings.Contains(key, "wezterm") ||
		strings.Contains(key, "alacritty") ||
		strings.Contains(key, "ghostty")
}

type linuxDesktopIndex struct {
	keys map[string]struct{}
}

func (idx linuxDesktopIndex) has(name string) bool {
	key := normalizeLinuxDesktopKey(name)
	if key == "" {
		return false
	}
	if _, ok := idx.keys[key]; ok {
		return true
	}
	for registered := range idx.keys {
		if registered != "" && strings.Contains(key, registered) {
			return true
		}
	}
	return false
}

var (
	linuxDesktopIndexOnce sync.Once
	linuxDesktopIndexData linuxDesktopIndex
)

func linuxDesktopAppIndex() linuxDesktopIndex {
	linuxDesktopIndexOnce.Do(func() {
		linuxDesktopIndexData = buildLinuxDesktopAppIndex()
	})
	return linuxDesktopIndexData
}

func buildLinuxDesktopAppIndex() linuxDesktopIndex {
	idx := linuxDesktopIndex{keys: map[string]struct{}{}}
	for _, dir := range linuxDesktopEntryDirs() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".desktop") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			entry, ok := parseDesktopEntry(path)
			if !ok || shouldSkipDesktopEntry(entry) {
				continue
			}
			for _, key := range linuxDesktopEntryKeys(entry, path) {
				idx.keys[key] = struct{}{}
			}
		}
	}
	return idx
}

func linuxDesktopEntryDirs() []string {
	dirs := []string{
		"/usr/share/applications",
		"/usr/local/share/applications",
		"/var/lib/flatpak/exports/share/applications",
		"/var/lib/snapd/desktop/applications",
	}
	if home := os.Getenv("HOME"); home != "" {
		dirs = append(dirs,
			filepath.Join(home, ".local/share/applications"),
			filepath.Join(home, ".local/share/flatpak/exports/share/applications"),
		)
	}
	for _, base := range []string{"/home", "/Users"} {
		users, err := os.ReadDir(base)
		if err != nil {
			continue
		}
		for _, user := range users {
			if !user.IsDir() {
				continue
			}
			home := filepath.Join(base, user.Name())
			dirs = append(dirs,
				filepath.Join(home, ".local/share/applications"),
				filepath.Join(home, ".local/share/flatpak/exports/share/applications"),
			)
		}
	}
	return dirs
}

func linuxDesktopEntryKeys(entry desktopEntry, path string) []string {
	candidates := []string{
		entry.name,
		strings.TrimSuffix(filepath.Base(path), ".desktop"),
	}
	for _, token := range strings.Fields(entry.exec) {
		if strings.HasPrefix(token, "%") || strings.Contains(token, "=") {
			continue
		}
		token = strings.Trim(token, `"'`)
		if token == "" {
			continue
		}
		candidates = append(candidates, token, filepath.Base(token))
		break
	}

	keys := make([]string, 0, len(candidates))
	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		key := normalizeLinuxDesktopKey(candidate)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	return keys
}

func normalizeLinuxDesktopKey(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.Trim(value, `"'`)
	value = strings.TrimSuffix(value, ".desktop")
	if value == "" {
		return ""
	}
	value = filepath.Base(value)
	for _, suffix := range []string{".bin", ".sh"} {
		value = strings.TrimSuffix(value, suffix)
	}
	return value
}

// betterLinuxDesktopCandidate reports whether next is a stronger "active app"
// guess than current. Recent-CPU delta decides first so the guess follows the
// app that is actually doing work right now; RSS breaks ties between apps that
// were equally idle during the sample window.
func betterLinuxDesktopCandidate(next, current candidate) bool {
	if current.name == "" {
		return true
	}
	if next.cpuDelta != current.cpuDelta {
		return next.cpuDelta > current.cpuDelta
	}
	return next.rss > current.rss
}

// ── /proc helpers ─────────────────────────────────────────────────────────────

// getSelfUID returns the UID of the current process as a string.
func getSelfUID() string {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "Uid:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				return fields[1]
			}
		}
	}
	return ""
}

// procOwnedByUID returns true if the process's status file shows the given UID.
// Kept for compatibility — new code uses getProcUID instead.
func procOwnedByUID(statusPath, uid string) bool {
	return getProcUID(statusPath) == uid
}

// readProcStat parses /proc/[pid]/stat. The second field (comm) is wrapped in
// parentheses and may contain spaces or parentheses itself, so we handle that.
// Returns a slice of fields (0-indexed, matching man-page 1-indexed minus 1).
func readProcStat(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	s := strings.TrimSpace(string(data))

	// Find the last ')' which marks the end of the comm field.
	rp := strings.LastIndex(s, ")")
	if rp < 0 {
		return nil
	}
	lp := strings.Index(s, "(")
	if lp < 0 {
		return nil
	}

	pid := strings.TrimSpace(s[:lp])
	comm := s[lp+1 : rp]
	rest := strings.TrimSpace(s[rp+1:])
	fields := strings.Fields(rest)

	// Reconstruct: pid, comm, <rest fields...>
	result := make([]string, 0, 2+len(fields))
	result = append(result, pid, comm)
	result = append(result, fields...)
	return result
}

// readComm and readCmdline are defined in proc_helpers.go (shared across platforms).
