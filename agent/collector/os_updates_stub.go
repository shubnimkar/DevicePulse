//go:build !windows
// +build !windows

package collector

func collectWindowsUpdatesImpl() OSUpdateInfo { return OSUpdateInfo{Source: "unsupported"} }
