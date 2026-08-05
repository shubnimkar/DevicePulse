//go:build !windows
// +build !windows

package collector

// On non-Windows platforms, Windows port collection is a no-op.
// Linux uses /proc/net directly (network_ports.go).
// macOS uses gopsutil connections (network_ports_darwin.go).
func collectWindowsPortsImpl() []PortEntry { return nil }
