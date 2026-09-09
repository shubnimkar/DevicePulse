package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRegistrationCachePrivacy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission semantics; Windows uses installer ACLs")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "registration.json")
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	data, err := readRegistrationCache(path)
	if err != nil || string(data) != "old" {
		t.Fatalf("read: %s %v", data, err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("existing cache permissions not repaired")
	}
	if err := writeRegistrationCache(path, []byte("new")); err != nil {
		t.Fatal(err)
	}
	data, err = readRegistrationCache(path)
	if err != nil || string(data) != "new" {
		t.Fatalf("replacement: %s %v", data, err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readRegistrationCache(link); err == nil {
		t.Fatal("symlink accepted")
	}
	if err := writeRegistrationCache(link, []byte("separate")); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if string(data) != "new" {
		t.Fatal("write followed symlink")
	}
}
