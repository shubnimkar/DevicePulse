//go:build windows

package commander

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// restartProcess relaunches the agent binary as a detached process, then lets
// the current instance exit so the single-instance mutex is released before
// the replacement starts.
//
// syscall.Exec does not exist on Windows, so a true in-place image swap is not
// possible; the detached-relaunch dance below is the standard equivalent for
// services managed by the SCM.
func restartProcess() error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return fmt.Errorf("eval symlinks: %w", err)
	}

	// Small delay so this process can exit (releasing the single-instance
	// mutex) before the replacement attempts to start.
	go func() {
		time.Sleep(2 * time.Second)
		attr := &os.ProcAttr{
			Files: []*os.File{nil, nil, nil},
			Sys:   &syscall.SysProcAttr{HideWindow: true},
		}
		proc, err := os.StartProcess(exePath, os.Args, attr)
		if err != nil {
			log.Printf("Commander: restart relaunch failed: %v", err)
			return
		}
		_ = proc.Release() // do not wait on it
		log.Printf("Commander: relaunched %s", exePath)
	}()

	os.Exit(0)
	return nil
}

