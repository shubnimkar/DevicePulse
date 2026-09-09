package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Repair permissions on existing caches as well as newly registered agents.
// Use the open descriptor for chmod and refuse symbolic links/special files.
func readRegistrationCache(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("registration cache must be a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(info, opened) {
		return nil, fmt.Errorf("registration cache changed while opening")
	}
	if err := f.Chmod(0600); err != nil {
		return nil, fmt.Errorf("restrict registration cache: %w", err)
	}
	return io.ReadAll(f)
}

// A private temporary file and rename avoid truncating an existing cache or
// following a destination symlink. Permissions do not depend on the old file.
func writeRegistrationCache(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".registration-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
