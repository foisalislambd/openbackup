package main

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/foisalislambd/openbackup/internal/agent/appsvc"
	"github.com/foisalislambd/openbackup/internal/agent/config"
	"github.com/foisalislambd/openbackup/internal/agent/engine"
	"github.com/foisalislambd/openbackup/internal/agent/ipc"
	"github.com/foisalislambd/openbackup/internal/logx"
)

// startEmbeddedAgent runs the backup engine inside this process (Dropbox-style).
// The window and tray share the process; closing the window does not stop backups.
func (a *App) startEmbeddedAgent() error {
	if a.agent == nil || !a.agent.Connected() {
		return nil
	}

	a.mu.Lock()
	if a.engRunning {
		a.mu.Unlock()
		return nil
	}
	if a.engStarting {
		a.mu.Unlock()
		return a.waitEmbeddedReady(10 * time.Second)
	}
	a.engStarting = true
	a.mu.Unlock()

	defer func() {
		a.mu.Lock()
		a.engStarting = false
		a.mu.Unlock()
	}()

	// Take over from any leftover detached/CLI agent so only one process backs up.
	// Never Stop when we already own the control channel (would kill this process).
	if appsvc.AgentRunning() && !appsvc.AgentIsSelf() {
		_ = appsvc.Stop(appsvc.Options{})
		deadline := time.Now().Add(8 * time.Second)
		for appsvc.AgentRunning() && !appsvc.AgentIsSelf() && time.Now().Before(deadline) {
			time.Sleep(200 * time.Millisecond)
		}
		if appsvc.AgentRunning() && !appsvc.AgentIsSelf() {
			return errors.New("another OpenBackup agent is still running — stop it and try again")
		}
	}

	cfg := a.agent.Config()
	stateDir, err := config.StateDir()
	if err != nil {
		return err
	}
	log := logx.New(logx.Options{
		Level: cfg.LogLevel,
		File:  filepath.Join(stateDir, "agent.log"),
		JSON:  true,
	})

	ctx, cancel := context.WithCancel(context.Background())
	eng, err := engine.New(ctx, engine.Options{Config: cfg, Logger: log, StateDir: stateDir})
	if err != nil {
		cancel()
		return err
	}

	control, err := ipc.Listen(stateDir, eng.Control())
	if err != nil {
		cancel()
		eng.Close()
		return err
	}

	a.mu.Lock()
	a.eng = eng
	a.engIPC = control
	a.engCancel = cancel
	a.engRunning = true
	a.mu.Unlock()

	go func() {
		defer func() {
			a.mu.Lock()
			a.engRunning = false
			a.eng = nil
			a.engIPC = nil
			a.engCancel = nil
			a.mu.Unlock()
		}()
		if err := eng.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			a.log.Error("embedded agent stopped", "error", err)
		}
		_ = control.Close()
		eng.Close()
	}()

	if err := a.waitEmbeddedReady(5 * time.Second); err != nil {
		a.stopEmbeddedAgent()
		return err
	}
	return nil
}

func (a *App) waitEmbeddedReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if appsvc.AgentRunning() {
			return nil
		}
		a.mu.Lock()
		running := a.engRunning
		starting := a.engStarting
		a.mu.Unlock()
		if !running && !starting {
			return errors.New("backups did not start")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if appsvc.AgentRunning() {
		return nil
	}
	return errors.New("backups did not start in time — check the log folder")
}

// stopEmbeddedAgent stops in-process backups. Called on Quit / Disconnect.
func (a *App) stopEmbeddedAgent() {
	a.mu.Lock()
	cancel := a.engCancel
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		a.mu.Lock()
		running := a.engRunning
		a.mu.Unlock()
		if !running {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// ensureBackingUp starts the in-process agent and registers login autostart so
// the app (with agent) comes back after reboot — no separate service binary.
func (a *App) ensureBackingUp() error {
	if a.agent == nil {
		return a.configError()
	}
	if !a.agent.Connected() {
		return errors.New("connect this device before starting backups")
	}

	// Before we own IPC: stop a leftover headless agent and drop old CLI
	// user-service units (Unix). Windows Run key is left to EnableLoginAutostart.
	if !appsvc.AgentIsSelf() {
		if appsvc.AgentRunning() {
			_ = appsvc.Stop(appsvc.Options{})
		}
		_ = appsvc.RemoveLegacyRegistration()
	}

	if err := a.startEmbeddedAgent(); err != nil {
		return err
	}
	// File/registry only — never Stop the current process.
	if err := appsvc.EnableLoginAutostart(appsvc.Options{
		Arguments: []string{"--background"},
	}); err != nil {
		a.log.Warn("login autostart", "error", err)
	}
	return nil
}
