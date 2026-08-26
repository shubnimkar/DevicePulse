package commander

import (
	"log"
	"net"
	"strings"
)

// Shared helpers for the per-platform quarantine implementations.

// resolveHostIPs resolves an API hostname to up to 4 IP addresses so firewall
// rules can allow-list it. Returns nil when resolution fails.
func resolveHostIPs(host string) []string {
	if host == "" {
		return nil
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return nil
	}
	var out []string
	for _, ip := range ips {
		out = append(out, ip.String())
		if len(out) >= 4 {
			break
		}
	}
	return out
}

func logf(format string, args ...interface{}) {
	log.Printf(format, args...)
}

func logLine(msg string) {
	logf("Commander: %s", msg)
}

// firstLine returns the first line of a multi-line command output string.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

