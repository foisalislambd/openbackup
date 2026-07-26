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
	"time"

	"github.com/gen2brain/beeep"

	"github.com/foisalislambd/openbackup/internal/agent/config"
)

// reveal opens a folder in the system file manager.
func reveal(path string) error {
	if path == "" {
		return errors.New("there is no folder to open")
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
// keeps the lock for the life of the process.
func singleInstance() (release func(), ok bool) {
	stateDir, err := config.StateDir()
	if err != nil {
		return func() {}, true
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return func() {}, true
	}
	path := filepath.Join(stateDir, "desktop.lock")

	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err == nil {
		fmt.Fprintf(file, "%d\n%s\n", os.Getpid(), time.Now().Format(time.RFC3339))
		_ = file.Close()
		return func() { _ = os.Remove(path) }, true
	}

	if !holderIsAlive(path) {
		_ = os.Remove(path)
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return func() {}, false
		}
		fmt.Fprintf(file, "%d\n%s\n", os.Getpid(), time.Now().Format(time.RFC3339))
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
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return false
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil || pid <= 0 || pid == os.Getpid() {
		return false
	}
	return processExists(pid)
}
