// Package safefs holds the few file operations the agent performs inside a
// directory that other local users may write to (the shared queue directory).
//
// Because the agent runs as root there, a plain os.Chmod or os.OpenFile that
// follows a planted symlink would let any local user change the permissions of,
// or append to, an arbitrary file. These helpers refuse to follow symlinks and
// act on the opened descriptor, not on the path.
package safefs

import (
	"errors"
	"fmt"
	"os"
)

// ErrSymlink is returned when a path that must be a plain file or directory is a symlink.
var ErrSymlink = errors.New("refusing to follow symlink")

// ChmodFile sets mode on a regular file without following symlinks.
func ChmodFile(path string, mode os.FileMode) error { return chmodNoFollow(path, mode, false) }

// ChmodDir sets mode on a directory without following symlinks.
func ChmodDir(path string, mode os.FileMode) error { return chmodNoFollow(path, mode, true) }

func chmodNoFollow(path string, mode os.FileMode, wantDir bool) error {
	f, err := openNoFollow(path, os.O_RDONLY|nonblock, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if wantDir != info.IsDir() || (!wantDir && !info.Mode().IsRegular()) {
		return fmt.Errorf("%s is not the expected file type", path)
	}
	return f.Chmod(mode)
}

// OpenAppend opens (creating if needed) a file for appending without following a symlink.
func OpenAppend(path string, perm os.FileMode) (*os.File, error) {
	return openNoFollow(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, perm)
}

// RemoveIfSymlink deletes path only when it is a symlink (the link itself, never
// its target). It reports whether a link was removed.
func RemoveIfSymlink(path string) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return false, nil
	}
	if err := os.Remove(path); err != nil {
		return false, err
	}
	return true, nil
}
