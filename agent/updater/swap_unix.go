//go:build !windows

package updater

import (
	"os"
	"syscall"
)

// swapBinary atomically replaces the running executable (same filesystem, so the
// rename is atomic; Unix lets a running binary be replaced).
func swapBinary(tmpPath, exePath string) error { return os.Rename(tmpPath, exePath) }

// relaunch replaces this process image with the new binary, same PID and arguments.
func relaunch(exePath string) error { return syscall.Exec(exePath, os.Args, os.Environ()) }

func cleanupOldBinary(string) {}
