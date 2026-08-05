//go:build windows
// +build windows

package collector

import (
	"golang.org/x/sys/windows/registry"
)

// scanWindowsApps reads the Windows Add/Remove Programs registry keys.
// No external binaries required — pure registry API.
// Covers both 32-bit and 64-bit installed software on all modern Windows versions.
func scanWindowsApps() []AppEntry {
	paths := []struct {
		root  registry.Key
		path  string
		flags uint32
	}{
		{registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`, registry.WOW64_64KEY},
		{registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`, registry.WOW64_32KEY},
		{registry.CURRENT_USER, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`, 0},
	}

	var apps []AppEntry
	seen := map[string]bool{}

	for _, p := range paths {
		k, err := registry.OpenKey(p.root, p.path, registry.ENUMERATE_SUB_KEYS|p.flags)
		if err != nil {
			continue
		}
		subkeys, err := k.ReadSubKeyNames(-1)
		k.Close()
		if err != nil {
			continue
		}

		for _, sub := range subkeys {
			sk, err := registry.OpenKey(p.root, p.path+`\`+sub, registry.QUERY_VALUE|p.flags)
			if err != nil {
				continue
			}
			name, _, _ := sk.GetStringValue("DisplayName")
			version, _, _ := sk.GetStringValue("DisplayVersion")
			sk.Close()

			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			apps = append(apps, AppEntry{Name: name, Version: version, Source: "registry"})
		}
	}
	return apps
}
