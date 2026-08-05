//go:build darwin
// +build darwin

package collector

// collectDarwinPortsImpl reads open listening ports on macOS using the
// gopsutil net/connections API, which wraps the kernel sysctl interface
// (CTL_NET/PF_INET/IPPROTO_TCP/TCPCTL_PCBLIST) — no lsof required.

import (
	"fmt"

	gopsnet "github.com/shirou/gopsutil/v3/net"
)

func collectDarwinPortsImpl() []PortEntry {
	// "inet" returns TCP + UDP connections/listeners across IPv4 and IPv6.
	conns, err := gopsnet.Connections("inet")
	if err != nil {
		return nil
	}

	var ports []PortEntry
	seen := map[string]bool{}

	for _, c := range conns {
		// Include only LISTEN (TCP) and stateless (UDP).
		if c.Type == 1 /* SOCK_STREAM / TCP */ && c.Status != "LISTEN" {
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
		// Process name lookup.
		if c.Pid > 0 {
			// On macOS /proc doesn't exist; gopsutil may provide the name via
			// internal sysctl calls. Since we didn't import the process package
			// (to keep the dependency graph simple), we leave the process name
			// blank. Implementing this fully requires github.com/shirou/gopsutil/v3/process.
			entry.Process = ""
		}
		ports = append(ports, entry)
	}
	return ports
}
