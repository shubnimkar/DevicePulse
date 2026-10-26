//go:build linux

package commander

import (
	"fmt"
	"strings"
)

// quarantineChain is a dedicated chain (in both iptables and ip6tables) so
// enable/release is fully idempotent and never touches rules it does not own.
const quarantineChain = "DEVICEPULSE_Q"

// splitIPFamilies separates resolved addresses: iptables rejects IPv6 literals and
// ip6tables rejects IPv4 ones, so each family needs its own rule set.
func splitIPFamilies(ips []string) (v4, v6 []string) {
	for _, ip := range ips {
		if strings.Contains(ip, ":") {
			v6 = append(v6, ip)
		} else {
			v4 = append(v4, ip)
		}
	}
	return v4, v6
}

// quarantineEnable isolates the host:
//   - loopback, established connections, DNS, DHCP and the API host stay up
//   - everything else inbound/outbound is dropped, for IPv4 and IPv6
//
// Requires root (the agent system service already runs as root). If IPv6
// isolation cannot be applied the whole operation is rolled back, so the host is
// never left half-isolated with an open IPv6 path.
func quarantineEnable(apiURL string) (string, string) {
	if out, err := execCommand("iptables", "--version"); err != nil {
		return StatusUnsupported, "iptables not available: " + out
	}

	apiIPs := resolveHostIPs(apiHost(apiURL))
	v4, v6 := splitIPFamilies(apiIPs)

	if status, detail := applyQuarantineChain("iptables", v4, false); status != StatusSuccess {
		return status, detail
	}

	note := ""
	if _, err := execCommand("ip6tables", "--version"); err == nil {
		if status, detail := applyQuarantineChain("ip6tables", v6, true); status != StatusSuccess {
			quarantineRelease()
			return StatusFailed, "IPv6 isolation failed, rolled back: " + detail
		}
	} else {
		note = " WARNING: ip6tables not available, IPv6 traffic is NOT isolated."
	}

	detail := fmt.Sprintf("host quarantined via iptables chain %s (allowed: loopback, established, DNS, DHCP, API %s)",
		quarantineChain, strings.Join(apiIPs, ","))
	if len(apiIPs) == 0 {
		detail = fmt.Sprintf("host quarantined via iptables chain %s (WARNING: API host could not be resolved; only DNS/loopback allowed)", quarantineChain)
	}
	detail += note
	logLine(detail)
	return StatusSuccess, detail
}

// applyQuarantineChain builds the isolation chain with one iptables-compatible
// binary and hooks it into INPUT and OUTPUT.
func applyQuarantineChain(bin string, apiIPs []string, v6 bool) (string, string) {
	execCommand(bin, "-N", quarantineChain) // ignore "already exists"
	if out, err := execCommand(bin, "-F", quarantineChain); err != nil {
		return StatusFailed, bin + ": cannot reset quarantine chain: " + out
	}

	rules := [][]string{
		{"-i", "lo", "-j", "ACCEPT"},
		{"-o", "lo", "-j", "ACCEPT"},
		{"-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-j", "ACCEPT"},
		{"-p", "udp", "--dport", "53", "-j", "ACCEPT"}, // DNS out
		{"-p", "tcp", "--dport", "53", "-j", "ACCEPT"},
	}
	if v6 {
		rules = append(rules,
			[]string{"-p", "udp", "--sport", "546:547", "--dport", "546:547", "-j", "ACCEPT"}, // DHCPv6
			// Neighbour discovery and router advertisements are carried in ICMPv6;
			// without them even the allowed API address becomes unreachable.
			[]string{"-p", "ipv6-icmp", "-j", "ACCEPT"},
		)
	} else {
		rules = append(rules, []string{"-p", "udp", "--sport", "67:68", "--dport", "67:68", "-j", "ACCEPT"}) // DHCP
	}
	for _, ip := range apiIPs {
		rules = append(rules, []string{"-d", ip, "-j", "ACCEPT"}, []string{"-s", ip, "-j", "ACCEPT"})
	}
	rules = append(rules, []string{"-j", "DROP"}) // everything else in this chain is blocked

	for _, r := range rules {
		args := append([]string{"-A", quarantineChain}, r...)
		if out, err := execCommand(bin, args...); err != nil {
			return StatusFailed, fmt.Sprintf("%s %v failed: %s", bin, args, out)
		}
	}

	// Hook INPUT and OUTPUT into the chain (idempotent).
	var failures []string
	for _, hook := range []string{"INPUT", "OUTPUT"} {
		if _, err := execCommand(bin, "-C", hook, "-j", quarantineChain); err != nil {
			if out, err := execCommand(bin, "-I", hook, "1", "-j", quarantineChain); err != nil {
				failures = append(failures, fmt.Sprintf("%s %s hook: %s", bin, hook, out))
			}
		}
	}
	if len(failures) > 0 {
		return StatusFailed, strings.Join(failures, "; ")
	}
	return StatusSuccess, ""
}

func quarantineRelease() (string, string) {
	var notes []string
	for _, bin := range []string{"iptables", "ip6tables"} {
		if _, err := execCommand(bin, "--version"); err != nil {
			continue
		}
		// Remove hooks first so traffic resumes even if chain flush fails. A hook
		// that is not there just means quarantine was not active for this family.
		for _, h := range []string{"INPUT", "OUTPUT"} {
			if _, err := execCommand(bin, "-C", h, "-j", quarantineChain); err != nil {
				continue
			}
			if out, err := execCommand(bin, "-D", h, "-j", quarantineChain); err != nil {
				notes = append(notes, fmt.Sprintf("%s unhook %s: %s", bin, h, out))
			}
		}
		if _, err := execCommand(bin, "-L", quarantineChain, "-n"); err != nil {
			continue // chain absent
		}
		if out, err := execCommand(bin, "-F", quarantineChain); err != nil {
			notes = append(notes, bin+" flush: "+out)
		}
		if out, err := execCommand(bin, "-X", quarantineChain); err != nil {
			notes = append(notes, bin+" delete chain: "+out)
		}
	}
	if len(notes) > 0 {
		return StatusFailed, "release incomplete: " + strings.Join(notes, "; ")
	}
	logLine("quarantine released")
	return StatusSuccess, "quarantine rules removed, full connectivity restored"
}
