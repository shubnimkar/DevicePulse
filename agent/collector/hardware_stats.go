package collector

import (
	"fmt"
	"math"
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
	TotalGB     float64 `json:"total_gb"`
	UsedGB      float64 `json:"used_gb"`
	FreeGB      float64 `json:"free_gb"`
	UsedPercent float64 `json:"used_percent"`
}

// NetStat holds cumulative I/O per interface.
type NetStat struct {
	Interface string `json:"interface"`
	BytesSent uint64 `json:"bytes_sent"`
	BytesRecv uint64 `json:"bytes_recv"`
	PacketsSent uint64 `json:"packets_sent"`
	PacketsRecv uint64 `json:"packets_recv"`
}

// BatteryStat holds battery info (may be empty on desktops).
type BatteryStat struct {
	Available    bool    `json:"available"`
	Percent      float64 `json:"percent"`       // 0–100
	Plugged      bool    `json:"plugged"`       // true = AC / charging source connected
	Charging     bool    `json:"charging"`      // true = actively charging
	ChargeRateW  float64 `json:"charge_rate_w"` // charge/discharge rate in Watts (negative = discharging)
	State        string  `json:"state"`         // "charging", "discharging", "full", "unknown"
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
			usage, err := disk.Usage(p.Mountpoint)
			if err != nil || usage.Total == 0 {
				continue
			}
			disks = append(disks, DiskStat{
				Mount:       p.Mountpoint,
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
	if battErr == nil && len(batteries) > 0 {
		b := batteries[0] // use first battery (covers most laptops)
		batt.Available = true

		// Percent: Current / Full * 100
		// b.Current and b.Full are in mWh; ratio gives %
		if b.Full > 0 {
			batt.Percent = round2(b.Current / b.Full * 100)
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

func toGB(bytes uint64) float64 {
	return round2(float64(bytes) / (1024 * 1024 * 1024))
}

func round2(v float64) float64 {
	return float64(int(v*100)) / 100
}
