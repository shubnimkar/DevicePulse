//go:build !windows
// +build !windows

package collector

// On non-Windows platforms, Windows USB collection is a no-op.
func collectWindowsUSBImpl() []USBDevice { return nil }
