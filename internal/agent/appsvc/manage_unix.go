//go:build !windows

package appsvc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/kardianos/service"

	"github.com/foisalislambd/openbackup/internal/agent/config"
	"github.com/foisalislambd/openbackup/internal/agent/engine"
	"github.com/foisalislambd/openbackup/internal/agent/ipc"
	"github.com/foisalislambd/openbackup/internal/logx"
)

// program implements service.Interface for systemd / launchd.
type program struct {
	configPath string
	engine     *engine.Engine
	control    *ipc.Server
	cancel     context.CancelFunc
	done       chan struct{}
}

func (p *program) Start(_ service.Service) error {
	cfg, err := config.Load(p.configPath)
	if err != nil {
		return err
	}
	if !cfg.Enrolled() {
		return errors.New("the service cannot start because this device is not connected to a server; " +
			"connect from the OpenBackup app first")
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

	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.done = make(chan struct{})

	eng, err := engine.New(ctx, engine.Options{Config: cfg, Logger: log, StateDir: stateDir})
	if err != nil {
		cancel()
		return err
	}
	p.engine = eng

	if control, err := ipc.Listen(stateDir, eng.Control()); err == nil {
		p.control = control
	} else {
		log.Warn("could not start the local control channel", "error", err)
	}

	go func() {
		defer close(p.done)
		if err := eng.Run(ctx); err != nil {
			log.Error("agent stopped", "error", err)
		}
	}()
	return nil
}

func (p *program) Stop(_ service.Service) error {
	if p.cancel != nil {
		p.cancel()
	}
	if p.done != nil {
		<-p.done
	}
	if p.control != nil {
		_ = p.control.Close()
	}
	if p.engine != nil {
		p.engine.Close()
	}
	return nil
}

func newUnixService(opt Options) (service.Service, error) {
	exe, err := resolveExe(opt)
	if err != nil {
		return nil, err
	}
	arguments := opt.Arguments
	if len(arguments) == 0 {
		arguments = []string{"service", "run"}
	}
	if opt.ConfigPath != "" {
		arguments = append(append([]string{}, arguments...), "--config", opt.ConfigPath)
	}

	cfg := &service.Config{
		Name:        Name,
		DisplayName: "OpenBackup",
		Description: "Backs up your documents, pictures and other files automatically.",
		Executable:  exe,
		Arguments:   arguments,
		Option:      service.KeyValue{},
	}

	switch runtime.GOOS {
	case "linux":
		cfg.Option["UserService"] = os.Geteuid() != 0
		cfg.Option["Restart"] = "always"
		cfg.Option["SuccessExitStatus"] = "0 2 SIGKILL"
		cfg.Option["Nice"] = 15
		cfg.Option["IOSchedulingClass"] = "idle"
	case "darwin":
		cfg.Option["UserService"] = true
		cfg.Option["KeepAlive"] = true
		cfg.Option["RunAtLoad"] = true
		cfg.Option["LowPriorityIO"] = true
	}

	return service.New(&program{configPath: opt.ConfigPath}, cfg)
}

// Run is invoked by the service manager (Arguments: service run).
func Run(opt Options) error {
	svc, err := newUnixService(opt)
	if err != nil {
		return err
	}
	return svc.Run()
}

// Install registers and starts the user service.
func Install(opt Options) error {
	if err := requireEnrolled(opt.ConfigPath); err != nil {
		return err
	}
	svc, err := newUnixService(opt)
	if err != nil {
		return err
	}
	if err := svc.Install(); err != nil {
		return fmt.Errorf("%w%s", err, privilegeHint(err))
	}
	if err := svc.Start(); err != nil {
		return fmt.Errorf("%w: %v\nStart it from the OpenBackup app, or run: openbackup service start",
			ErrInstalledNotStarted, err)
	}
	return nil
}

// Start starts an already-installed service.
func Start(opt Options) error {
	svc, err := newUnixService(opt)
	if err != nil {
		return err
	}
	if err := svc.Start(); err != nil {
		return fmt.Errorf("%w%s", err, privilegeHint(err))
	}
	return WaitUntilRunning(20 * time.Second)
}

// Stop stops the service.
func Stop(opt Options) error {
	svc, err := newUnixService(opt)
	if err != nil {
		return err
	}
	if err := svc.Stop(); err != nil {
		return fmt.Errorf("%w%s", err, privilegeHint(err))
	}
	return nil
}

// Restart restarts the service.
func Restart(opt Options) error {
	svc, err := newUnixService(opt)
	if err != nil {
		return err
	}
	if err := svc.Restart(); err != nil {
		return fmt.Errorf("%w%s", err, privilegeHint(err))
	}
	return WaitUntilRunning(20 * time.Second)
}

// Uninstall removes the service registration.
func Uninstall(opt Options) error {
	svc, err := newUnixService(opt)
	if err != nil {
		return err
	}
	_ = svc.Stop()
	if err := svc.Uninstall(); err != nil {
		return fmt.Errorf("%w%s", err, privilegeHint(err))
	}
	return nil
}

// StatusText returns a short human description of the service state.
func StatusText(opt Options) (string, error) {
	svc, err := newUnixService(opt)
	if err != nil {
		return "", err
	}
	status, err := svc.Status()
	if err != nil {
		if errors.Is(err, service.ErrNotInstalled) {
			return "The background service is not installed.", nil
		}
		return "", err
	}
	switch status {
	case service.StatusRunning:
		return "The background service is running.", nil
	case service.StatusStopped:
		return "The background service is installed but stopped.", nil
	default:
		return "The background service state is unknown.", nil
	}
}

// InstallOrStart installs if needed, otherwise starts.
func InstallOrStart(opt Options) error {
	err := Install(opt)
	if err == nil {
		return WaitUntilRunning(20 * time.Second)
	}
	if errors.Is(err, ErrInstalledNotStarted) {
		if err := Start(opt); err != nil {
			return err
		}
		return nil
	}
	if strings.Contains(strings.ToLower(err.Error()), "already") {
		_ = Uninstall(opt)
		if err := Install(opt); err != nil {
			return err
		}
		return WaitUntilRunning(20 * time.Second)
	}
	return err
}

func privilegeHint(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "denied") && !strings.Contains(msg, "permission") &&
		!strings.Contains(msg, "administrator") {
		return ""
	}
	if runtime.GOOS == "darwin" {
		return "\n\nTry again with sudo."
	}
	return "\n\nTry again with sudo, or install it as a user service by running as your own user."
}
