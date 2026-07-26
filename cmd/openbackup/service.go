package main

import (
	"context"
	"errors"
	"flag"
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

// serviceName must be stable: it is the identity of the installed Windows
// service, systemd unit or launchd job.
const serviceName = "openbackup"

// program implements service.Interface: the same binary runs as a CLI and as a
// managed background service, so there is one thing to install and update.
type program struct {
	engine  *engine.Engine
	control *ipc.Server
	cancel  context.CancelFunc
	done    chan struct{}
}

// Start is called by the service manager. It must not block.
func (p *program) Start(_ service.Service) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	if !cfg.Enrolled() {
		return errors.New("the service cannot start because this device is not connected to a server; " +
			"run 'openbackup connect' first")
	}
	stateDir, err := config.StateDir()
	if err != nil {
		return err
	}
	log := logx.New(logx.Options{
		Level: cfg.LogLevel,
		File:  filepath.Join(stateDir, "agent.log"),
		// A service has no terminal, so structured JSON in a rotating file is the
		// only record worth keeping.
		JSON: true,
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

// Stop shuts the agent down cleanly so an in-flight snapshot is not left half
// finished without the server knowing.
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

// newService builds the service definition for this platform.
func newService() (service.Service, *program, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, nil, err
	}
	arguments := []string{"service", "run"}
	if configPath != "" {
		arguments = append(arguments, "--config", configPath)
	}

	cfg := &service.Config{
		Name:        serviceName,
		DisplayName: "OpenBackup",
		Description: "Backs up your documents, pictures and other files automatically.",
		Executable:  exe,
		Arguments:   arguments,
		Option:      service.KeyValue{},
	}

	switch runtime.GOOS {
	case "windows":
		// Delayed start keeps the agent out of the way of login, and automatic
		// restart means a crash does not silently end backups.
		cfg.Option["DelayedAutoStart"] = true
		cfg.Option["OnFailure"] = "restart"
		cfg.Option["OnFailureDelayDuration"] = "30s"
		cfg.Option["OnFailureResetPeriod"] = 10
	case "linux":
		// A user unit keeps the agent running as the person whose files it backs
		// up, with no root privileges anywhere.
		cfg.Option["UserService"] = os.Geteuid() != 0
		cfg.Option["Restart"] = "always"
		cfg.Option["SuccessExitStatus"] = "0 2 SIGKILL"
		// Lowest scheduling priority: the backup should never be the reason a
		// machine feels slow.
		cfg.Option["Nice"] = 15
		cfg.Option["IOSchedulingClass"] = "idle"
	case "darwin":
		cfg.Option["UserService"] = true
		cfg.Option["KeepAlive"] = true
		cfg.Option["RunAtLoad"] = true
		cfg.Option["LowPriorityIO"] = true
	}

	prg := &program{}
	svc, err := service.New(prg, cfg)
	if err != nil {
		return nil, nil, err
	}
	return svc, prg, nil
}

func cmdService(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: openbackup service <install|uninstall|start|stop|restart|status|run>")
	}
	action := args[0]

	fs := flag.NewFlagSet("service", flag.ContinueOnError)
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	svc, _, err := newService()
	if err != nil {
		return err
	}

	switch action {
	case "run":
		// Invoked by the service manager, not by hand.
		return svc.Run()

	case "install":
		if _, err := requireEnrolled(); err != nil {
			return err
		}
		if err := svc.Install(); err != nil {
			return fmt.Errorf("%w%s", err, privilegeHint(err))
		}
		if err := svc.Start(); err != nil {
			fmt.Println("Installed, but it could not be started automatically:", err)
			fmt.Println("Start it with: openbackup service start")
			return nil
		}
		fmt.Println("OpenBackup now runs in the background and starts with your computer.")
		fmt.Println("Check on it any time with 'openbackup status'.")
		return nil

	case "uninstall", "remove":
		// Stopping first avoids leaving a running process with no service record.
		_ = svc.Stop()
		if err := svc.Uninstall(); err != nil {
			return fmt.Errorf("%w%s", err, privilegeHint(err))
		}
		fmt.Println("The background service has been removed. Your backups on the server are untouched.")
		return nil

	case "start":
		if err := svc.Start(); err != nil {
			return fmt.Errorf("%w%s", err, privilegeHint(err))
		}
		fmt.Println("Started.")
		return nil

	case "stop":
		if err := svc.Stop(); err != nil {
			return fmt.Errorf("%w%s", err, privilegeHint(err))
		}
		fmt.Println("Stopped. Backups will not run until you start it again.")
		return nil

	case "restart":
		if err := svc.Restart(); err != nil {
			return fmt.Errorf("%w%s", err, privilegeHint(err))
		}
		fmt.Println("Restarted.")
		return nil

	case "status":
		status, err := svc.Status()
		if err != nil {
			if errors.Is(err, service.ErrNotInstalled) {
				fmt.Println("The background service is not installed. Install it with 'openbackup service install'.")
				return nil
			}
			return err
		}
		switch status {
		case service.StatusRunning:
			fmt.Println("The background service is running.")
		case service.StatusStopped:
			fmt.Println("The background service is installed but stopped.")
		default:
			fmt.Println("The background service state is unknown.")
		}
		return nil

	default:
		return fmt.Errorf("unknown service action %q", action)
	}
}

// privilegeHint explains the most common failure: a service operation that needs
// elevation.
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
		return "\n\nThis needs an elevated prompt: right-click Windows Terminal or Command Prompt " +
			"and choose \"Run as administrator\", then run the command again."
	case "darwin":
		return "\n\nTry again with sudo."
	default:
		return "\n\nTry again with sudo, or install it as a user service by running this command as your own user."
	}
}
