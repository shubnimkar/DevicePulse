//go:build !windows
// +build !windows

package collector

// scanWindowsApps is a no-op on non-Windows platforms.
func scanWindowsApps() []AppEntry { return nil }
