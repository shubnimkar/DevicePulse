//go:build windows
// +build windows

package collector

// collectWindowsPortsImpl reads TCP/UDP listeners on Windows using
// iphlpapi via gopsutil — no netstat.exe binary required.

import (
	"fmt"

	gopsnet "github.com/shirou/gopsutil/v3/net"
)

func collectWindowsPortsImpl() []PortEntry {
	conns, err := gopsnet.Connections("inet")
	if err != nil {
		return nil
	}

	var ports []PortEntry
	seen := map[string]bool{}

	for _, c := range conns {
		if c.Type == 1 && c.Status != "LISTEN" {
			continue
		}
		if c.Laddr.Port == 0 {
			continue
		}
		proto := "tcp"
		if c.Type == 2 {
			proto = "udp"
		}
		addr := fmt.Sprintf("%s:%d", c.Laddr.IP, c.Laddr.Port)
		key := proto + ":" + addr
		if seen[key] {
			continue
		}
		seen[key] = true

		entry := PortEntry{
			Protocol:  proto,
			LocalAddr: addr,
			PID:       int(c.Pid),
		}
		if c.Type == 1 {
			entry.State = "LISTEN"
		}
		ports = append(ports, entry)
	}
	return ports
}
