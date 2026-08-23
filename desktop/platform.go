package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/gen2brain/beeep"

	"github.com/foisalislambd/openbackup/internal/agent/config"
)

// reveal opens a folder in the system file manager. The path is validated
// because it crosses the renderer boundary: a UNC path would make Explorer
// negotiate NTLM with a remote host, and a dash-leading argument could be read
// as a flag by the opener.
func reveal(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("there is no folder to open")
	}
	if strings.HasPrefix(path, `\\`) || strings.HasPrefix(path, "-") {
		return fmt.Errorf("cannot open %q", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("that folder is not available: %w", err)
	}
	if !info.IsDir() {
		path = filepath.Dir(path)
	}
	switch runtime.GOOS {
	case "windows":
		return exec.Command("explorer.exe", filepath.Clean(path)).Start()
	case "darwin":
		return exec.Command("open", path).Start()
	default:
		return exec.Command("xdg-open", path).Start()
	}
}

// showNotification raises a system notification.
func showNotification(title, body string) error {
	beeep.AppName = "OpenBackup"
	return beeep.Notify(title, body, iconFile())
}

// iconFile writes the tray icon to a temporary file, because the notification
// APIs on Windows and Linux want a path rather than bytes.
func iconFile() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	dir = filepath.Join(dir, "OpenBackup")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}
	path := filepath.Join(dir, "openbackup.png")
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		return path
	}
	if err := os.WriteFile(path, notificationIcon, 0o600); err != nil {
		return ""
	}
	return path
}

// logPath returns where the agent writes its log.
func logPath() string {
	stateDir, err := config.StateDir()
	if err != nil {
		return ""
	}
	return filepath.Join(stateDir, "agent.log")
}

// singleInstance reports whether this is the only copy of the app running, and
// keeps the lock for the life of the process. The lock records which executable
// holds it, so a recycled PID left behind by a crash cannot wedge every future
// launch with "another window is already open".
func singleInstance() (release func(), ok bool) {
	stateDir, err := config.StateDir()
	if err != nil {
		return func() {}, true
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return func() {}, true
	}
	path := filepath.Join(stateDir, "desktop.lock")

	exeBase := ""
	if exe, exeErr := os.Executable(); exeErr == nil {
		exeBase = filepath.Base(exe)
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err == nil {
		fmt.Fprintf(file, "%d\n%s\n", os.Getpid(), exeBase)
		_ = file.Close()
		return func() { _ = os.Remove(path) }, true
	}

	if !holderIsAlive(path) {
		_ = os.Remove(path)
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return func() {}, false
		}
		fmt.Fprintf(file, "%d\n%s\n", os.Getpid(), exeBase)
		_ = file.Close()
		return func() { _ = os.Remove(path) }, true
	}
	return func() {}, false
}

func holderIsAlive(path string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) == 0 {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil || pid <= 0 || pid == os.Getpid() {
		return false
	}
	exeBase := ""
	if len(lines) > 1 {
		exeBase = strings.TrimSpace(lines[1])
	}
	return holderMatches(pid, exeBase)
}
