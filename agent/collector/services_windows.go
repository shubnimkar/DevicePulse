//go:build windows
// +build windows

package collector

import (
	"golang.org/x/sys/windows/registry"
	"strings"
)

// collectWindowsServicesImpl reads Windows services from the SCM registry.
// No sc.exe, WMI, or COM required.
func collectWindowsServicesImpl() []ServiceEntry {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SYSTEM\CurrentControlSet\Services`,
		registry.ENUMERATE_SUB_KEYS|registry.WOW64_64KEY)
	if err != nil {
		return nil
	}
	defer k.Close()

	names, err := k.ReadSubKeyNames(-1)
	if err != nil {
		return nil
	}

	var services []ServiceEntry
	for _, name := range names {
		sk, err := registry.OpenKey(registry.LOCAL_MACHINE,
			`SYSTEM\CurrentControlSet\Services\`+name,
			registry.QUERY_VALUE|registry.WOW64_64KEY)
		if err != nil {
			continue
		}

		// Type: 0x1/0x2 = kernel driver, 0x10/0x20 = Win32 service
		svcType, _, _ := sk.GetIntegerValue("Type")
		start, _, _ := sk.GetIntegerValue("Start")
		sk.Close()

		// Skip drivers — only enumerate Win32 services (Type 0x10 or 0x20).
		if svcType != 0x10 && svcType != 0x20 {
			continue
		}

		// Start: 0=Boot, 1=System, 2=Auto, 3=Manual, 4=Disabled
		status := "unknown"
		if start == 4 {
			status = "stopped"
		} else if start == 2 || start == 1 || start == 0 {
			// Auto-start services are presumed running; manual ones unknown.
			status = "running"
		} else {
			status = "stopped"
		}

		displayName := name
		services = append(services, ServiceEntry{
			Name:   strings.ToLower(displayName),
			Status: status,
		})
	}
	return services
}
