//go:build darwin

package commander

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const (
	pfAnchorName = "io.devicepulse.quarantine"
	pfAnchorPath = "/etc/pf.anchors/io.devicepulse.quarantine"
	pfConfPath   = "/etc/pf.conf"
	pfBackupPath = "/etc/pf.conf.devicepulse.bak"
)

// quarantineEnable isolates the host with a pf anchor:
//   - loopback, DNS, DHCP and traffic to the API host stay up
//   - all other inbound/outbound traffic is dropped
//
// Requires root (the agent LaunchDaemon runs as root). The original
// /etc/pf.conf is backed up so release restores it byte-for-byte.
func quarantineEnable(apiURL string) (string, string) {
	if _, err := exec.LookPath("pfctl"); err != nil {
		return StatusUnsupported, "pfctl not available"
	}
	apiIPs := resolveHostIPs(apiHost(apiURL))

	var b strings.Builder
	b.WriteString("# Managed by DevicePulse agent — do not edit\n")
	b.WriteString("set skip on lo0\n")
	b.WriteString("pass out quick inet proto udp to any port 53 keep state\n")   // DNS
	b.WriteString("pass out quick inet proto tcp to any port 53 keep state\n")
	b.WriteString("pass out quick inet proto udp from any port 68 to any port 67 keep state\n") // DHCP
	b.WriteString("pass in quick inet proto udp from any port 67 to any port 68 keep state\n")
	for _, ip := range apiIPs {
		fmt.Fprintf(&b, "pass out quick to %s keep state\n", ip)
		fmt.Fprintf(&b, "pass in quick from %s keep state\n", ip)
	}
	b.WriteString("block drop out quick all\n")
	b.WriteString("block drop in quick all\n")

	if err := os.WriteFile(pfAnchorPath, []byte(b.String()), 0644); err != nil {
		return StatusFailed, fmt.Sprintf("cannot write pf anchor: %v", err)
	}

	conf, err := os.ReadFile(pfConfPath)
	if err != nil {
		return StatusFailed, fmt.Sprintf("cannot read %s: %v", pfConfPath, err)
	}
	confStr := string(conf)
	if !strings.Contains(confStr, `anchor "`+pfAnchorName+`"`) {
		_ = os.WriteFile(pfBackupPath, conf, 0644) // backup once
		var sb strings.Builder
		sb.WriteString(strings.TrimRight(confStr, "\n"))
		sb.WriteString("\n\n# Added by DevicePulse agent\n")
		sb.WriteString(fmt.Sprintf("anchor %q\n", pfAnchorName))
		sb.WriteString(fmt.Sprintf("load anchor %q from %q\n", pfAnchorName, pfAnchorPath))
		if err := os.WriteFile(pfConfPath, []byte(sb.String()), 0644); err != nil {
			return StatusFailed, fmt.Sprintf("cannot update %s: %v", pfConfPath, err)
		}
	}

	if out, err := execCommand("pfctl", "-f", pfConfPath); err != nil {
		return StatusFailed, "pfctl load failed: " + out
	}
	execCommand("pfctl", "-E") // enable pf if not already (best effort)

	detail := fmt.Sprintf("host quarantined via pf anchor %s (allowed: loopback, DNS, DHCP, API %s)", pfAnchorName, strings.Join(apiIPs, ","))
	if len(apiIPs) == 0 {
		detail = fmt.Sprintf("host quarantined via pf anchor %s (WARNING: API host unresolved; only loopback/DNS/DHCP allowed)", pfAnchorName)
	}
	logLine(detail)
	return StatusSuccess, detail
}

func quarantineRelease() (string, string) {
	var notes []string

	conf, err := os.ReadFile(pfConfPath)
	if err != nil {
		notes = append(notes, fmt.Sprintf("read %s: %v", pfConfPath, err))
	} else {
		var kept []string
		for _, line := range strings.Split(string(conf), "\n") {
			t := strings.TrimSpace(line)
			if t == fmt.Sprintf("anchor %q", pfAnchorName) ||
				strings.HasPrefix(t, fmt.Sprintf("load anchor %q", pfAnchorName)) ||
				t == "# Added by DevicePulse agent" ||
				strings.Contains(t, "Added by DevicePulse") {
				continue
			}
			kept = append(kept, line)
		}
		if err := os.WriteFile(pfConfPath, []byte(strings.Join(kept, "\n")), 0644); err != nil {
			notes = append(notes, fmt.Sprintf("rewrite %s: %v", pfConfPath, err))
		}
		if out, err := execCommand("pfctl", "-f", pfConfPath); err != nil {
			notes = append(notes, "pfctl reload: "+out)
		}
	}
	os.Remove(pfAnchorPath)

	if len(notes) > 0 {
		return StatusFailed, "release incomplete: " + strings.Join(notes, "; ")
	}
	logLine("quarantine released")
	return StatusSuccess, "quarantine rules removed, full connectivity restored"
}
