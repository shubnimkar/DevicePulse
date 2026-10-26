package main

import (
	"log"
	"os"
	"time"
)

// exitRestartForUpdate is a non-zero status so the systemd user unit
// (Restart=on-failure) starts the service again from the new binary.
const exitRestartForUpdate = 75

// sameBinary reports whether two stat results describe the same file contents on
// disk (the updater swaps in a new file by rename, so size or mtime changes).
func sameBinary(a, b os.FileInfo) bool {
	return a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

// restartWhenBinaryReplaced ends the per-user window service when the agent binary
// on disk is replaced. The root service re-executes itself after an update, but
// this process would otherwise keep running the old code until the next login.
func restartWhenBinaryReplaced(path string, every time.Duration) {
	initial, err := os.Stat(path)
	if err != nil {
		log.Printf("Self-restart watch disabled: %v", err)
		return
	}
	for {
		time.Sleep(every)
		current, err := os.Stat(path)
		if err != nil || sameBinary(initial, current) {
			continue
		}
		log.Printf("Agent binary was updated on disk; restarting to run the new version")
		os.Exit(exitRestartForUpdate)
	}
}
