package collector

import "strings"

var ignoredLinuxForegroundProcesses = map[string]struct{}{
	"apt-check":         {},
	"apt.systemd.daily": {},
	"packagekitd":       {},
	"snapd":             {},
	"fwupd":             {},
	"devicepulse-age":   {},
	"devicepulse-agent": {},
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
	if strings.HasSuffix(key, "d") && strings.Contains(key, "packagekit") {
		return ""
	}
	if strings.HasPrefix(key, "devicepulse-") {
		return ""
	}

	return name
}
