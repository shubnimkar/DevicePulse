//go:build !linux
// +build !linux

package collector

// Stubs for non-Linux platforms. All Linux-specific functions are no-ops here;
// the actual implementations live in the active_window_linux_*.go files which
// are only compiled on Linux via build tags.

func getForegroundAppLinux() string           { return "" }
func linuxX11ActiveWindow() string            { return "" }
func linuxWaylandGNOMEActiveWindow() string   { return "" }
func linuxWaylandKDEActiveWindow() string     { return "" }
func linuxWaylandSwayActiveWindow() string    { return "" }
func linuxProcFallbackActiveWindow() string   { return "" }
