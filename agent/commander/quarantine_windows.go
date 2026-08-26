//go:build windows

package commander

import (
	"fmt"
	"strings"
)

// Quarantine on Windows uses Windows Firewall via netsh.
//
// Windows Firewall block rules override allow rules, so a blanket "block all"
// rule would also kill API traffic. Instead we flip the default outbound
// policy to block and add explicit allow rules for the API host, DNS and DHCP.
// Release restores the standard Windows defaults (blockinbound,allowoutbound).

const wfRulePrefix = "DevicePulseQ"

func quarantineEnable(apiURL string) (string, string) {
	if out, err := execCommand("netsh", "advfirewall", "show", "allprofiles", "state"); err != nil {
		return StatusUnsupported, "netsh advfirewall not available: " + firstLine(out)
	}
	apiIPs := resolveHostIPs(apiHost(apiURL))

	type rule struct {
		name string
		args []string
	}
	rules := []rule{
		{wfRulePrefix + "_DNS_UDP", []string{"dir=out", "action=allow", "protocol=udp", "remoteport=53"}},
		{wfRulePrefix + "_DNS_TCP", []string{"dir=out", "action=allow", "protocol=tcp", "remoteport=53"}},
		{wfRulePrefix + "_DHCP_OUT", []string{"dir=out", "action=allow", "protocol=udp", "localport=68", "remoteport=67"}},
		{wfRulePrefix + "_DHCP_IN", []string{"dir=in", "action=allow", "protocol=udp", "localport=68", "remoteport=67"}},
	}
	if len(apiIPs) > 0 {
		rules = append(rules, rule{wfRulePrefix + "_API",
			[]string{"dir=out", "action=allow", "remoteip=" + strings.Join(apiIPs, ",")}})
		rules = append(rules, rule{wfRulePrefix + "_API_IN",
			[]string{"dir=in", "action=allow", "remoteip=" + strings.Join(apiIPs, ",")}})
	}
	for _, r := range rules {
		args := append([]string{"advfirewall", "firewall", "add", "rule",
			"name=" + r.name, "enable=yes", "profile=any"}, r.args...)
		if out, err := execCommand("netsh", args...); err != nil {
			return StatusFailed, fmt.Sprintf("netsh add %s failed: %s", r.name, firstLine(out))
		}
	}

	if out, err := execCommand("netsh", "advfirewall", "set", "allprofiles",
		"firewallpolicy", "blockinbound,blockoutbound"); err != nil {
		return StatusFailed, "cannot set firewall policy to deny-by-default: " + firstLine(out)
	}

	detail := fmt.Sprintf("host quarantined via Windows Firewall (default deny in/out; allowed: DNS, DHCP, API %s)", strings.Join(apiIPs, ","))
	if len(apiIPs) == 0 {
		detail = "host quarantined via Windows Firewall (WARNING: API host unresolved — agent will lose server connectivity until released)"
	}
	logLine(detail)
	return StatusSuccess, detail
}

func quarantineRelease() (string, string) {
	var notes []string

	// Restore standard Windows default policy before removing allows so
	// connectivity is restored even if rule deletion partially fails.
	if out, err := execCommand("netsh", "advfirewall", "set", "allprofiles",
		"firewallpolicy", "blockinbound,allowoutbound"); err != nil {
		notes = append(notes, "restore policy: "+firstLine(out))
	}

	names := []string{
		wfRulePrefix + "_API", wfRulePrefix + "_API_IN",
		wfRulePrefix + "_DNS_UDP", wfRulePrefix + "_DNS_TCP",
		wfRulePrefix + "_DHCP_OUT", wfRulePrefix + "_DHCP_IN",
	}
	for _, n := range names {
		if out, err := execCommand("netsh", "advfirewall", "firewall", "delete", "rule", "name="+n); err != nil && !strings.Contains(out, "No rules") {
			notes = append(notes, n+": "+firstLine(out))
		}
	}

	if len(notes) > 0 {
		return StatusFailed, "release incomplete: " + strings.Join(notes, "; ")
	}
	logLine("quarantine released")
	return StatusSuccess, "Windows Firewall policy restored to defaults, quarantine rules removed"
}
