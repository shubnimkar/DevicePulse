package collector

import "testing"

func TestCleanLinuxForegroundAppDropsBackgroundServices(t *testing.T) {
	for _, name := range []string{"fwupd", "fwupd.service", "dbus-daemon", "systemd", "upowerd", "devicepulse-agent"} {
		if got := cleanLinuxForegroundApp(name); got != "" {
			t.Fatalf("expected %q to be ignored, got %q", name, got)
		}
	}
}

func TestCleanLinuxForegroundAppKeepsDesktopApps(t *testing.T) {
	for _, name := range []string{"Chrome", "Firefox", "Code"} {
		if got := cleanLinuxForegroundApp(name); got != name {
			t.Fatalf("expected %q to be kept, got %q", name, got)
		}
	}
}
