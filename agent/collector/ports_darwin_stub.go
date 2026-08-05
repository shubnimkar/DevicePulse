//go:build !darwin
// +build !darwin

package collector

// On non-macOS platforms, macOS port collection is a no-op.
// Linux uses /proc/net; Windows uses gopsutil.
func collectDarwinPortsImpl() []PortEntry { return nil }
