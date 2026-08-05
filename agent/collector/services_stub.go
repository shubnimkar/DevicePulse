//go:build !windows
// +build !windows

package collector

func collectWindowsServicesImpl() []ServiceEntry { return nil }
