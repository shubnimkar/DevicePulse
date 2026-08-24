package collector

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/distatus/battery"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/net"
)

// HardwareStats collects CPU usage, RAM, disk, network I/O, and battery state.
type HardwareStats struct{}

func (h *HardwareStats) Name() string { return "HardwareStats" }
func (h *HardwareStats) Start() error { return nil }
func (h *HardwareStats) Stop() error  { return nil }

// CPUStat holds per-read CPU data.
type CPUStat struct {
	UsagePercent float64 `json:"usage_percent"`
	CoreCount    int     `json:"core_count"`
}

// RAMStat holds memory snapshot.
type RAMStat struct {
	TotalGB     float64 `json:"total_gb"`
	UsedGB      float64 `json:"used_gb"`
	FreeGB      float64 `json:"free_gb"`
	UsedPercent float64 `json:"used_percent"`
}

// DiskStat holds per-mount usage.
type DiskStat struct {
	Mount       string  `json:"mount"`
	TotalBytes  uint64  `json:"total_bytes"`
	UsedBytes   uint64  `json:"used_bytes"`
	FreeBytes   uint64  `json:"free_bytes"`
	TotalGB     float64 `json:"total_gb"`
	UsedGB      float64 `json:"used_gb"`
	FreeGB      float64 `json:"free_gb"`
	UsedPercent float64 `json:"used_percent"`
}

// NetStat holds cumulative I/O per interface.
type NetStat struct {
	Interface   string `json:"interface"`
	BytesSent   uint64 `json:"bytes_sent"`
	BytesRecv   uint64 `json:"bytes_recv"`
	PacketsSent uint64 `json:"packets_sent"`
	PacketsRecv uint64 `json:"packets_recv"`
}

// BatteryStat holds battery info (may be empty on desktops).
type BatteryStat struct {
	Available   bool    `json:"available"`
	Percent     float64 `json:"percent"`       // 0–100
	Plugged     bool    `json:"plugged"`       // true = AC / charging source connected
	Charging    bool    `json:"charging"`      // true = actively charging
	ChargeRateW float64 `json:"charge_rate_w"` // charge/discharge rate in Watts (negative = discharging)
	State       string  `json:"state"`         // "charging", "discharging", "full", "unknown"
}

func (h *HardwareStats) Collect() (map[string]interface{}, error) {
	result := map[string]interface{}{}

	// ── CPU ──────────────────────────────────────────────────
	// 100ms interval gives a quick non-blocking sample
	percents, err := cpu.Percent(100*time.Millisecond, false)
	cpuStat := CPUStat{CoreCount: 0}
	if err == nil && len(percents) > 0 {
		cpuStat.UsagePercent = round2(percents[0])
	}
	counts, err := cpu.Counts(true)
	if err == nil {
		cpuStat.CoreCount = counts
	}
	result["cpu"] = cpuStat

	// ── RAM ──────────────────────────────────────────────────
	vm, err := mem.VirtualMemory()
	if err == nil {
		result["ram"] = RAMStat{
			TotalGB:     toGB(vm.Total),
			UsedGB:      toGB(vm.Used),
			FreeGB:      toGB(vm.Available),
			UsedPercent: round2(vm.UsedPercent),
		}
	}

	// ── Disk ─────────────────────────────────────────────────
	var disks []DiskStat
	partitions, err := disk.Partitions(false)
	if err == nil {
		for _, p := range partitions {
			if shouldSkipDiskPartition(p) {
				continue
			}
			usage, err := disk.Usage(p.Mountpoint)
			if err != nil || usage.Total == 0 {
				continue
			}
			disks = append(disks, DiskStat{
				Mount:       p.Mountpoint,
				TotalBytes:  usage.Total,
				UsedBytes:   usage.Used,
				FreeBytes:   usage.Free,
				TotalGB:     toGB(usage.Total),
				UsedGB:      toGB(usage.Used),
				FreeGB:      toGB(usage.Free),
				UsedPercent: round2(usage.UsedPercent),
			})
		}
	}
	result["disks"] = disks

	// ── Network ───────────────────────────────────────────────
	var nets []NetStat
	counters, err := net.IOCounters(true)
	if err == nil {
		for _, c := range counters {
			// Skip loopback and idle interfaces
			if c.Name == "lo" || c.Name == "lo0" {
				continue
			}
			if c.BytesSent == 0 && c.BytesRecv == 0 {
				continue
			}
			nets = append(nets, NetStat{
				Interface:   c.Name,
				BytesSent:   c.BytesSent,
				BytesRecv:   c.BytesRecv,
				PacketsSent: c.PacketsSent,
				PacketsRecv: c.PacketsRecv,
			})
		}
	}
	result["network"] = nets

	// ── Battery ───────────────────────────────────────────────
	batt := BatteryStat{Available: false}
	batteries, battErr := battery.GetAll()
	// battery.GetAll() returns a battery.Errors ([]error) value, which is
	// a non-nil slice even when every individual battery read succeeds with
	// only partial errors (ErrPartial). A direct `battErr == nil` check always
	// fails on Linux when some sysfs fields are missing (e.g. charge_full on
	// some ThinkPads). Instead, treat any non-ErrFatal result as usable data.
	battUsable := false
	if battErr == nil {
		battUsable = true
	} else if errs, ok := battErr.(battery.Errors); ok {
		// Errors is []error — usable if at least one entry is nil or ErrPartial
		// (meaning the battery struct was populated even if some fields failed).
		for _, e := range errs {
			if e == nil {
				battUsable = true
				break
			}
			if _, isPartial := e.(battery.ErrPartial); isPartial {
				battUsable = true
				break
			}
		}
	}
	if battUsable && len(batteries) > 0 {
		b := batteries[0] // use first battery (covers most laptops)
		batt.Available = true

		// Percent: Current / Full * 100
		// b.Current and b.Full are in mWh; ratio gives %.
		// Fall back to reading capacity% directly from sysfs when Full == 0
		// (some kernels/firmware report 0 for charge_full).
		if b.Full > 0 {
			batt.Percent = round2(b.Current / b.Full * 100)
		} else {
			batt.Percent = round2(readSysfsBatteryCapacity())
		}

		// State string and derived flags.
		// Note: battery.Idle means charger is plugged but battery is held at ~80%
		// (capacity-saving mode on macOS). Treat it as plugged-in / not charging.
		switch b.State.Raw {
		case battery.Charging:
			batt.State = "charging"
			batt.Charging = true
			batt.Plugged = true
		case battery.Full:
			batt.State = "full"
			batt.Plugged = true
		case battery.Idle:
			batt.State = "idle" // plugged in, capacity-saving, not actively charging
			batt.Plugged = true
		case battery.Discharging:
			batt.State = "discharging"
		case battery.Empty:
			batt.State = "empty"
		default:
			batt.State = "unknown"
		}

		// ChargeRate from the library is always non-negative (in mW).
		// We assign sign based on state: positive = gaining charge, negative = losing charge.
		// Guard against NaN which macOS can return briefly during state transitions.
		rate := b.ChargeRate / 1000 // mW → W
		if math.IsNaN(rate) || math.IsInf(rate, 0) {
			rate = 0
		}
		switch batt.State {
		case "discharging", "empty":
			batt.ChargeRateW = -round2(rate)
		default:
			batt.ChargeRateW = round2(rate)
		}
	}
	result["battery"] = batt

	// ── Uptime ───────────────────────────────────────────────
	uptime, err := host.Uptime()
	if err == nil {
		result["uptime_seconds"] = uptime
		result["uptime_human"] = fmt.Sprintf("%dd %dh %dm",
			uptime/86400, (uptime%86400)/3600, (uptime%3600)/60)
	}

	return result, nil
}

func shouldSkipDiskPartition(partition disk.PartitionStat) bool {
	mountpoint := partition.Mountpoint
	fstype := strings.ToLower(partition.Fstype)

	if mountpoint == "" {
		return true
	}

	switch runtime.GOOS {
	case "darwin":
		if fstype == "devfs" || fstype == "autofs" {
			return true
		}
		if mountpoint == "/System/Volumes/Data/home" {
			return true
		}
		if strings.HasPrefix(mountpoint, "/System/Volumes/") {
			return true
		}
	case "linux":
		if isLinuxPseudoFilesystem(fstype) {
			return true
		}
		for _, prefix := range []string{"/proc", "/sys", "/dev", "/run", "/snap"} {
			if mountpoint == prefix || strings.HasPrefix(mountpoint, prefix+"/") {
				return true
			}
		}
	case "windows":
		if fstype == "cdfs" || fstype == "udf" {
			return true
		}
	}

	return false
}

func isLinuxPseudoFilesystem(fstype string) bool {
	switch fstype {
	case "autofs", "bpf", "binfmt_misc", "cgroup", "cgroup2", "configfs",
		"debugfs", "devpts", "devtmpfs", "efivarfs", "fusectl", "hugetlbfs",
		"mqueue", "nsfs", "overlay", "proc", "pstore", "ramfs", "securityfs",
		"squashfs", "sysfs", "tmpfs", "tracefs":
		return true
	}
	return false
}

func toGB(bytes uint64) float64 {
	return round2(float64(bytes) / (1024 * 1024 * 1024))
}

// round2 rounds v to 2 decimal places.
func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

// readSysfsBatteryCapacity is a direct sysfs fallback for when the distatus/battery
// library cannot compute a percentage (e.g. charge_full returns "no such device"
// on some ThinkPads / older kernels). Reads /sys/class/power_supply/BAT*/capacity
// which is a simple integer 0-100 written by the kernel's power-supply core and
// is always available when a battery is present.
func readSysfsBatteryCapacity() float64 {
	const sysfsBase = "/sys/class/power_supply"
	entries, err := os.ReadDir(sysfsBase)
	if err != nil {
		return 0
	}
	for _, e := range entries {
		path := sysfsBase + "/" + e.Name()
		// Only look at battery nodes.
		typeBytes, err := os.ReadFile(path + "/type")
		if err != nil || strings.TrimSpace(string(typeBytes)) != "Battery" {
			continue
		}
		capBytes, err := os.ReadFile(path + "/capacity")
		if err != nil {
			continue
		}
		val, err := strconv.ParseFloat(strings.TrimSpace(string(capBytes)), 64)
		if err != nil {
			continue
		}
		return val // 0-100
	}
	return 0
}
