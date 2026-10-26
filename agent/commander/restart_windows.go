//go:build windows

package commander

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/devicepulse/agent/winrestart"
)

// restartProcess brings the agent back up from its own binary. See
// winrestart.Restart for how this works as a service and in per-user mode. (The
// previous version started the replacement from a goroutine and then called
// os.Exit immediately, so the replacement never started.)
func restartProcess() error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return fmt.Errorf("eval symlinks: %w", err)
	}
	winrestart.Restart(exePath)
	return nil // not reached
}
