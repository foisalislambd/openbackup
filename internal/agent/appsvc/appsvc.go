// Package appsvc installs and runs the OpenBackup agent in the background.
//
// On Linux and macOS this registers a user service (systemd/launchd). On Windows
// it uses a per-user login autostart — not a Windows Service — because the
// desktop app is a GUI binary and Service Control Manager both requires admin
// elevation and does not run GUI apps reliably. The desktop "Start it" button
// must work without "Run as administrator".
package appsvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/foisalislambd/openbackup/internal/agent/config"
	"github.com/foisalislambd/openbackup/internal/agent/engine"
	"github.com/foisalislambd/openbackup/internal/agent/ipc"
	"github.com/foisalislambd/openbackup/internal/logx"
)

// Name is the stable identity used in service managers and the Windows Run key.
const Name = "openbackup"

// ErrInstalledNotStarted means registration succeeded but the agent did not come up.
var ErrInstalledNotStarted = errors.New("installed, but could not start automatically")

// Options configures how the background agent is registered and started.
type Options struct {
	// ConfigPath overrides the default config file (empty = platform default).
	ConfigPath string
	// Executable is the binary to launch. Empty means this process.
	Executable string
	// Arguments are passed to Executable; platform defaults apply when empty.
	Arguments []string
}

func resolveExe(opt Options) (string, error) {
	if opt.Executable != "" {
		return opt.Executable, nil
	}
	return os.Executable()
}

func requireEnrolled(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	if !cfg.Enrolled() {
		return errors.New("connect this device to a server before starting the background agent")
	}
	return nil
}

// AgentRunning reports whether the local control channel answers.
func AgentRunning() bool {
	stateDir, err := config.StateDir()
	if err != nil {
		return false
	}
	client, err := ipc.Dial(stateDir)
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var st engine.Status
	return client.Status(ctx, &st) == nil
}

// WaitUntilRunning polls until the agent answers or the timeout elapses.
func WaitUntilRunning(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if AgentRunning() {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return errors.New("the background agent did not start in time — check the log in the OpenBackup data folder")
}

// runForeground runs the agent in this process until cancelled (used by
// `service run` / `run` when the OS or a parent has launched us headless).
func runForeground(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	if !cfg.Enrolled() {
		return errors.New("connect this device to a server before running the agent")
	}
	stateDir, err := config.StateDir()
	if err != nil {
		return err
	}
	log := logx.New(logx.Options{
		Level: cfg.LogLevel,
		File:  filepath.Join(stateDir, "agent.log"),
		JSON:  true,
	})
	ctx := context.Background()
	eng, err := engine.New(ctx, engine.Options{Config: cfg, Logger: log, StateDir: stateDir})
	if err != nil {
		return err
	}
	defer eng.Close()

	if control, err := ipc.Listen(stateDir, eng.Control()); err == nil {
		defer control.Close()
	} else {
		log.Warn("could not start the local control channel", "error", err)
	}
	return eng.Run(ctx)
}

func readAgentPID() (int, error) {
	stateDir, err := config.StateDir()
	if err != nil {
		return 0, err
	}
	raw, err := os.ReadFile(filepath.Join(stateDir, "control.json"))
	if err != nil {
		return 0, err
	}
	var c struct {
		PID int `json:"pid"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return 0, err
	}
	if c.PID <= 0 {
		return 0, fmt.Errorf("no pid")
	}
	return c.PID, nil
}
