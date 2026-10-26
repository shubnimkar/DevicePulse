package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSameBinaryDetectsReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent")
	if err := os.WriteFile(path, []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	first, _ := os.Stat(path)
	again, _ := os.Stat(path)
	if !sameBinary(first, again) {
		t.Fatal("an untouched file must compare equal")
	}

	// The updater writes a temp file and renames it over the binary.
	tmp := filepath.Join(dir, ".agent-update")
	if err := os.WriteFile(tmp, []byte("version two is longer"), 0o755); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute)
	_ = os.Chtimes(tmp, later, later)
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
	replaced, _ := os.Stat(path)
	if sameBinary(first, replaced) {
		t.Fatal("a replaced binary must be detected")
	}
}
