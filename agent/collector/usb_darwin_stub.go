//go:build !darwin
// +build !darwin

package collector

// On non-macOS platforms, macOS-specific USB collection is a no-op.
func collectMacOSUSBImpl() []USBDevice { return nil }
