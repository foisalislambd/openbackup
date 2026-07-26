// Package appsvc installs and runs the OpenBackup agent as an OS background
// service (Windows service, systemd user unit, or launchd).
//
// The same code is used by the CLI and the desktop app so either binary can be
// the service executable. The desktop window must not require a second download.
package appsvc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/kardianos/service"

	"github.com/foisalislambd/openbackup/internal/agent/config"
	"github.com/foisalislambd/openbackup/internal/agent/engine"
	"github.com/foisalislambd/openbackup/internal/agent/ipc"
	"github.com/foisalislambd/openbackup/internal/logx"
)

// Name is the stable OS-level service identity.
const Name = "openbackup"

// ErrInstalledNotStarted means the service was registered but Start failed.
// Callers may retry Start without treating install as a total failure.
var ErrInstalledNotStarted = errors.New("installed, but could not start automatically")

// Options configures how the service is registered and started.
type Options struct {
	// ConfigPath overrides the default config file (empty = platform default).
	ConfigPath string
	// Executable is the binary the OS should launch. Empty means this process.
	Executable string
	// Arguments are passed to Executable; default is {"service", "run"}.
	Arguments []string
}

// program implements service.Interface.
type program struct {
	configPath string
	engine     *engine.Engine
	control    *ipc.Server
	cancel     context.CancelFunc
	done       chan struct{}
}

// Start is called by the service manager. It must not block.
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

// Stop shuts the agent down cleanly.
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

// New builds the platform service wrapper.
func New(opt Options) (service.Service, error) {
	exe := opt.Executable
	if exe == "" {
		var err error
		exe, err = os.Executable()
		if err != nil {
			return nil, err
		}
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
	case "windows":
		cfg.Option["DelayedAutoStart"] = true
		cfg.Option["OnFailure"] = "restart"
		cfg.Option["OnFailureDelayDuration"] = "30s"
		cfg.Option["OnFailureResetPeriod"] = 10
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

// Run is invoked by the OS service manager (Arguments: service run).
func Run(opt Options) error {
	svc, err := New(opt)
	if err != nil {
		return err
	}
	return svc.Run()
}

// Install registers and starts the service. The device must already be enrolled.
func Install(opt Options) error {
	cfg, err := config.Load(opt.ConfigPath)
	if err != nil {
		return err
	}
	if !cfg.Enrolled() {
		return errors.New("connect this device to a server before starting the background service")
	}
	svc, err := New(opt)
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
	svc, err := New(opt)
	if err != nil {
		return err
	}
	if err := svc.Start(); err != nil {
		return fmt.Errorf("%w%s", err, privilegeHint(err))
	}
	return nil
}

// Stop stops the service.
func Stop(opt Options) error {
	svc, err := New(opt)
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
	svc, err := New(opt)
	if err != nil {
		return err
	}
	if err := svc.Restart(); err != nil {
		return fmt.Errorf("%w%s", err, privilegeHint(err))
	}
	return nil
}

// Uninstall removes the service registration.
func Uninstall(opt Options) error {
	svc, err := New(opt)
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
	svc, err := New(opt)
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

// InstallOrStart installs if needed, otherwise starts. Used by the desktop app.
//
// If a service is already registered (for example from an older CLI install), it
// is removed and re-registered so Executable points at this binary — otherwise
// "Start service" would keep running a missing or outdated openbackup.exe.
func InstallOrStart(opt Options) error {
	err := Install(opt)
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrInstalledNotStarted) {
		return Start(opt)
	}
	if strings.Contains(strings.ToLower(err.Error()), "already") {
		_ = Uninstall(opt)
		return Install(opt)
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
	switch runtime.GOOS {
	case "windows":
		return "\n\nThis needs administrator rights: right-click the OpenBackup app or Terminal " +
			"and choose \"Run as administrator\", then try again."
	case "darwin":
		return "\n\nTry again with sudo."
	default:
		return "\n\nTry again with sudo, or install it as a user service by running as your own user."
	}
}
