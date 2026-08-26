//go:build !windows

package commander

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// restartProcess re-executes the current binary in place. The process image is
// replaced atomically, keeping the same PID, arguments and environment — the
// same mechanism the self-updater uses.
func restartProcess() error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return fmt.Errorf("eval symlinks: %w", err)
	}
	return syscall.Exec(exePath, os.Args, os.Environ())
}
