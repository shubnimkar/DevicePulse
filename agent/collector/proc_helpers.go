package collector

// proc_helpers.go — cross-platform /proc and process-name helpers.
// These functions are compiled on all platforms; on non-Linux platforms they
// gracefully return empty strings since /proc doesn't exist.

import (
	"os"
	"strings"
)

// readComm reads the single-line process name from /proc/[pid]/comm (Linux)
// or any given path. Returns "" on error.
func readComm(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// readCmdline reads /proc/[pid]/cmdline and returns the first argument (argv[0]).
func readCmdline(path string) string {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return ""
	}
	idx := strings.IndexByte(string(data), 0)
	if idx > 0 {
		return string(data[:idx])
	}
	return strings.TrimSpace(string(data))
}
