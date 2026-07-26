package main

import (
	"errors"
	"flag"
	"fmt"

	"github.com/foisalislambd/openbackup/internal/agent/appsvc"
)

func cmdService(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: openbackup service <install|uninstall|start|stop|restart|status|run>")
	}
	action := args[0]

	fs := flag.NewFlagSet("service", flag.ContinueOnError)
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	opt := appsvc.Options{ConfigPath: configPath}

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
		fmt.Println("Check on it any time with 'openbackup status'.")
		return nil

	case "uninstall", "remove":
		if err := appsvc.Uninstall(opt); err != nil {
			return err
		}
		fmt.Println("The background service has been removed. Your backups on the server are untouched.")
		return nil

	case "start":
		if err := appsvc.Start(opt); err != nil {
			return err
		}
		fmt.Println("Started.")
		return nil

	case "stop":
		if err := appsvc.Stop(opt); err != nil {
			return err
		}
		fmt.Println("Stopped. Backups will not run until you start it again.")
		return nil

	case "restart":
		if err := appsvc.Restart(opt); err != nil {
			return err
		}
		fmt.Println("Restarted.")
		return nil

	case "status":
		text, err := appsvc.StatusText(opt)
		if err != nil {
			return err
		}
		fmt.Println(text)
		if text == "The background service is not installed." {
			fmt.Println("Install it with 'openbackup service install'.")
		}
		return nil

	default:
		return fmt.Errorf("unknown service action %q", action)
	}
}
