//go:build windows

package appsvc

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`
const runValueName = "OpenBackup"

// Run runs the agent in this process. Used when launched as `service run` or by
// older registrations; new installs use `run` via the login autostart entry.
func Run(opt Options) error {
	return runForeground(opt.ConfigPath)
}

// Install registers a per-user login autostart and starts the agent now.
// No administrator rights are required.
func Install(opt Options) error {
	if err := requireEnrolled(opt.ConfigPath); err != nil {
		return err
	}
	if err := enableAutostart(opt); err != nil {
		return err
	}
	if AgentRunning() {
		return nil
	}
	if err := startDetached(opt); err != nil {
		return fmt.Errorf("%w: %v", ErrInstalledNotStarted, err)
	}
	if err := WaitUntilRunning(25 * time.Second); err != nil {
		return fmt.Errorf("%w: %v", ErrInstalledNotStarted, err)
	}
	return nil
}

// Start launches the agent if it is not already running.
func Start(opt Options) error {
	if err := requireEnrolled(opt.ConfigPath); err != nil {
		return err
	}
	_ = enableAutostart(opt) // best-effort: keep login start in sync with this exe
	if AgentRunning() {
		return nil
	}
	if err := startDetached(opt); err != nil {
		return err
	}
	return WaitUntilRunning(25 * time.Second)
}

// Stop ends the running agent process for this user.
func Stop(opt Options) error {
	_ = opt
	if !AgentRunning() {
		return nil
	}
	if AgentIsSelf() {
		return errors.New("refusing to stop the current process — use the in-process shutdown instead")
	}
	pid, err := readAgentPID()
	if err != nil {
		return fmt.Errorf("could not find the running agent: %w", err)
	}
	if err := terminatePID(pid); err != nil {
		return err
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !AgentRunning() {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("the agent did not stop in time")
}

// Restart stops then starts the agent.
func Restart(opt Options) error {
	_ = Stop(opt)
	return Start(opt)
}

// Uninstall removes login autostart and stops the agent.
func Uninstall(opt Options) error {
	_ = Stop(opt)
	return disableAutostart()
}

// StatusText describes whether the agent is running / set to start at login.
func StatusText(opt Options) (string, error) {
	_ = opt
	auto, _ := autostartEnabled()
	switch {
	case AgentRunning():
		return "The background agent is running.", nil
	case auto:
		return "The background agent is set to start at login but is not running now.", nil
	default:
		return "The background agent is not installed.", nil
	}
}

// InstallOrStart ensures autostart is registered and the agent is running.
func InstallOrStart(opt Options) error {
	return Install(opt)
}

// EnableLoginAutostart registers this executable to start at user login.
// Default arguments are --background (open the app hidden with the in-process agent).
func EnableLoginAutostart(opt Options) error {
	if len(opt.Arguments) == 0 {
		opt.Arguments = []string{"--background"}
	}
	return enableAutostart(opt)
}

// DisableLoginAutostart removes the per-user login Run key entry.
func DisableLoginAutostart() error {
	return disableAutostart()
}

func enableAutostart(opt Options) error {
	exe, err := resolveExe(opt)
	if err != nil {
		return err
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return err
	}
	args := opt.Arguments
	if len(args) == 0 {
		// `run` starts the agent without going through Windows Service APIs.
		args = []string{"run"}
	}
	cmdLine := `"` + exe + `"`
	for _, a := range args {
		cmdLine += " " + quoteArg(a)
	}
	if opt.ConfigPath != "" {
		cmdLine += " --config " + quoteArg(opt.ConfigPath)
	}

	key, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return fmt.Errorf("could not register login start: %w", err)
	}
	defer key.Close()
	return key.SetStringValue(runValueName, cmdLine)
}

func disableAutostart() error {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		if errorsIsNotExist(err) {
			return nil
		}
		return err
	}
	defer key.Close()
	err = key.DeleteValue(runValueName)
	if err != nil && !errorsIsNotExist(err) {
		return err
	}
	return nil
}

func autostartEnabled() (bool, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false, nil
	}
	defer key.Close()
	_, _, err = key.GetStringValue(runValueName)
	return err == nil, nil
}

func startDetached(opt Options) error {
	exe, err := resolveExe(opt)
	if err != nil {
		return err
	}
	args := opt.Arguments
	if len(args) == 0 {
		args = []string{"run"}
	}
	if opt.ConfigPath != "" {
		args = append(append([]string{}, args...), "--config", opt.ConfigPath)
	}

	cmd := exec.Command(exe, args...)
	cmd.Dir = filepath.Dir(exe)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS | windows.CREATE_NO_WINDOW,
		HideWindow:    true,
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("could not launch the background agent: %w", err)
	}
	// The child outlives this call; do not wait.
	_ = cmd.Process.Release()
	return nil
}

func terminatePID(pid int) error {
	handle, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("could not open agent process: %w", err)
	}
	defer windows.CloseHandle(handle)
	if err := windows.TerminateProcess(handle, 1); err != nil {
		return fmt.Errorf("could not stop the agent: %w", err)
	}
	return nil
}

func quoteArg(s string) string {
	if s == "" {
		return `""`
	}
	if !strings.ContainsAny(s, " \t\"") {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

func errorsIsNotExist(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, registry.ErrNotExist) || os.IsNotExist(err) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "cannot find") || strings.Contains(msg, "file not found")
}
