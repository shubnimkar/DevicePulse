package collector

// USBEvents collects currently connected USB and external devices.
//
// Platform strategy — zero external binary requirements:
//   macOS   — reads IOKit USB device tree via the IORegistry sysctl/iokit API
//              through the pure-Go ioreg file reader at:
//              /var/db/usbmuxd/* or by reading the IOKit registry via
//              system calls. We use the ioreg XML output cached file path
//              as a first pass, then fall back to the macOS-specific build
//              tag file which uses the IOKit C framework via cgo-free bindings.
//   Linux   — reads /sys/bus/usb/devices/ sysfs entries directly.
//              Each device directory contains idVendor, idProduct, manufacturer,
//              product, serial files — no lsusb binary required.
//   Windows — uses SetupAPI via golang.org/x/sys (build-tagged file).

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// USBEvents collector.
type USBEvents struct{}

func (u *USBEvents) Name() string { return "USBEvents" }
func (u *USBEvents) Start() error { return nil }
func (u *USBEvents) Stop() error  { return nil }

// USBDevice represents a single connected USB device.
type USBDevice struct {
	Name         string `json:"name"`
	VendorID     string `json:"vendor_id,omitempty"`
	ProductID    string `json:"product_id,omitempty"`
	Manufacturer string `json:"manufacturer,omitempty"`
	SerialNumber string `json:"serial_number,omitempty"`
	LocationID   string `json:"location_id,omitempty"`
	Speed        string `json:"speed,omitempty"`
}

func (u *USBEvents) Collect() (map[string]interface{}, error) {
	var devices []USBDevice

	switch runtime.GOOS {
	case "linux":
		devices = collectLinuxUSB()
	case "darwin":
		devices = collectMacOSUSB()
	case "windows":
		devices = collectWindowsUSB()
	}

	if devices == nil {
		devices = []USBDevice{}
	}
	return map[string]interface{}{
		"usb_devices": devices,
		"count":       len(devices),
		"source":      "native",
	}, nil
}

// ─── Linux /sys/bus/usb ───────────────────────────────────────────────────────

// collectLinuxUSB reads /sys/bus/usb/devices/ sysfs entries.
// Each real USB device has a directory like "1-1", "1-1.2", etc.
// (Hub interfaces are "1-1:1.0" — we skip entries with ":".)
func collectLinuxUSB() []USBDevice {
	const sysUSB = "/sys/bus/usb/devices"
	entries, err := os.ReadDir(sysUSB)
	if err != nil {
		// Alternative path on some distros.
		return collectLinuxUSBFromPath("/sys/devices")
	}

	var devices []USBDevice
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		// Skip interface descriptors (contain ":") and usb buses themselves.
		if strings.Contains(name, ":") || strings.HasPrefix(name, "usb") {
			continue
		}

		devPath := filepath.Join(sysUSB, name)
		dev := readSysUSBDevice(devPath)
		if dev != nil {
			devices = append(devices, *dev)
		}
	}
	return devices
}

// collectLinuxUSBFromPath walks /sys/devices looking for USB devices.
func collectLinuxUSBFromPath(root string) []USBDevice {
	var devices []USBDevice
	// Walk only 4 levels deep to avoid traversing the entire /sys tree.
	walkDepth(root, 0, 4, func(path string) {
		dev := readSysUSBDevice(path)
		if dev != nil {
			devices = append(devices, *dev)
		}
	})
	return devices
}

// readSysUSBDevice reads USB device attributes from a sysfs directory.
// Returns nil if the directory doesn't look like a real USB device.
func readSysUSBDevice(dir string) *USBDevice {
	// A real USB device must have idVendor and idProduct.
	vendor := strings.TrimSpace(readSysFile(dir, "idVendor"))
	product := strings.TrimSpace(readSysFile(dir, "idProduct"))
	if vendor == "" || product == "" {
		return nil
	}

	name := strings.TrimSpace(readSysFile(dir, "product"))
	if name == "" {
		name = vendor + ":" + product
	}

	return &USBDevice{
		Name:         name,
		VendorID:     vendor,
		ProductID:    product,
		Manufacturer: strings.TrimSpace(readSysFile(dir, "manufacturer")),
		SerialNumber: strings.TrimSpace(readSysFile(dir, "serial")),
		Speed:        strings.TrimSpace(readSysFile(dir, "speed")),
	}
}

// readSysFile reads a single-value sysfs file, returning "" on error.
func readSysFile(dir, file string) string {
	data, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// walkDepth walks a directory tree to a maximum depth, calling fn for each dir.
func walkDepth(dir string, current, max int, fn func(string)) {
	if current > max {
		return
	}
	fn(dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			walkDepth(filepath.Join(dir, e.Name()), current+1, max, fn)
		}
	}
}

// ─── macOS ────────────────────────────────────────────────────────────────────

// collectMacOSUSB uses the IOKit registry via build-tagged implementation.
func collectMacOSUSB() []USBDevice {
	return collectMacOSUSBImpl() // usb_events_darwin.go
}

// ─── Windows ─────────────────────────────────────────────────────────────────

// collectWindowsUSB uses SetupAPI via build-tagged implementation.
func collectWindowsUSB() []USBDevice {
	return collectWindowsUSBImpl() // usb_events_windows.go
}
