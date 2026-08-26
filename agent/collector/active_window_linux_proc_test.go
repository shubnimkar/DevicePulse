//go:build linux
// +build linux

package collector

import "testing"

func TestLinuxProcFallbackRecognizesDesktopApps(t *testing.T) {
	for _, name := range []string{"chrome", "code", "antigravity-ide", "pgadmin4"} {
		if !isLikelyLinuxDesktopApp(name) {
			t.Fatalf("expected %q to be recognized as desktop app", name)
		}
	}
}

func TestLinuxDesktopEntryKeysIncludeExecNameAndDesktopID(t *testing.T) {
	keys := linuxDesktopEntryKeys(desktopEntry{
		name: "Future AI IDE",
		exec: `/opt/future-ai/future-ai-ide --enable-features %U`,
	}, "/usr/share/applications/com.vendor.FutureAI.desktop")

	seen := map[string]bool{}
	for _, key := range keys {
		seen[key] = true
	}
	for _, want := range []string{"future ai ide", "future-ai-ide", "com.vendor.futureai"} {
		if !seen[want] {
			t.Fatalf("expected keys to include %q, got %#v", want, keys)
		}
	}
}

func TestLinuxDesktopIndexMatchesRegisteredExecutableVariants(t *testing.T) {
	idx := linuxDesktopIndex{keys: map[string]struct{}{
		"future-ai-ide": {},
	}}
	for _, name := range []string{"future-ai-ide", "future-ai-ide-helper"} {
		if !idx.has(name) {
			t.Fatalf("expected %q to match desktop app index", name)
		}
	}
}
