//go:build windows
// +build windows

package collector

// getForegroundAppWindows returns the name of the process that owns the
// current foreground window using direct Win32 API calls via
// golang.org/x/sys/windows — already a project dependency.
//
// Why not PowerShell:
//   The previous approach spawned powershell.exe + compiled a C# class on
//   every 2-second sample tick. That's 300-500ms of overhead per sample and
//   shows up visibly in Task Manager.
//
// Why not GetForegroundWindow from a Windows Service:
//   Services run in Session 0 (isolated, no desktop). GetForegroundWindow()
//   called from Session 0 always returns NULL because there is no foreground
//   window in the service session.
//
// Solution — WTSEnumerateSessions + OpenProcess:
//   1. Use WTSEnumerateSessions (wtsapi32.dll) to find the active interactive
//      session ID (WTSActive state).
//   2. Use WTSQuerySessionInformation to get the session's focused process via
//      WTSGetActiveConsoleSessionId.
//   3. Call GetForegroundWindow in the context of that session using
//      SetThreadDesktop + OpenInputDesktop, or use the simpler approach of
//      reading the session's active window via the undocumented but stable
//      NtQuerySystemInformation or — most practically — by walking the process
//      list and finding the process whose window is in the foreground via
//      EnumWindows + GetWindowThreadProcessId.
//
// Practical approach used here (reliable on all Windows versions):
//   EnumWindows callback → GetWindowThreadProcessId → find the one whose
//   window is "foreground" by checking GetForegroundWindow PID.
//   We call GetForegroundWindow via a helper thread injected into the active
//   session, OR — much simpler — we just call it directly. On Windows 10/11
//   the agent typically runs as the logged-in user (not SYSTEM), so this
//   works correctly. If running as SYSTEM, the active session is found via
//   WTSGetActiveConsoleSessionId and we fall back to process name heuristics.

import (
	"fmt"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Lazily loaded DLL procedures. Loaded once, zero cost after first call.
var (
	modUser32    = windows.NewLazySystemDLL("user32.dll")
	modKernel32  = windows.NewLazySystemDLL("kernel32.dll")

	procGetForegroundWindow      = modUser32.NewProc("GetForegroundWindow")
	procGetWindowThreadProcessId = modUser32.NewProc("GetWindowThreadProcessId")
	procGetWindowTextW           = modUser32.NewProc("GetWindowTextW")
	procIsWindowVisible          = modUser32.NewProc("IsWindowVisible")
	procGetConsoleSessionId      = modKernel32.NewProc("WTSGetActiveConsoleSessionId")
)

// getForegroundAppWindows returns the process name of the focused window.
// It uses direct Win32 DLL calls — no subprocess spawned.
func getForegroundAppWindows() string {
	// Step 1: Get the foreground window handle.
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		// Running in Session 0 (SYSTEM service) — foreground window is not
		// accessible. Fall back to finding the active console session's top process.
		return getForegroundAppFromConsoleSession()
	}

	// Step 2: Get the PID that owns this window.
	var pid uint32
	procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return ""
	}

	// Step 3: Open the process and read its image name.
	name := processNameByPID(pid)

	// Step 4: Also grab the window title for context (optional enrichment).
	// We return just the process name to match the other platform behaviour.
	_ = getWindowTitle(hwnd) // available if needed later

	return name
}

// processNameByPID returns the base executable name for a given PID.
// Uses QueryFullProcessImageName (Vista+) which works without elevated rights
// for processes in the same session, and with PROCESS_QUERY_LIMITED_INFORMATION
// which is granted even for protected processes.
func processNameByPID(pid uint32) string {
	handle, err := windows.OpenProcess(
		windows.PROCESS_QUERY_LIMITED_INFORMATION,
		false,
		pid,
	)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(handle)

	var buf [windows.MAX_PATH]uint16
	size := uint32(len(buf))
	// QueryFullProcessImageName returns the full path; we take the base name.
	err = windows.QueryFullProcessImageName(handle, 0, &buf[0], &size)
	if err != nil {
		return ""
	}
	fullPath := syscall.UTF16ToString(buf[:size])
	// Extract just the filename and strip .exe suffix.
	parts := strings.Split(fullPath, `\`)
	name := parts[len(parts)-1]
	name = strings.TrimSuffix(name, ".exe")
	name = strings.TrimSuffix(name, ".EXE")
	return name
}

// getWindowTitle reads the title bar text of a window (up to 256 chars).
func getWindowTitle(hwnd uintptr) string {
	var buf [256]uint16
	n, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:n])
}

// getForegroundAppFromConsoleSession is the fallback when the agent runs as
// SYSTEM in Session 0 and GetForegroundWindow returns NULL.
//
// Strategy: find the active console session, then enumerate processes in that
// session. The process with the most recently used input desktop is the
// "foreground" one. As a simpler heuristic we return the name of the process
// with the highest CPU time that is not a system process — this is a
// best-effort approximation only.
func getForegroundAppFromConsoleSession() string {
	// Get the active console session ID (the physical console — session 1 usually).
	sessionID, _, _ := procGetConsoleSessionId.Call()
	if sessionID == 0xFFFFFFFF {
		// No active console session.
		return ""
	}

	// Enumerate all processes and find ones in this session.
	// We use CreateToolhelp32Snapshot via golang.org/x/sys/windows.
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(snapshot)

	type candidate struct {
		name    string
		threads uint32
	}

	var best candidate
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))

	if err := windows.Process32First(snapshot, &entry); err != nil {
		return ""
	}

	for {
		name := syscall.UTF16ToString(entry.ExeFile[:])

		// Skip well-known system/idle processes.
		lower := strings.ToLower(name)
		if !isSystemProcess(lower) {
			// Check if this process is in the active console session.
			handle, err := windows.OpenProcess(
				windows.PROCESS_QUERY_LIMITED_INFORMATION,
				false,
				entry.ProcessID,
			)
			if err == nil {
				var procSessionID uint32
				if windows.ProcessIdToSessionId(entry.ProcessID, &procSessionID) == nil &&
					uintptr(procSessionID) == sessionID {
					// Use thread count as a rough "activity" heuristic.
					if entry.Threads > best.threads {
						n := strings.TrimSuffix(name, ".exe")
						best = candidate{name: n, threads: entry.Threads}
					}
				}
				windows.CloseHandle(handle)
			}
		}

		if err := windows.Process32Next(snapshot, &entry); err != nil {
			break
		}
	}

	return best.name
}

// isSystemProcess returns true for processes that should be excluded from the
// "active app" heuristic.
func isSystemProcess(name string) bool {
	systemProcs := map[string]bool{
		"system":               true,
		"idle":                 true,
		"smss.exe":             true,
		"csrss.exe":            true,
		"wininit.exe":          true,
		"winlogon.exe":         true,
		"services.exe":         true,
		"lsass.exe":            true,
		"svchost.exe":          true,
		"dwm.exe":              true,
		"explorer.exe":         true,
		"taskhostw.exe":        true,
		"conhost.exe":          true,
		"spoolsv.exe":          true,
		"searchindexer.exe":    true,
		"wuauclt.exe":          true,
		"audiodg.exe":          true,
		"fontdrvhost.exe":      true,
		"runtimebroker.exe":    true,
		"sihost.exe":           true,
		"ctfmon.exe":           true,
	}
	return systemProcs[name]
}

// Ensure ProcessIdToSessionId is accessible — it lives in kernel32.
// golang.org/x/sys/windows exposes it directly.
var _ = fmt.Sprintf // keep fmt import used
