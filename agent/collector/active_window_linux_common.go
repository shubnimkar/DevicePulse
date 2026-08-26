package collector

import "strings"

// ignoredLinuxForegroundProcesses is a comprehensive blocklist of background
// system processes, daemons, and infrastructure tools that are never user-facing
// applications. Any process matching this list is suppressed from app-usage
// reporting so only real GUI apps (Chrome, VS Code, AI agents, etc.) appear.
var ignoredLinuxForegroundProcesses = map[string]struct{}{
	// ── DevicePulse own processes ─────────────────────────────────────────────
	"devicepulse-age":   {},
	"devicepulse-agent": {},

	// ── systemd / init ────────────────────────────────────────────────────────
	"systemd":            {},
	"systemd-journald":   {},
	"systemd-logind":     {},
	"systemd-udevd":      {},
	"systemd-resolved":   {},
	"systemd-networkd":   {},
	"systemd-timesyncd":  {},
	"systemd-hostnamed":  {},
	"systemd-machined":   {},
	"systemd-oomd":       {},
	"init":               {},

	// ── package managers / updaters ───────────────────────────────────────────
	"apt-check":         {},
	"apt.systemd.daily": {},
	"apt-get":           {},
	"dpkg":              {},
	"packagekitd":       {},
	"packagekit":        {},
	"snapd":             {},
	"snap":              {},
	"appstreamcli":      {}, // ← was appearing in dashboard
	"xdelta3":           {}, // ← was appearing in dashboard
	"unattended-upgr":   {},
	"unattended-upgrade": {},
	"update-notifier":   {},
	"dnf":               {},
	"yum":               {},
	"rpm":               {},
	"zypper":            {},
	"pacman":            {},
	"flatpak":           {},

	// ── D-Bus / IPC daemons ───────────────────────────────────────────────────
	"dbus-daemon":          {},
	"dbus-launch":          {},
	"gdbus":                {},
	"ibus-daemon":          {},
	"ibus-x11":             {},
	"ibus-portal":          {},
	"at-spi-bus-launcher":  {},
	"at-spi2-registryd":    {},

	// ── display / compositor infrastructure ───────────────────────────────────
	"Xorg":            {},
	"xorg":            {},
	"x11":             {},
	"Xwayland":        {},
	"xwayland":        {},
	"mutter":          {},
	"kwin_wayland":    {},
	"kwin_x11":        {},
	"kwin":            {},
	"openbox":         {},
	"xfwm4":           {},
	"marco":           {},
	"compiz":          {},
	"picom":           {},
	"compton":         {},
	"sway":            {},
	"wayfire":         {},

	// ── GNOME shell / desktop services ───────────────────────────────────────
	"gnome-shell":              {},
	"gnome-session":            {},
	"gnome-session-binary":     {},
	"gnome-keyring-daemon":     {},
	"gnome-settings-daemon":    {},
	"gsd-media-keys":           {},
	"gsd-power":                {},
	"gsd-color":                {},
	"gsd-keyboard":             {},
	"gsd-print-notifications":  {},
	"gsd-rfkill":               {},
	"gsd-screensaver-proxy":    {},
	"gsd-sharing":              {},
	"gsd-smartcard":            {},
	"gsd-sound":                {},
	"gsd-usb-protection":       {},
	"gsd-wacom":                {},
	"gsd-wwan":                 {},
	"gsd-xsettings":            {},
	"gnome-software":           {},
	"evolution-source-registry": {},
	"evolution-calendar-factory": {},
	"evolution-addressbook-factory": {},
	"tracker-miner-fs":         {},
	"tracker-store":            {},
	"tracker-extract":          {},
	"gvfsd":                    {},
	"gvfsd-fuse":               {},
	"gvfs-udisks2-volume-monitor": {},
	"gvfs-goa-volume-monitor":  {},
	"gvfs-mtp-volume-monitor":  {},
	"gvfs-afc-volume-monitor":  {},

	// ── KDE desktop services ──────────────────────────────────────────────────
	"plasmashell":       {},
	"kded5":             {},
	"kded6":             {},
	"kdeinit5":          {},
	"kdeinit6":          {},
	"baloo_file":        {},
	"akonadi_control":   {},
	"akonadiserver":     {},
	"kwalletd5":         {},
	"kwalletd6":         {},
	"kactivitymanagerd": {},
	"polkit-kde-auth":   {},

	// ── hardware / power / device daemons ────────────────────────────────────
	"upowerd":           {},
	"udisksd":           {},
	"bluetoothd":        {},
	"NetworkManager":    {},
	"networkmanager":    {},
	"ModemManager":      {},
	"wpa_supplicant":    {},
	"avahi-daemon":      {},
	"cups":              {},
	"cupsd":             {},
	"fwupd":             {},
	"tlp":               {},
	"thermald":          {},
	"rtkit-daemon":      {},
	"polkitd":           {},
	"colord":            {},
	"fprintd":           {},
	"iio-sensor-proxy":  {},
	"geoclue":           {},

	// ── logging / monitoring daemons ─────────────────────────────────────────
	"rsyslogd":          {},
	"syslogd":           {},
	"journald":          {},
	"auditd":            {},
	"crond":             {},
	"cron":              {},
	"atd":               {},

	// ── network / security daemons ────────────────────────────────────────────
	"sshd":              {},
	"firewalld":         {},
	"fail2ban-server":   {},
	"nginx":             {},
	"apache2":           {},
	"httpd":             {},
	"mysqld":            {},
	"postgres":          {},
	"redis-server":      {},

	// ── container / VM helpers ────────────────────────────────────────────────
	"dockerd":           {},
	"docker-proxy":      {},
	"containerd":        {},
	"containerd-shim":   {},
	"virtiofsd":         {},

	// ── helper / wrapper processes ────────────────────────────────────────────
	"sh":                {},
	"bash":              {},
	"dash":              {},
	"zsh":               {},
	"fish":              {},
	"python":            {},
	"python3":           {},
	"perl":              {},
	"ruby":              {},
	"node":              {},
	"gjs":               {},
	"xdg-permission-store": {},
	"xdg-document-portal":  {},
	"xdg-desktop-portal":   {},
	"pipewire":          {},
	"pipewire-pulse":    {},
	"wireplumber":       {},
	"pulseaudio":        {},
	"alsactl":           {},
	"ssh-agent":         {},
	"gpg-agent":         {},
	"dconf-service":     {},
	"gconfd-2":          {},
	"gsettings":         {},
	"obexd":             {},
	"zeitgeist-daemon":  {},
	"zeitgeist-fts":     {},
	"mission-control-5": {},
	"tumblerd":          {},
	"xfconfd":           {},
	"light-locker":      {},
	"xscreensaver":      {},
}

// isLinuxSystemProcess returns true for processes that are background system
// tools — matched by prefix/suffix/substring patterns too broad for a static map.
func isLinuxSystemProcess(key string) bool {
	// Systemd transient unit wrappers
	if strings.HasPrefix(key, "systemd-") {
		return true
	}
	// Any daemon ending in 'd' that looks like a system service
	// (gsd-*, kded*, akonadi*, etc.)
	if strings.HasPrefix(key, "gsd-") ||
		strings.HasPrefix(key, "gvfs") ||
		strings.HasPrefix(key, "gvf-") ||
		strings.HasPrefix(key, "gnome-") ||
		strings.HasPrefix(key, "kde") ||
		strings.HasPrefix(key, "plasma") ||
		strings.HasPrefix(key, "akonadi") ||
		strings.HasPrefix(key, "dconf") ||
		strings.HasPrefix(key, "xdg-") ||
		strings.HasPrefix(key, "tracker") ||
		strings.HasPrefix(key, "zeitgeist") ||
		strings.HasPrefix(key, "evolution") {
		return true
	}
	if strings.HasPrefix(key, "devicepulse-") {
		return true
	}
	// Snap/flatpak runtime wrappers
	if strings.Contains(key, "snap-confine") ||
		strings.Contains(key, "flatpak-spawn") ||
		strings.Contains(key, "bwrap") {
		return true
	}
	return false
}

func cleanLinuxForegroundApp(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}

	key := strings.ToLower(name)
	key = strings.TrimSuffix(key, ".service")

	if _, ignored := ignoredLinuxForegroundProcesses[key]; ignored {
		return ""
	}
	if isLinuxSystemProcess(key) {
		return ""
	}
	// Catch any remaining packagekit variants
	if strings.Contains(key, "packagekit") {
		return ""
	}

	return name
}
