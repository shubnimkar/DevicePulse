//go:build linux

package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseLoginctlUsers(t *testing.T) {
	out := "     0 root     no active\n  1000 ashish   no active\n  1001 suraj    yes lingering\n   120 gdm      no active\n\n"
	got := parseLoginctlUsers(out)
	if want := []string{"ashish", "suraj"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestWindowUnitStartsWithGraphicalSession(t *testing.T) {
	unit := windowUnitContent("/usr/local/bin/devicepulse-agent")
	for _, want := range []string{
		"ExecStart=/usr/local/bin/devicepulse-agent\n",
		"WantedBy=graphical-session.target",
		"DEVICEPULSE_MODE=window_only",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit missing %q", want)
		}
	}
	if strings.Contains(unit, "default.target") {
		t.Error("unit must not hang off default.target: it never restarts after a logout")
	}
}
