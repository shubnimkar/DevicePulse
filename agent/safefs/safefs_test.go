//go:build !windows

package safefs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestChmodRefusesSymlinkAndLeavesTargetAlone(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "devicepulse.db-shm")
	if err := os.Symlink(victim, link); err != nil {
		t.Fatal(err)
	}

	if err := ChmodFile(link, 0666); err == nil {
		t.Fatal("chmod through a symlink must fail")
	}
	if info, _ := os.Stat(victim); info.Mode().Perm() != 0600 {
		t.Fatalf("victim permissions changed to %v", info.Mode().Perm())
	}
}

func TestChmodRegularFileAndDir(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := ChmodFile(file, 0666); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(file); info.Mode().Perm() != 0666 {
		t.Fatalf("file mode = %v", info.Mode().Perm())
	}
	if err := ChmodDir(dir, 0777); err != nil {
		t.Fatal(err)
	}
	if err := ChmodFile(dir, 0666); err == nil {
		t.Fatal("a directory must not pass as a file")
	}
}

func TestOpenAppendRefusesSymlinkAndRemoveIfSymlinkKeepsTarget(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "agent.log")
	if err := os.Symlink(victim, link); err != nil {
		t.Fatal(err)
	}

	if f, err := OpenAppend(link, 0644); err == nil {
		f.Close()
		t.Fatal("append through a symlink must fail")
	}
	if data, _ := os.ReadFile(victim); string(data) != "keep" {
		t.Fatalf("victim modified: %q", data)
	}

	removed, err := RemoveIfSymlink(link)
	if err != nil || !removed {
		t.Fatalf("expected the planted link to be removed: %v %v", removed, err)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatal("removing the link must not remove its target")
	}
	f, err := OpenAppend(link, 0644)
	if err != nil {
		t.Fatalf("append to a fresh regular file should work: %v", err)
	}
	f.Close()

	if removed, _ := RemoveIfSymlink(victim); removed {
		t.Fatal("a regular file must not be removed")
	}
}
