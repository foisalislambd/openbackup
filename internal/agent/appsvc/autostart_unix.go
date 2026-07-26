//go:build !windows

package appsvc

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	xdgDesktopName   = "openbackup-autostart.desktop"
	launchAgentLabel = "app.openbackup.login"
)

func writeXDGAutostart(opt Options) error {
	exe, args, err := loginCommand(opt)
	if err != nil {
		return err
	}
	dir, err := xdgAutostartDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	execLine := quoteDesktopExec(exe, args...)
	body := fmt.Sprintf(`[Desktop Entry]
Type=Application
Version=1.0
Name=OpenBackup
Comment=Start OpenBackup backups in the tray
Exec=%s
X-GNOME-Autostart-enabled=true
Hidden=false
NoDisplay=false
Terminal=false
`, execLine)
	return os.WriteFile(filepath.Join(dir, xdgDesktopName), []byte(body), 0o644)
}

func removeXDGAutostart() error {
	dir, err := xdgAutostartDir()
	if err != nil {
		return nil
	}
	err = os.Remove(filepath.Join(dir, xdgDesktopName))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func xdgAutostartDir() (string, error) {
	if config := os.Getenv("XDG_CONFIG_HOME"); config != "" {
		return filepath.Join(config, "autostart"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "autostart"), nil
}

func writeLaunchAgent(opt Options) error {
	exe, args, err := loginCommand(opt)
	if err != nil {
		return err
	}
	dir, err := launchAgentDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var progArgs strings.Builder
	progArgs.WriteString(fmt.Sprintf("\t\t<string>%s</string>\n", xmlEscape(exe)))
	for _, a := range args {
		progArgs.WriteString(fmt.Sprintf("\t\t<string>%s</string>\n", xmlEscape(a)))
	}
	body := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
%s	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<false/>
</dict>
</plist>
`, launchAgentLabel, progArgs.String())
	path := filepath.Join(dir, launchAgentLabel+".plist")
	return os.WriteFile(path, []byte(body), 0o644)
}

func removeLaunchAgent() error {
	dir, err := launchAgentDir()
	if err != nil {
		return nil
	}
	err = os.Remove(filepath.Join(dir, launchAgentLabel+".plist"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func launchAgentDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents"), nil
}

func loginCommand(opt Options) (exe string, args []string, err error) {
	exe, err = resolveExe(opt)
	if err != nil {
		return "", nil, err
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return "", nil, err
	}
	args = append([]string{}, opt.Arguments...)
	if len(args) == 0 {
		args = []string{"--background"}
	}
	if opt.ConfigPath != "" {
		args = append(args, "--config", opt.ConfigPath)
	}
	return exe, args, nil
}

func quoteDesktopExec(exe string, args ...string) string {
	parts := make([]string, 0, 1+len(args))
	parts = append(parts, shellQuote(exe))
	for _, a := range args {
		parts = append(parts, shellQuote(a))
	}
	return strings.Join(parts, " ")
}

func shellQuote(s string) string {
	if s == "" {
		return `""`
	}
	if !strings.ContainsAny(s, " \t\"'\\$`") {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

func xmlEscape(s string) string {
	r := strings.NewReplacer(
		`&`, `&amp;`,
		`<`, `&lt;`,
		`>`, `&gt;`,
		`"`, `&quot;`,
		`'`, `&apos;`,
	)
	return r.Replace(s)
}
