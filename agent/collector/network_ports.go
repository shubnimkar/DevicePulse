package collector

// NetworkPorts collects open TCP/UDP listening ports and the owning process.
//
// Platform strategy — zero external binary requirements:
//   macOS   — uses syscall.Syscall6 with NET_INET sysctl (CTL_NET path)
//              to read the socket tables from the kernel directly.
//              Falls back to parsing /proc/net on macOS (not available, so
//              we use the gopsutil net package which wraps the sysctl).
//   Linux   — reads /proc/net/tcp, /proc/net/tcp6, /proc/net/udp,
//              /proc/net/udp6 directly (no ss, no netstat, no lsof).
//              PID lookup via /proc/net/sockstat + inode → /proc/<pid>/fd/.
//   Windows — uses iphlpapi.dll (GetExtendedTcpTable / GetExtendedUdpTable)
//              via golang.org/x/sys/windows (build-tagged file).

import (
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// NetworkPorts collector.
type NetworkPorts struct{}

func (n *NetworkPorts) Name() string { return "NetworkPorts" }
func (n *NetworkPorts) Start() error { return nil }
func (n *NetworkPorts) Stop() error  { return nil }

// PortEntry describes a single open/listening port.
type PortEntry struct {
	Protocol  string `json:"protocol"`
	LocalAddr string `json:"local_addr"`
	State     string `json:"state,omitempty"`
	PID       int    `json:"pid,omitempty"`
	Process   string `json:"process,omitempty"`
}

func (n *NetworkPorts) Collect() (map[string]interface{}, error) {
	var ports []PortEntry

	switch runtime.GOOS {
	case "linux":
		ports = collectLinuxPorts()
	case "darwin":
		ports = collectDarwinPorts()
	case "windows":
		ports = collectWindowsPorts()
	}

	return map[string]interface{}{"open_ports": ports, "source": "native"}, nil
}

// ─── Linux /proc/net ─────────────────────────────────────────────────────────

// collectLinuxPorts reads socket state directly from /proc/net.
// No external binaries required.
func collectLinuxPorts() []PortEntry {
	// Build inode → pid map first (used for process name lookup).
	inodePID := buildInodePIDMap()

	var ports []PortEntry

	// TCP (IPv4) — only LISTEN state (0x0A)
	ports = append(ports, parseProcNet("/proc/net/tcp", "tcp", inodePID, true)...)
	// TCP (IPv6)
	ports = append(ports, parseProcNet("/proc/net/tcp6", "tcp6", inodePID, true)...)
	// UDP (IPv4) — all (no state concept)
	ports = append(ports, parseProcNet("/proc/net/udp", "udp", inodePID, false)...)
	// UDP (IPv6)
	ports = append(ports, parseProcNet("/proc/net/udp6", "udp6", inodePID, false)...)

	return ports
}

// parseProcNet parses a /proc/net/{tcp,tcp6,udp,udp6} file.
//
// Column layout (space-separated):
//   sl local_address rem_address st tx_queue:rx_queue tr:tm->when retrnsmt uid timeout inode
//
// local_address format: "0100007F:1F90" (hex little-endian IP : hex port)
// For IPv6: "00000000000000000000000001000000:1F90"
func parseProcNet(path, proto string, inodePID map[uint64]int, listenOnly bool) []PortEntry {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var entries []PortEntry
	lines := strings.Split(string(data), "\n")
	seen := map[string]bool{}

	for i, line := range lines {
		if i == 0 || strings.TrimSpace(line) == "" {
			continue // skip header
		}
		fields := strings.Fields(line)
		// Minimum: sl local rem st ... inode
		if len(fields) < 10 {
			continue
		}

		stateHex := fields[3]
		if listenOnly {
			// 0A = TCP_LISTEN
			if stateHex != "0A" {
				continue
			}
		}

		localHex := fields[1]
		inode, _ := strconv.ParseUint(fields[9], 10, 64)

		addr := parseHexAddr(localHex, proto)
		if addr == "" {
			continue
		}

		key := proto + ":" + addr
		if seen[key] {
			continue
		}
		seen[key] = true

		entry := PortEntry{
			Protocol:  normalizeProto(proto),
			LocalAddr: addr,
		}
		if listenOnly {
			entry.State = "LISTEN"
		}

		// Process name lookup via inode.
		if pid, ok := inodePID[inode]; ok {
			entry.PID = pid
			entry.Process = readComm(fmt.Sprintf("/proc/%d/comm", pid))
		}

		entries = append(entries, entry)
	}
	return entries
}

// parseHexAddr converts the /proc/net hex address format to "ip:port".
// IPv4: "0100007F:1F90"  → "127.0.0.1:8080"
// IPv6: "00000000000000000000000001000000:1F90" → "[::1]:8080"
func parseHexAddr(hexAddr, proto string) string {
	parts := strings.SplitN(hexAddr, ":", 2)
	if len(parts) != 2 {
		return ""
	}
	portVal, err := strconv.ParseUint(parts[1], 16, 16)
	if err != nil {
		return ""
	}

	ipHex := parts[0]
	isV6 := strings.Contains(proto, "6") || len(ipHex) > 8

	var ipStr string
	if isV6 {
		// 32 hex chars = 16 bytes, stored as 4 little-endian uint32s.
		if len(ipHex) != 32 {
			return ""
		}
		b, err := hex.DecodeString(ipHex)
		if err != nil {
			return ""
		}
		// Reverse each 4-byte group (little-endian).
		for i := 0; i < 16; i += 4 {
			b[i], b[i+3] = b[i+3], b[i]
			b[i+1], b[i+2] = b[i+2], b[i+1]
		}
		ip := net.IP(b)
		ipStr = "[" + ip.String() + "]"
	} else {
		if len(ipHex) != 8 {
			return ""
		}
		b, err := hex.DecodeString(ipHex)
		if err != nil {
			return ""
		}
		// Little-endian: reverse.
		ip := net.IPv4(b[3], b[2], b[1], b[0])
		ipStr = ip.String()
	}
	return fmt.Sprintf("%s:%d", ipStr, portVal)
}

func normalizeProto(p string) string {
	if strings.HasPrefix(p, "tcp") {
		return "tcp"
	}
	if strings.HasPrefix(p, "udp") {
		return "udp"
	}
	return p
}

// buildInodePIDMap walks /proc/<pid>/fd to build a map from socket inode → pid.
// This allows us to resolve which process owns each socket.
func buildInodePIDMap() map[uint64]int {
	result := map[uint64]int{}

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return result
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pidStr := e.Name()
		if pidStr == "" || pidStr[0] < '0' || pidStr[0] > '9' {
			continue
		}
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			continue
		}

		fdDir := filepath.Join("/proc", pidStr, "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}

		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil {
				continue
			}
			// Socket symlinks look like: "socket:[12345678]"
			if !strings.HasPrefix(link, "socket:[") {
				continue
			}
			inodeStr := link[len("socket:[") : len(link)-1]
			inode, err := strconv.ParseUint(inodeStr, 10, 64)
			if err != nil {
				continue
			}
			result[inode] = pid
		}
	}
	return result
}

// ─── macOS ───────────────────────────────────────────────────────────────────

// collectDarwinPorts uses gopsutil's net package which reads the kernel socket
// tables via sysctl(3) — no lsof binary required.
func collectDarwinPorts() []PortEntry {
	return collectDarwinPortsImpl() // darwin_ports.go
}

// ─── Windows ─────────────────────────────────────────────────────────────────

func collectWindowsPorts() []PortEntry {
	return collectWindowsPortsImpl() // network_ports_windows.go
}
