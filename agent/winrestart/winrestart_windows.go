//go:build windows

// Package winrestart relaunches the agent on Windows, where an executable cannot
// replace its own process image the way syscall.Exec does on Unix.
package winrestart

import (
	"fmt"
	"log"
	"os"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
)

// Restart ends this process and brings the agent back up from exePath. It does
// not return.
//
//   - As a Windows service it exits with a failure status: the service recovery
//     actions the installer configures (restart, also on non-crash failures)
//     start the service again from whatever binary is on disk now.
//   - Otherwise a detached helper waits for this process to exit, so the
//     single-instance mutex is free, and then starts the agent again.
func Restart(exePath string) {
	if isService, err := svc.IsWindowsService(); err == nil && isService {
		log.Printf("Restart: exiting so the service manager restarts the service")
		os.Exit(1)
	}

	args := make([]string, 0, len(os.Args))
	for _, a := range os.Args[1:] {
		args = append(args, syscall.EscapeArg(a))
	}
	cmdline := fmt.Sprintf(`cmd.exe /c ping -n 4 127.0.0.1 >nul & "%s" %s`, exePath, strings.Join(args, " "))
	comspec := os.Getenv("ComSpec")
	if comspec == "" {
		comspec = `C:\Windows\System32\cmd.exe`
	}
	proc, err := os.StartProcess(comspec, []string{"cmd.exe"}, &os.ProcAttr{
		Files: []*os.File{nil, nil, nil},
		Sys: &syscall.SysProcAttr{
			CmdLine:       cmdline,
			HideWindow:    true,
			CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP,
		},
	})
	if err != nil {
		log.Printf("Restart: could not start relaunch helper: %v", err)
		os.Exit(1)
	}
	_ = proc.Release()
	os.Exit(0)
}
