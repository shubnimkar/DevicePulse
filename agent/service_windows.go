//go:build windows
// +build windows

package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const windowsServiceName = "DevicePulseAgent"

var singleInstanceMutex windows.Handle

func runPlatformService() bool {
	dataDir = resolveDataDir()
	configureLogging(dataDir)

	if len(os.Args) > 1 {
		switch strings.ToLower(os.Args[1]) {
		case "--run-foreground":
			if !acquireSingleInstance() {
				log.Printf("DevicePulse Agent is already running")
				return true
			}
			return false
		case "--install-service":
			if err := installAndStartWindowsService(); err != nil {
				log.Printf("Windows service install failed: %v", err)
				os.Exit(1)
			}
			return true
		case "--uninstall-service":
			if err := uninstallWindowsService(); err != nil {
				log.Printf("Windows service uninstall failed: %v", err)
				os.Exit(1)
			}
			return true
		}
	}

	isService, err := svc.IsWindowsService()
	if err == nil && isService {
		if err := svc.Run(windowsServiceName, &devicePulseService{}); err != nil {
			log.Printf("Windows service stopped with error: %v", err)
		}
		return true
	}

	if err := installAndStartWindowsService(); err == nil {
		log.Printf("Installed and started %s Windows service", windowsServiceName)
		return true
	} else {
		log.Printf("Windows service install/start failed, using per-user startup fallback: %v", err)
	}

	if err := ensureUserStartupAndLaunchDetached(); err != nil {
		log.Printf("Per-user startup fallback failed: %v", err)
		return false
	}
	return true
}

func acquireSingleInstance() bool {
	name, err := windows.UTF16PtrFromString(`Global\DevicePulseAgent`)
	if err != nil {
		log.Printf("Create mutex name failed: %v", err)
		return true
	}
	handle, err := windows.CreateMutex(nil, false, name)
	if err != nil {
		log.Printf("Create mutex failed: %v", err)
		return true
	}
	if windows.GetLastError() == windows.ERROR_ALREADY_EXISTS {
		windows.CloseHandle(handle)
		return false
	}
	singleInstanceMutex = handle
	return true
}

type devicePulseService struct{}

func (s *devicePulseService) Execute(args []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	go runAgent()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

	for req := range requests {
		switch req.Cmd {
		case svc.Interrogate:
			status <- req.CurrentStatus
		case svc.Stop, svc.Shutdown:
			status <- svc.Status{State: svc.StopPending}
			return false, 0
		}
	}
	return false, 0
}

func installAndStartWindowsService() error {
	installPath, err := ensureServiceBinaryInstalled()
	if err != nil {
		return err
	}

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect service manager: %w", err)
	}
	defer m.Disconnect()

	service, err := m.OpenService(windowsServiceName)
	if err == nil {
		defer service.Close()
		binaryChanged := updateWindowsServiceConfig(service, installPath)
		if err := configureWindowsService(service); err != nil {
			log.Printf("Configure existing service failed: %v", err)
		}
		if binaryChanged {
			restartWindowsService(service)
		}
		if err := service.Start(); err != nil && !isWindowsServiceAlreadyRunning(err) {
			return fmt.Errorf("start existing service: %w", err)
		}
		return nil
	}

	service, err = m.CreateService(windowsServiceName, quoteWindowsPath(installPath), mgr.Config{
		DisplayName:      "DevicePulse Agent",
		Description:      "Streams endpoint telemetry to the DevicePulse API.",
		StartType:        mgr.StartAutomatic,
		ErrorControl:     mgr.ErrorNormal,
		ServiceStartName: "LocalSystem",
		DelayedAutoStart: true,
	})
	if err != nil {
		return fmt.Errorf("create service: %w", err)
	}
	defer service.Close()

	if err := configureWindowsService(service); err != nil {
		log.Printf("Configure service recovery failed: %v", err)
	}
	if err := service.Start(); err != nil && !isWindowsServiceAlreadyRunning(err) {
		return fmt.Errorf("start service: %w", err)
	}
	return nil
}

func ensureServiceBinaryInstalled() (string, error) {
	currentPath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}
	currentPath, err = filepath.Abs(currentPath)
	if err != nil {
		return "", fmt.Errorf("resolve absolute executable path: %w", err)
	}

	installPath := filepath.Join(resolveProgramFilesDir(), "DevicePulse", "Agent", "devicepulse-agent.exe")
	if strings.EqualFold(currentPath, installPath) {
		return installPath, nil
	}

	if err := os.MkdirAll(filepath.Dir(installPath), 0755); err != nil {
		return "", fmt.Errorf("create install directory: %w", err)
	}
	if err := copyFile(currentPath, installPath); err != nil {
		return "", fmt.Errorf("copy agent to install directory: %w", err)
	}
	return installPath, nil
}

func resolveProgramFilesDir() string {
	if dir := os.Getenv("ProgramFiles"); dir != "" {
		return dir
	}
	return `C:\Program Files`
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

func updateWindowsServiceConfig(service *mgr.Service, installPath string) bool {
	config, err := service.Config()
	if err != nil {
		log.Printf("Read service config failed: %v", err)
		return false
	}

	wantedPath := quoteWindowsPath(installPath)
	if sameWindowsCommandPath(config.BinaryPathName, wantedPath) &&
		config.StartType == mgr.StartAutomatic &&
		config.DelayedAutoStart {
		return false
	}

	config.BinaryPathName = wantedPath
	config.DisplayName = "DevicePulse Agent"
	config.Description = "Streams endpoint telemetry to the DevicePulse API."
	config.StartType = mgr.StartAutomatic
	config.ErrorControl = mgr.ErrorNormal
	config.ServiceStartName = "LocalSystem"
	config.DelayedAutoStart = true
	if err := service.UpdateConfig(config); err != nil {
		log.Printf("Update service config failed: %v", err)
		return false
	}
	return true
}

func restartWindowsService(service *mgr.Service) {
	status, err := service.Query()
	if err != nil || status.State != svc.Running {
		return
	}

	if _, err := service.Control(svc.Stop); err != nil {
		log.Printf("Stop service for binary-path update failed: %v", err)
		return
	}
	for i := 0; i < 20; i++ {
		time.Sleep(500 * time.Millisecond)
		status, err = service.Query()
		if err != nil || status.State == svc.Stopped {
			return
		}
	}
	log.Printf("Timed out waiting for service stop after binary-path update")
}

func quoteWindowsPath(path string) string {
	return `"` + path + `"`
}

func sameWindowsCommandPath(a, b string) bool {
	return strings.EqualFold(strings.Trim(a, `"`), strings.Trim(b, `"`))
}

func configureWindowsService(service *mgr.Service) error {
	actions := []mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 60 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 5 * time.Minute},
	}
	if err := service.SetRecoveryActions(actions, 86400); err != nil {
		return err
	}
	return service.SetRecoveryActionsOnNonCrashFailures(true)
}

func uninstallWindowsService() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect service manager: %w", err)
	}
	defer m.Disconnect()

	service, err := m.OpenService(windowsServiceName)
	if err != nil {
		return fmt.Errorf("open service: %w", err)
	}
	defer service.Close()
	return service.Delete()
}

func ensureUserStartupAndLaunchDetached() error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}
	exePath, err = filepath.Abs(exePath)
	if err != nil {
		return fmt.Errorf("resolve absolute executable path: %w", err)
	}

	key, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open Run registry key: %w", err)
	}
	defer key.Close()

	cmd := fmt.Sprintf(`"%s" --run-foreground`, exePath)
	if err := key.SetStringValue("DevicePulseAgent", cmd); err != nil {
		return fmt.Errorf("write Run registry key: %w", err)
	}

	proc, err := os.StartProcess(exePath, []string{exePath, "--run-foreground"}, &os.ProcAttr{
		Files: []*os.File{nil, nil, nil},
		Sys: &windows.SysProcAttr{
			CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP,
			HideWindow:    true,
		},
	})
	if err != nil {
		return fmt.Errorf("launch detached agent: %w", err)
	}
	return proc.Release()
}

func isWindowsServiceAlreadyRunning(err error) bool {
	return err == windows.ERROR_SERVICE_ALREADY_RUNNING
}
