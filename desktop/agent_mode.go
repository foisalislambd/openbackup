package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/foisalislambd/openbackup/internal/agent/appsvc"
	"github.com/foisalislambd/openbackup/internal/agent/config"
	"github.com/foisalislambd/openbackup/internal/agent/engine"
	"github.com/foisalislambd/openbackup/internal/agent/ipc"
	"github.com/foisalislambd/openbackup/internal/logx"
)

// agentMode reports whether these args should run the backup engine instead of
// opening the window (used when this binary is registered as the OS service).
func agentMode(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "service", "run", "daemon":
		return true
	default:
		return false
	}
}

// runAgentMode handles service/run without starting the GUI.
func runAgentMode(args []string) error {
	// Allow --config anywhere after the command, matching the CLI.
	rest, cfgPath := extractConfigFlag(args[1:])
	opt := appsvc.Options{ConfigPath: cfgPath}

	switch args[0] {
	case "service":
		return runDesktopService(rest, opt)
	case "run", "daemon":
		return runDesktopForeground(rest, cfgPath)
	default:
		return fmt.Errorf("unknown agent command %q", args[0])
	}
}

func runDesktopService(args []string, opt appsvc.Options) error {
	if len(args) == 0 {
		return errors.New("usage: openbackup-desktop service <install|uninstall|start|stop|restart|status|run>")
	}
	action := args[0]
	fs := flag.NewFlagSet("service", flag.ContinueOnError)
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	switch action {
	case "run":
		return appsvc.Run(opt)
	case "install":
		if err := appsvc.Install(opt); err != nil {
			if errors.Is(err, appsvc.ErrInstalledNotStarted) {
				fmt.Println(err.Error())
				return nil
			}
			return err
		}
		fmt.Println("OpenBackup now runs in the background and starts with your computer.")
		return nil
	case "uninstall", "remove":
		return appsvc.Uninstall(opt)
	case "start":
		return appsvc.Start(opt)
	case "stop":
		return appsvc.Stop(opt)
	case "restart":
		return appsvc.Restart(opt)
	case "status":
		text, err := appsvc.StatusText(opt)
		if err != nil {
			return err
		}
		fmt.Println(text)
		return nil
	default:
		return fmt.Errorf("unknown service action %q", action)
	}
}

func runDesktopForeground(args []string, cfgPath string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	once := fs.Bool("once", false, "run a single backup and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(cfgPath)
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
	})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	eng, err := engine.New(ctx, engine.Options{Config: cfg, Logger: log, StateDir: stateDir})
	if err != nil {
		return err
	}
	defer eng.Close()
	if *once {
		return eng.RunOnce(ctx)
	}

	// Same as the CLI: the window / `openbackup status` talk over this channel.
	control, err := ipc.Listen(stateDir, eng.Control())
	if err != nil {
		log.Warn("could not start the local control channel", "error", err)
	} else {
		defer control.Close()
	}
	return eng.Run(ctx)
}

// extractConfigFlag pulls --config from args the way the CLI does.
func extractConfigFlag(args []string) (rest []string, configPath string) {
	rest = make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--config" && i+1 < len(args):
			configPath = args[i+1]
			i++
		case strings.HasPrefix(a, "--config="):
			configPath = strings.TrimPrefix(a, "--config=")
		default:
			rest = append(rest, a)
		}
	}
	return rest, configPath
}
