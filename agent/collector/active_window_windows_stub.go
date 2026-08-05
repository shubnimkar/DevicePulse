//go:build !windows
// +build !windows

package collector

// On non-Windows platforms the Windows-specific active window collector is a no-op.
// The real implementation is in active_window_windows.go.
func getForegroundAppWindows() string { return "" }
