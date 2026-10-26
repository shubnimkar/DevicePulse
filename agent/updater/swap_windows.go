//go:build windows

package updater

import (
	"fmt"
	"os"

	"github.com/devicepulse/agent/winrestart"
)

// A running .exe cannot be overwritten on Windows, but it can be renamed. Move it
// aside, put the new binary in place and roll back if that fails. The old file is
// deleted on the next start (cleanupOldBinary).
func swapBinary(tmpPath, exePath string) error {
	old := exePath + ".old"
	_ = os.Remove(old)
	if err := os.Rename(exePath, old); err != nil {
		return fmt.Errorf("move running binary aside: %w", err)
	}
	if err := os.Rename(tmpPath, exePath); err != nil {
		if rbErr := os.Rename(old, exePath); rbErr != nil {
			return fmt.Errorf("install new binary: %v (rollback also failed: %v)", err, rbErr)
		}
		return fmt.Errorf("install new binary: %w", err)
	}
	return nil
}

func relaunch(exePath string) error {
	winrestart.Restart(exePath)
	return nil // not reached
}

func cleanupOldBinary(exePath string) { _ = os.Remove(exePath + ".old") }
