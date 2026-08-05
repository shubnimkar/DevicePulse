//go:build windows
// +build windows

package collector

// collectWindowsUSBImpl reads USB device information from the Windows
// registry under HKLM\SYSTEM\CurrentControlSet\Enum\USB.
// No external binaries, SetupAPI DLL calls, or WMI required.

import (
	"strings"

	"golang.org/x/sys/windows/registry"
)

func collectWindowsUSBImpl() []USBDevice {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SYSTEM\CurrentControlSet\Enum\USB`,
		registry.ENUMERATE_SUB_KEYS|registry.WOW64_64KEY)
	if err != nil {
		return nil
	}
	defer k.Close()

	// VID_xxxx&PID_xxxx sub-keys
	vidPidKeys, err := k.ReadSubKeyNames(-1)
	if err != nil {
		return nil
	}

	var devices []USBDevice

	for _, vidPid := range vidPidKeys {
		vid, pid := parseVidPid(vidPid)

		// Each VID/PID key has one or more instance sub-keys.
		vpKey, err := registry.OpenKey(k, vidPid,
			registry.ENUMERATE_SUB_KEYS|registry.WOW64_64KEY)
		if err != nil {
			continue
		}
		instances, _ := vpKey.ReadSubKeyNames(-1)
		vpKey.Close()

		for _, inst := range instances {
			instKey, err := registry.OpenKey(registry.LOCAL_MACHINE,
				`SYSTEM\CurrentControlSet\Enum\USB\`+vidPid+`\`+inst,
				registry.QUERY_VALUE|registry.WOW64_64KEY)
			if err != nil {
				continue
			}
			name, _, _ := instKey.GetStringValue("DeviceDesc")
			mfg, _, _ := instKey.GetStringValue("Mfg")
			serial := inst // instance ID often contains serial
			instKey.Close()

			// Clean up name: "USB\VID_xxx" prefixes or "Generic USB Hub" etc.
			if idx := strings.LastIndex(name, ";"); idx >= 0 {
				name = strings.TrimSpace(name[idx+1:])
			}
			if name == "" {
				name = vidPid
			}

			devices = append(devices, USBDevice{
				Name:         name,
				VendorID:     vid,
				ProductID:    pid,
				Manufacturer: cleanupMfg(mfg),
				SerialNumber: serial,
			})
		}
	}
	return devices
}

// parseVidPid extracts vendor and product IDs from "VID_1234&PID_5678" format.
func parseVidPid(s string) (vid, pid string) {
	s = strings.ToLower(s)
	for _, part := range strings.Split(s, "&") {
		if strings.HasPrefix(part, "vid_") {
			vid = part[4:]
		} else if strings.HasPrefix(part, "pid_") {
			pid = part[4:]
		}
	}
	return
}

func cleanupMfg(s string) string {
	// Manufacturer strings sometimes have ";Company Name" format.
	if idx := strings.LastIndex(s, ";"); idx >= 0 {
		return strings.TrimSpace(s[idx+1:])
	}
	return strings.TrimSpace(s)
}
