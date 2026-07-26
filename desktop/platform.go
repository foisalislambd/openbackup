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

	"github.com/openbackup/openbackup/internal/agent/config"
)

// agentCommand locates the openbackup command line tool.
//
// The app deliberately drives the real tool for anything that touches the
// operating system's service manager, instead of carrying its own copy of that
// logic. One implementation of "install the service" means one set of bugs, and
// the terminal and the window stay in agreement.
func agentCommand() (string, error) {
	name := "openbackup"
	if runtime.GOOS == "windows" {
		name = "openbackup.exe"
	}
	// Next to this executable first: an installed app ships both binaries in the
	// same directory, and that copy is guaranteed to match this app's version.
	if self, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(self), name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}
	return "", fmt.Errorf("could not find the %s command; reinstall OpenBackup to repair it", name)
}

// runAgent runs an agent subcommand and returns its output on failure, because
// the tool's own message is more useful than "exit status 1".
func runAgent(args ...string) error {
	bin, err := agentCommand()
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, args...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	message := strings.TrimSpace(string(out))
	if message == "" {
		return err
	}
	return errors.New(message)
}

// installService makes sure backups run in the background, then starts them.
//
// Installing a service needs administrator rights on Windows; when that is
// refused the error says so, since a user who dismissed the prompt needs to know
// that nothing is being backed up.
func installService() error {
	if err := runAgent("service", "install"); err != nil {
		// Already installed is the common case on a second run; starting it is
		// still worth attempting.
		if !strings.Contains(strings.ToLower(err.Error()), "already") {
			return err
		}
	}
	return runAgent("service", "start")
}

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
//
// A second window would show the same agent's state twice and let the user issue
// contradictory commands, so the second copy raises the first one instead.
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

	// A lock left behind by a crash must never stop someone opening their own app,
	// so the recorded process is checked rather than trusted. Only a lock held by a
	// process that is genuinely still running counts.
	if !holderIsAlive(path) {
		_ = os.Remove(path)
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			// Another copy won the race in the meantime, which is the answer.
			return func() {}, false
		}
		fmt.Fprintf(file, "%d\n%s\n", os.Getpid(), time.Now().Format(time.RFC3339))
		_ = file.Close()
		return func() { _ = os.Remove(path) }, true
	}
	return func() {}, false
}

// holderIsAlive reports whether the process named in a lock file still exists.
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
