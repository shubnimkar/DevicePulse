package collector

import (
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"

	"github.com/shirou/gopsutil/v3/host"
)

type SystemInfo struct{}

func (s *SystemInfo) Name() string { return "SystemInfo" }
func (s *SystemInfo) Start() error { return nil }
func (s *SystemInfo) Stop() error  { return nil }

func (s *SystemInfo) Collect() (map[string]interface{}, error) {
	hostname, _ := os.Hostname()

	info := map[string]interface{}{
		"hostname":     hostname,
		"os":           runtime.GOOS,
		"architecture": runtime.GOARCH,
		"num_cpus":     runtime.NumCPU(),
	}

	// Enrich with gopsutil host info (OS version, kernel, platform)
	hi, err := host.Info()
	if err == nil {
		info["platform"]         = hi.Platform
		info["platform_version"] = hi.PlatformVersion
		info["kernel_version"]   = hi.KernelVersion
		info["os_family"]        = hi.PlatformFamily
	}

	return info, nil
}

// HardwareFingerprint holds stable hardware identifiers used to uniquely
// identify a device across reinstalls.
type HardwareFingerprint struct {
	HardwareUUID string // OS-level host UUID (e.g. from DMI/SMBIOS on Linux/Windows, IOKit on macOS)
	MACAddress   string // Primary non-loopback, non-virtual MAC address (canonical lower-case no-colon form)
	Hostname     string
}

// String returns a short human-readable representation.
func (f HardwareFingerprint) String() string {
	return fmt.Sprintf("uuid=%s mac=%s host=%s", f.HardwareUUID, f.MACAddress, f.Hostname)
}

// GetHardwareFingerprint collects stable hardware identifiers for this machine.
// It never returns an error — fields fall back to empty strings if unavailable
// so callers can decide how to handle partial data.
func GetHardwareFingerprint() HardwareFingerprint {
	fp := HardwareFingerprint{}

	fp.Hostname, _ = os.Hostname()

	// Hardware UUID via gopsutil (reads from SMBIOS/DMI on Linux/Windows,
	// IOKit on macOS). This survives OS reinstalls on the same hardware.
	if hi, err := host.Info(); err == nil {
		fp.HardwareUUID = hi.HostID
	}

	// Primary MAC address — pick the first non-loopback, non-virtual interface
	// that has a hardware address and is up.
	fp.MACAddress = primaryMAC()

	return fp
}

// primaryMAC returns the MAC address of the first suitable network interface.
// The returned string is lower-case with no separators, e.g. "a4c3f0112233".
func primaryMAC() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}

	for _, iface := range ifaces {
		// Skip loopback, down, and virtual/bridge interfaces.
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		if iface.HardwareAddr == nil || len(iface.HardwareAddr) == 0 {
			continue
		}
		// Skip common virtual interface prefixes.
		name := strings.ToLower(iface.Name)
		if strings.HasPrefix(name, "veth") ||
			strings.HasPrefix(name, "docker") ||
			strings.HasPrefix(name, "br-") ||
			strings.HasPrefix(name, "virbr") ||
			strings.HasPrefix(name, "vmnet") {
			continue
		}

		// Normalise: lower-case, strip colons → "a4c3f0112233"
		mac := strings.ToLower(iface.HardwareAddr.String())
		mac = strings.ReplaceAll(mac, ":", "")
		mac = strings.ReplaceAll(mac, "-", "")
		if mac != "" {
			return mac
		}
	}
	return ""
}
