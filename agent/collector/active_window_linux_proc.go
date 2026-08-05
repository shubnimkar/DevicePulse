//go:build linux
// +build linux

package collector

// linuxProcFallbackActiveWindow provides a best-effort active process name
// without any display server requirement.
//
// Strategy:
//  1. Read /proc/self/loginuid to get the current user's UID.
//  2. Walk /proc/[pid]/status to find all processes owned by that UID.
//  3. Of those, find the one that is:
//       a. NOT a kernel thread (has a /proc/[pid]/exe link).
//       b. In state "S" (sleeping/interactive) or "R" (running).
//       c. Belongs to the foreground process group of some TTY
//          (read /proc/[pid]/stat field 8 = tpgid).
//       d. Has the highest OOM score adjustment or resident set size as a
//          heuristic for "the thing the user is currently using".
//  4. Return the process name from /proc/[pid]/comm (trimmed).
//
// This is a last resort and will return the name of the most likely
// interactive process, not necessarily the exact focused GUI window.
// It works on any Linux kernel >= 2.6.26.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// linuxProcFallbackActiveWindow is the entry point called from active_window.go.
func linuxProcFallbackActiveWindow() string {
	uid := getSelfUID()

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return ""
	}

	type candidate struct {
		name         string
		rss          int64  // resident set size in pages (higher = more likely "active")
		tty          int    // controlling tty (0 = no tty / daemon)
		pgid         int    // process group ID
		tpgid        int    // foreground process group of the tty
		isForeground bool   // true when pgid == tpgid and tty != 0
	}

	var best candidate

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid := e.Name()
		if pid == "" || pid[0] < '0' || pid[0] > '9' {
			continue
		}

		// Filter by owner UID.
		if uid != "" && !procOwnedByUID("/proc/"+pid+"/status", uid) {
			continue
		}

		// Skip kernel threads (no /proc/[pid]/exe).
		exePath := "/proc/" + pid + "/exe"
		if _, err := os.Readlink(exePath); err != nil {
			continue
		}

		stat := readProcStat("/proc/" + pid + "/stat")
		if stat == nil {
			continue
		}

		// stat fields (1-indexed as per man 5 proc):
		//  1  pid        2  comm     3  state   4  ppid
		//  5  pgrp       6  session  7  tty_nr  8  tpgid
		// ... 24 rss
		if len(stat) < 24 {
			continue
		}

		state := stat[2]
		// Only consider interactive processes (running or sleeping).
		if state != "S" && state != "R" && state != "D" {
			continue
		}

		ttyNr, _ := strconv.Atoi(stat[6])
		tpgid, _  := strconv.Atoi(stat[7])
		pgid, _   := strconv.Atoi(stat[4])
		rss, _    := strconv.ParseInt(stat[23], 10, 64)

		// A foreground process has pgid == tpgid of its tty.
		isForeground := ttyNr != 0 && pgid == tpgid

		comm := readComm("/proc/" + pid + "/comm")
		if comm == "" {
			comm = filepath.Base(readCmdline("/proc/" + pid + "/cmdline"))
		}
		if comm == "" {
			continue
		}

		// Prefer foreground processes; among those, the one with the largest RSS.
		if isForeground && (!best.isForeground || rss > best.rss) {
			best = candidate{name: comm, rss: rss, tty: ttyNr, pgid: pgid, tpgid: tpgid, isForeground: true}
		} else if !best.isForeground && rss > best.rss {
			best = candidate{name: comm, rss: rss}
		}
	}

	return best.name
}

// ── /proc helpers ─────────────────────────────────────────────────────────────

// getSelfUID returns the UID of the current process as a string.
func getSelfUID() string {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "Uid:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				return fields[1]
			}
		}
	}
	return ""
}

// procOwnedByUID returns true if the process's status file shows the given UID.
func procOwnedByUID(statusPath, uid string) bool {
	data, err := os.ReadFile(statusPath)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "Uid:") {
			fields := strings.Fields(line)
			// fields: Uid: real effective saved fs
			if len(fields) >= 2 && fields[1] == uid {
				return true
			}
			return false
		}
	}
	return false
}

// readProcStat parses /proc/[pid]/stat. The second field (comm) is wrapped in
// parentheses and may contain spaces or parentheses itself, so we handle that.
// Returns a slice of fields (0-indexed, matching man-page 1-indexed minus 1).
func readProcStat(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	s := strings.TrimSpace(string(data))

	// Find the last ')' which marks the end of the comm field.
	rp := strings.LastIndex(s, ")")
	if rp < 0 {
		return nil
	}
	lp := strings.Index(s, "(")
	if lp < 0 {
		return nil
	}

	pid   := strings.TrimSpace(s[:lp])
	comm  := s[lp+1 : rp]
	rest  := strings.TrimSpace(s[rp+1:])
	fields := strings.Fields(rest)

	// Reconstruct: pid, comm, <rest fields...>
	result := make([]string, 0, 2+len(fields))
	result = append(result, pid, comm)
	result = append(result, fields...)
	return result
}

// readComm and readCmdline are defined in proc_helpers.go (shared across platforms).
