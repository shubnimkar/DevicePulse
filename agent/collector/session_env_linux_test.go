//go:build linux

package collector

import "testing"

func TestParseEnviron(t *testing.T) {
	env := parseEnviron([]byte("XDG_CURRENT_DESKTOP=ubuntu:GNOME\x00WAYLAND_DISPLAY=wayland-0\x00JUNK\x00A=b=c\x00"))
	if env["XDG_CURRENT_DESKTOP"] != "ubuntu:GNOME" || env["WAYLAND_DISPLAY"] != "wayland-0" {
		t.Fatalf("unexpected env: %v", env)
	}
	if env["A"] != "b=c" {
		t.Fatalf("value with '=' lost: %q", env["A"])
	}
	if _, ok := env["JUNK"]; ok {
		t.Fatal("entry without '=' must be ignored")
	}
}
