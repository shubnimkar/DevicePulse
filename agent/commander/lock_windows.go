//go:build windows

package commander

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// lockScreen locks the interactive console session on Windows.
//
// The agent service runs as SYSTEM in session 0, where LockWorkStation has no
// effect. Instead we acquire the active console session's primary token via
// WTSQueryUserToken and start rundll32 inside the user's session; that
// invocation calls LockWorkStation in the right desktop.
func lockScreen() (string, string) {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getConsoleSessionID := kernel32.NewProc("WTSGetActiveConsoleSessionId")
	wtsapi32 := syscall.NewLazyDLL("wtsapi32.dll")
	queryUserToken := wtsapi32.NewProc("WTSQueryUserToken")

	sessionID, _, _ := getConsoleSessionID.Call()
	const invalidSession = 0xFFFFFFFF
	if sessionID == invalidSession {
		return StatusFailed, "no active console session found"
	}

	var hToken syscall.Handle
	ret, _, callErr := queryUserToken.Call(sessionID, uintptr(unsafe.Pointer(&hToken)))
	if ret == 0 {
		return StatusFailed, fmt.Sprintf("WTSQueryUserToken failed: %v", callErr)
	}
	defer syscall.CloseHandle(hToken)

	token := windows.Token(hToken)
	defer token.Close()

	cmdline, err := syscall.UTF16PtrFromString(`rundll32.exe user32.dll,LockWorkStation`)
	if err != nil {
		return StatusFailed, fmt.Sprintf("build command line: %v", err)
	}

	var si windows.StartupInfo
	si.Flags = 0x00000001 // STARTF_USESHOWWINDOW
	si.ShowWindow = 0     // SW_HIDE
	var pi windows.ProcessInformation

	err = windows.CreateProcessAsUser(token, nil, cmdline, nil, nil, false,
		windows.CREATE_NO_WINDOW|windows.CREATE_UNICODE_ENVIRONMENT, nil, nil, &si, &pi)
	if err != nil {
		return StatusFailed, fmt.Sprintf("CreateProcessAsUser(rundll32 LockWorkStation): %v", err)
	}
	windows.CloseHandle(pi.Process)
	windows.CloseHandle(pi.Thread)

	return StatusSuccess, fmt.Sprintf("LockWorkStation dispatched in console session %d", sessionID)
}
