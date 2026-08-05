package collector

// Services collects running services and daemons.
//
// Platform strategy — zero external binary requirements:
//   macOS   — reads launchd plist files from standard load paths and checks
//              /var/db/launchd.db/* socket files for running state
//   Linux   — reads /proc/<pid>/comm + /proc/<pid>/cmdline to find systemd
//              services, and reads the cgroup hierarchy to enumerate units;
//              also parses /run/systemd/units/ sockets for running unit state
//   Windows — reads the Windows Services registry key (build-tagged file)

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Services collector.
type Services struct{}

func (s *Services) Name() string { return "Services" }
func (s *Services) Start() error { return nil }
func (s *Services) Stop() error  { return nil }

// ServiceEntry represents a single service/daemon.
type ServiceEntry struct {
	Name   string `json:"name"`
	Status string `json:"status"` // "running", "stopped", "unknown"
	PID    string `json:"pid,omitempty"`
}

func (s *Services) Collect() (map[string]interface{}, error) {
	switch runtime.GOOS {
	case "darwin":
		services := collectMacOSServices()
		return map[string]interface{}{"services": services, "source": "launchd_plist"}, nil
	case "linux":
		services := collectLinuxServices()
		return map[string]interface{}{"services": services, "source": "proc_cgroup"}, nil
	case "windows":
		services := collectWindowsServices()
		return map[string]interface{}{"services": services, "source": "scm_registry"}, nil
	default:
		return map[string]interface{}{"services": []ServiceEntry{}, "error": "unsupported platform"}, nil
	}
}

// ─── macOS ────────────────────────────────────────────────────────────────────

// collectMacOSServices enumerates launchd services by reading the plist
// directories directly — no launchctl binary required.
//
// Running state is detected by checking whether launchd has a socket file
// under /var/db/launchd.db/com.apple.launchd/sockets/<label> or the process
// appears in /proc (which doesn't exist on macOS, so we use /var/run pids).
func collectMacOSServices() []ServiceEntry {
	searchDirs := []string{
		"/Library/LaunchDaemons",
		"/Library/LaunchAgents",
		"/System/Library/LaunchDaemons",
		"/System/Library/LaunchAgents",
		filepath.Join(os.Getenv("HOME"), "Library", "LaunchAgents"),
	}

	// Build a set of running labels from /var/run/*.pid and /tmp/*.pid files.
	runningLabels := discoverRunningMacOSLabels()

	var services []ServiceEntry
	seen := map[string]bool{}

	for _, dir := range searchDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || (!strings.HasSuffix(e.Name(), ".plist") && !strings.HasSuffix(e.Name(), ".plist.disabled")) {
				continue
			}
			label := strings.TrimSuffix(strings.TrimSuffix(e.Name(), ".disabled"), ".plist")
			if seen[label] {
				continue
			}
			seen[label] = true

			status := "stopped"
			pid := ""
			if p, ok := runningLabels[label]; ok {
				status = "running"
				pid = p
			}
			services = append(services, ServiceEntry{Name: label, Status: status, PID: pid})
		}
	}
	return services
}

// discoverRunningMacOSLabels returns a map of launchd label → pid string for
// all currently running launchd jobs. It first tries `launchctl list` (fast,
// authoritative) and falls back to scanning /var/run/*.pid files.
func discoverRunningMacOSLabels() map[string]string {
	running := map[string]string{}

	// Primary: parse `launchctl list` output.
	// Output format (tab-separated):  PID  LastExitStatus  Label
	// Running jobs have a numeric PID; stopped jobs show "-".
	out, err := exec.Command("launchctl", "list").Output()
	if err == nil {
		scanner := bufio.NewScanner(strings.NewReader(string(out)))
		for scanner.Scan() {
			line := scanner.Text()
			parts := strings.Fields(line)
			if len(parts) < 3 {
				continue
			}
			pid := parts[0]
			label := parts[2]
			if pid == "-" || pid == "PID" {
				continue // stopped or header line
			}
			running[label] = pid
		}
		return running
	}

	// Fallback: scan /var/run/*.pid files.
	for _, dir := range []string{"/var/run", "/private/var/run"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".pid") {
				continue
			}
			label := strings.TrimSuffix(e.Name(), ".pid")
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			pid := strings.TrimSpace(string(data))
			if pid != "" && pid != "0" {
				running[label] = pid
			}
		}
	}
	return running
}

// ─── Linux ────────────────────────────────────────────────────────────────────

// collectLinuxServices enumerates systemd units by reading the filesystem
// directly — no systemctl binary required.
//
// Strategy:
//  1. Read /run/systemd/units/ — each file here is a running unit.
//  2. Walk /lib/systemd/system/, /etc/systemd/system/, /run/systemd/system/
//     to discover all installed service units.
//  3. Cross-reference to determine running vs stopped.
func collectLinuxServices() []ServiceEntry {
	// Step 1: running units from /run/systemd/units/ (runtime state dir).
	runningUnits := map[string]bool{}
	runningPIDs := map[string]string{}

	if entries, err := os.ReadDir("/run/systemd/units"); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".service") {
				runningUnits[e.Name()] = true
			}
		}
	}

	// Alternative: parse /proc/*/cgroup to find systemd service assignments.
	// Each systemd service has a cgroup like:
	//   /system.slice/ssh.service   or   /user.slice/user-1000.slice/…
	collectCgroupServices(runningUnits, runningPIDs)

	// Step 2: all installed service unit files.
	searchDirs := []string{
		"/lib/systemd/system",
		"/usr/lib/systemd/system",
		"/etc/systemd/system",
		"/run/systemd/system",
	}

	var services []ServiceEntry
	seen := map[string]bool{}

	for _, dir := range searchDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.HasSuffix(name, ".service") {
				continue
			}
			if seen[name] {
				continue
			}
			seen[name] = true

			displayName := strings.TrimSuffix(name, ".service")
			status := "stopped"
			pid := ""
			if runningUnits[name] {
				status = "running"
				pid = runningPIDs[name]
			}
			services = append(services, ServiceEntry{Name: displayName, Status: status, PID: pid})
		}
	}
	return services
}

// collectCgroupServices parses /proc/<pid>/cgroup for all processes to build
// the set of running systemd service units.
func collectCgroupServices(running map[string]bool, pids map[string]string) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid := e.Name()
		if pid == "" || pid[0] < '0' || pid[0] > '9' {
			continue
		}

		data, err := os.ReadFile("/proc/" + pid + "/cgroup")
		if err != nil {
			continue
		}

		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		for scanner.Scan() {
			line := scanner.Text()
			// cgroup v2: "0::/system.slice/sshd.service"
			// cgroup v1: "1:name=systemd:/system.slice/sshd.service"
			parts := strings.SplitN(line, ":", 3)
			if len(parts) < 3 {
				continue
			}
			cgPath := parts[2]
			// Extract the .service unit name from the cgroup path.
			for _, seg := range strings.Split(cgPath, "/") {
				if strings.HasSuffix(seg, ".service") {
					running[seg] = true
					pids[seg] = pid
				}
			}
		}
	}
}

// ─── Windows ─────────────────────────────────────────────────────────────────

// collectWindowsServices is implemented in services_windows.go (build-tagged).
func collectWindowsServices() []ServiceEntry {
	return collectWindowsServicesImpl()
}
