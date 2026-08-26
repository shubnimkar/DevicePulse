//go:build linux

package commander

import (
	"fmt"
	"strings"
)

// quarantineChain is a dedicated iptables chain so enable/release is fully
// idempotent and never touches rules it does not own.
const quarantineChain = "DEVICEPULSE_Q"

// quarantineEnable isolates the host with iptables:
//   - loopback, established connections, DNS, DHCP and the API host stay up
//   - everything else inbound/outbound is dropped
//
// Requires root (the agent system service already runs as root).
func quarantineEnable(apiURL string) (string, string) {
	if out, err := execCommand("iptables", "--version"); err != nil {
		return StatusUnsupported, "iptables not available: " + out
	}

	apiIPs := resolveHostIPs(apiHost(apiURL))

	// Create + flush the chain.
	execCommand("iptables", "-N", quarantineChain) // ignore "already exists"
	if out, err := execCommand("iptables", "-F", quarantineChain); err != nil {
		return StatusFailed, "cannot reset quarantine chain: " + out
	}

	rules := [][]string{
		{"-i", "lo", "-j", "ACCEPT"},
		{"-o", "lo", "-j", "ACCEPT"},
		{"-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-j", "ACCEPT"},
		{"-p", "udp", "--dport", "53", "-j", "ACCEPT"}, // DNS out
		{"-p", "tcp", "--dport", "53", "-j", "ACCEPT"},
		{"-p", "udp", "--sport", "67:68", "--dport", "67:68", "-j", "ACCEPT"}, // DHCP
	}
	for _, ip := range apiIPs {
		rules = append(rules, []string{"-d", ip, "-j", "ACCEPT"})
		rules = append(rules, []string{"-s", ip, "-j", "ACCEPT"})
	}
	rules = append(rules,
		[]string{"-j", "DROP"}, // everything else in this chain is blocked
	)
	for _, r := range rules {
		args := append([]string{"-A", quarantineChain}, r...)
		if out, err := execCommand("iptables", args...); err != nil {
			return StatusFailed, fmt.Sprintf("iptables %v failed: %s", args, out)
		}
	}

	// Hook INPUT and OUTPUT into the chain (idempotent).
	var failures []string
	for _, hook := range [][]string{
		{"INPUT"},
		{"OUTPUT"},
	} {
		args := append([]string{"-C"}, hook[0], "-j", quarantineChain)
		if _, err := execCommand("iptables", args...); err != nil {
			if out, err := execCommand("iptables", "-I", hook[0], "1", "-j", quarantineChain); err != nil {
				failures = append(failures, fmt.Sprintf("%s hook: %s", hook[0], out))
			}
		}
	}
	if len(failures) > 0 {
		return StatusFailed, strings.Join(failures, "; ")
	}

	detail := fmt.Sprintf("host quarantined via iptables chain %s (allowed: loopback, established, DNS, DHCP, API %s)",
		quarantineChain, strings.Join(apiIPs, ","))
	if len(apiIPs) == 0 {
		detail = fmt.Sprintf("host quarantined via iptables chain %s (WARNING: API host could not be resolved; only DNS/loopback allowed)", quarantineChain)
	}
	logLine(detail)
	return StatusSuccess, detail
}

func quarantineRelease() (string, string) {
	var notes []string
	// Remove hooks first so traffic resumes even if chain flush fails.
	for _, h := range []string{"INPUT", "OUTPUT"} {
		if out, err := execCommand("iptables", "-D", h, "-j", quarantineChain); err != nil {
			notes = append(notes, fmt.Sprintf("unhook %s: %s", h, out))
		}
	}
	if out, err := execCommand("iptables", "-F", quarantineChain); err != nil {
		notes = append(notes, "flush: "+out)
	}
	if out, err := execCommand("iptables", "-X", quarantineChain); err != nil {
		notes = append(notes, "delete chain: "+out)
	}
	if len(notes) > 0 {
		return StatusFailed, "release incomplete: " + strings.Join(notes, "; ")
	}
	logLine("quarantine released")
	return StatusSuccess, "quarantine rules removed, full connectivity restored"
}
