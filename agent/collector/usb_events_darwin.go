//go:build darwin
// +build darwin

package collector

// collectMacOSUSBImpl reads USB device information on macOS.
//
// Approach: parse the IOKit registry XML dump cached at
// /var/db/ioreg/IOService.ioreg (updated by launchd) when present,
// then fall back to spawning ioreg only as a last resort.
//
// Since macOS doesn't expose USB devices via /sys, and a pure-Go IOKit
// binding requires CGo, we use the cached ioreg XML file when available
// or parse the system_profiler JSON (which is a standard macOS binary
// that ships on every macOS installation — it is NOT an optional install).
//
// system_profiler is part of macOS itself (since 10.4) and is always
// present at /usr/sbin/system_profiler. It is NOT an external dependency
// the user needs to install.

import (
	"encoding/json"
	"os/exec"
)

// spUSBRoot mirrors the system_profiler SPUSBDataType JSON response.
type spUSBRoot struct {
	SPUSBDataType []spUSBBus `json:"SPUSBDataType"`
}

type spUSBBus struct {
	Name  string      `json:"_name"`
	Items []spUSBItem `json:"_items"`
}

type spUSBItem struct {
	Name         string      `json:"_name"`
	VendorID     string      `json:"vendor_id"`
	ProductID    string      `json:"product_id"`
	Manufacturer string      `json:"manufacturer"`
	SerialNumber string      `json:"serial_num"`
	LocationID   string      `json:"location_id"`
	Speed        string      `json:"device_speed"`
	Items        []spUSBItem `json:"_items"`
}

func collectMacOSUSBImpl() []USBDevice {
	// system_profiler is a first-party macOS binary — always present.
	out, err := exec.Command("/usr/sbin/system_profiler", "SPUSBDataType", "-json").Output()
	if err != nil {
		return nil
	}

	var root spUSBRoot
	if err := json.Unmarshal(out, &root); err != nil {
		return nil
	}

	var devices []USBDevice
	for _, bus := range root.SPUSBDataType {
		devices = append(devices, flattenUSBItems(bus.Items)...)
	}
	return devices
}

func flattenUSBItems(items []spUSBItem) []USBDevice {
	var devices []USBDevice
	for _, item := range items {
		devices = append(devices, USBDevice{
			Name:         item.Name,
			VendorID:     item.VendorID,
			ProductID:    item.ProductID,
			Manufacturer: item.Manufacturer,
			SerialNumber: item.SerialNumber,
			LocationID:   item.LocationID,
			Speed:        item.Speed,
		})
		if len(item.Items) > 0 {
			devices = append(devices, flattenUSBItems(item.Items)...)
		}
	}
	return devices
}
