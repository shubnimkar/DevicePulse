package commander

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestQuarantineDurationBounds(t *testing.T) {
	cases := []struct {
		params map[string]interface{}
		want   time.Duration
	}{
		{nil, 24 * time.Hour},                                             // default failsafe
		{map[string]interface{}{}, 24 * time.Hour},                        // missing
		{map[string]interface{}{"duration_minutes": 0.0}, 24 * time.Hour}, // invalid
		{map[string]interface{}{"duration_minutes": "5"}, 24 * time.Hour}, // wrong type
		{map[string]interface{}{"duration_minutes": 90.0}, 90 * time.Minute},
		{map[string]interface{}{"duration_minutes": 1e9}, 30 * 24 * time.Hour}, // capped
	}
	for _, c := range cases {
		if got := quarantineDuration(c.params); got != c.want {
			t.Errorf("quarantineDuration(%v) = %v, want %v", c.params, got, c.want)
		}
	}
}

func TestQuarantineDeadlinePersistsAndClears(t *testing.T) {
	dir := t.TempDir()
	old := wipeDataDir
	wipeDataDir = dir
	t.Cleanup(func() {
		clearQuarantineDeadline()
		wipeDataDir = old
	})

	armQuarantineDeadline(time.Hour) // far enough that the release never fires in a test
	data, err := os.ReadFile(filepath.Join(dir, quarantineStateFile))
	if err != nil {
		t.Fatalf("deadline not persisted: %v", err)
	}
	until, err := time.Parse(time.RFC3339, string(data))
	if err != nil || time.Until(until) < 50*time.Minute {
		t.Fatalf("bad deadline %q: %v", data, err)
	}

	clearQuarantineDeadline()
	if _, err := os.Stat(filepath.Join(dir, quarantineStateFile)); !os.IsNotExist(err) {
		t.Fatal("deadline file must be removed on release")
	}
}

func TestResumeDropsUnreadableDeadline(t *testing.T) {
	dir := t.TempDir()
	old := wipeDataDir
	wipeDataDir = dir
	t.Cleanup(func() { wipeDataDir = old })

	path := filepath.Join(dir, quarantineStateFile)
	if err := os.WriteFile(path, []byte("garbage"), 0600); err != nil {
		t.Fatal(err)
	}
	ResumeQuarantineDeadline()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("a corrupt deadline file must not linger as a permanent marker")
	}
}
