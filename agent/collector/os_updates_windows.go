//go:build windows
// +build windows

package collector

import (
	"time"

	"golang.org/x/sys/windows/registry"
)

// collectWindowsUpdatesImpl reads Windows Update information from the registry.
// No external binaries or WMI required.
func collectWindowsUpdatesImpl() OSUpdateInfo {
	info := OSUpdateInfo{Source: "registry"}

	// Last successful update install time.
	// HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion
	k, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Windows NT\CurrentVersion`,
		registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err == nil {
		defer k.Close()
		// UBR = Update Build Revision — present since Windows 10
		if ubr, _, err := k.GetIntegerValue("UBR"); err == nil && ubr > 0 {
			info.LastUpdateRaw = buildWindowsVersion(k, ubr)
		}
	}

	// Last Windows Update install date via:
	// HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\WindowsUpdate\Auto Update\Results\Install
	k2, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Windows\CurrentVersion\WindowsUpdate\Auto Update\Results\Install`,
		registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err == nil {
		defer k2.Close()
		if lastCheck, _, err := k2.GetStringValue("LastSuccessTime"); err == nil {
			info.LastUpdateRaw = lastCheck
			// Format: "2024-11-15 10:23:45"
			if t, err := time.Parse("2006-01-02 15:04:05", lastCheck); err == nil {
				info.LastUpdateTime = t.UTC().Format(time.RFC3339)
			}
		}
	}

	// Pending updates: check
	// HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\WindowsUpdate\Auto Update\RebootRequired
	_, err = registry.OpenKey(registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Windows\CurrentVersion\WindowsUpdate\Auto Update\RebootRequired`,
		registry.QUERY_VALUE)
	if err == nil {
		info.PendingUpdates = []string{"reboot required to complete pending update"}
		info.PendingCount = 1
	}

	return info
}

func buildWindowsVersion(k registry.Key, ubr uint64) string {
	cv, _, _ := k.GetStringValue("CurrentVersion")
	build, _, _ := k.GetStringValue("CurrentBuildNumber")
	return cv + "." + build + "." + itoa(ubr)
}

func itoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	buf := make([]byte, 20)
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte(n%10) + '0'
		n /= 10
	}
	return string(buf[pos:])
}
