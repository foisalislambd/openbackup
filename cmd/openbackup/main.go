// Command openbackup is the OpenBackup agent and its command line interface.
//
// The command set is deliberately small. Most users will only ever run
// "openbackup connect" once and let the background service do the rest; the
// remaining commands exist for the moments that matter - checking that backups
// are actually happening, and getting files back.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/openbackup/openbackup/internal/agent/config"
	"github.com/openbackup/openbackup/internal/agent/engine"
	"github.com/openbackup/openbackup/internal/agent/ipc"
	"github.com/openbackup/openbackup/internal/agent/restore"
	"github.com/openbackup/openbackup/internal/api"
	"github.com/openbackup/openbackup/internal/codec"
	"github.com/openbackup/openbackup/internal/ignore"
	"github.com/openbackup/openbackup/internal/logx"
	"github.com/openbackup/openbackup/internal/version"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// configPath is the global --config override.
var configPath string

func run(args []string) error {
	// A bare "openbackup" should do something useful rather than print usage:
	// show the status if it is set up, or explain how to connect if it is not.
	if len(args) == 0 {
		return cmdStatus(nil)
	}

	command := args[0]
	rest := args[1:]
	// Allow --config anywhere in the argument list.
	rest, configPath = extractConfig(rest)

	switch command {
	case "connect":
		return cmdConnect(rest)
	case "run", "daemon":
		return cmdRun(rest)
	case "status":
		return cmdStatus(rest)
	case "backup":
		return cmdBackup(rest)
	case "pause":
		return cmdPause(rest)
	case "resume":
		return cmdResume(rest)
	case "snapshots":
		return cmdSnapshots(rest)
	case "find":
		return cmdFind(rest)
	case "restore":
		return cmdRestore(rest)
	case "folders":
		return cmdFolders(rest)
	case "encrypt":
		return cmdEncrypt(rest)
	case "limit":
		return cmdLimit(rest)
	case "service":
		return cmdService(rest)
	case "doctor":
		return cmdDoctor(rest)
	case "rules":
		return cmdRules(rest)
	case "version", "--version", "-v":
		fmt.Println(version.String())
		return nil
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", command)
	}
}

func usage() {
	fmt.Print(`openbackup - automatic backup for your files

Getting started:
  openbackup connect --server https://backup.example.com --code ABCD-EFGH
  openbackup service install        Run in the background from now on

Everyday use:
  openbackup status                 Is my data backed up?
  openbackup backup                 Back up right now
  openbackup pause [--for 2h]       Stop for a while
  openbackup resume                 Start again

Getting files back:
  openbackup snapshots              List the backups on the server
  openbackup find <text>            Find a file in the latest backup
  openbackup restore --path Documents/report.docx --to .
  openbackup restore --snapshot <id> --to ./restored

Folders:
  openbackup folders                Show what is backed up
  openbackup folders add <path>     Back up another folder
  openbackup folders remove <path>  Stop backing up a folder
  openbackup rules                  Explain what is excluded, and why

Other:
  openbackup encrypt                Turn on end-to-end encryption
  openbackup limit --upload 5MB     Cap the upload speed
  openbackup doctor                 Check that everything is working
  openbackup run                    Run in the foreground (for debugging)

Global flags:
  --config <path>                   Use a different configuration file
`)
}

// extractConfig pulls a --config flag out of the argument list.
func extractConfig(args []string) ([]string, string) {
	out := make([]string, 0, len(args))
	path := os.Getenv(config.EnvConfigPath)
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--config" && i+1 < len(args):
			path = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--config="):
			path = strings.TrimPrefix(args[i], "--config=")
		default:
			out = append(out, args[i])
		}
	}
	return out, path
}

func loadConfig() (*config.Config, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

// requireEnrolled loads the configuration and fails helpfully when the device is
// not connected yet.
func requireEnrolled() (*config.Config, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	if !cfg.Enrolled() {
		return nil, errors.New("this device is not connected to a server yet.\n" +
			"Get a code from your dashboard, then run:\n" +
			"  openbackup connect --server https://your-server --code YOUR-CODE")
	}
	return cfg, nil
}

func cmdConnect(args []string) error {
	fs := flag.NewFlagSet("connect", flag.ContinueOnError)
	server := fs.String("server", "", "server URL, for example https://backup.example.com")
	code := fs.String("code", "", "enrolment code from the dashboard")
	name := fs.String("name", "", "name for this device (defaults to its hostname)")
	encrypt := fs.Bool("encrypt", false, "turn on end-to-end encryption for this account")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *server == "" || *code == "" {
		return errors.New("both --server and --code are required.\n" +
			"Create a code in the dashboard under Devices, or run 'openbackup-server invite' on the server")
	}

	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if cfg.Enrolled() {
		return fmt.Errorf("this device is already connected to %s; "+
			"remove it in the dashboard first if you want to reconnect", cfg.ServerURL)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client, err := api.NewClient(*server, "")
	if err != nil {
		return err
	}
	// Check the server first so a typo in the URL produces a clear message
	// instead of a failed enrolment.
	if err := client.Health(ctx); err != nil {
		return fmt.Errorf("cannot reach %s: %w", *server, err)
	}

	deviceName := *name
	if deviceName == "" {
		deviceName = config.DefaultDeviceName()
	}

	// Encryption keys are generated on the device, before enrolment, so the
	// server never sees the material.
	var recoveryCode, keyID string
	if *encrypt {
		key, err := codec.NewRandomKey()
		if err != nil {
			return err
		}
		recoveryCode = key.RecoveryCode()
		keyID = key.ID()
	}

	hostname, _ := os.Hostname()
	resp, err := client.Enroll(ctx, api.EnrollRequest{
		JoinToken:    *code,
		DeviceName:   deviceName,
		Hostname:     hostname,
		Platform:     currentPlatform(),
		OSVersion:    runtime.GOOS + " " + runtime.GOARCH,
		AgentVersion: version.Version,
		KeyID:        keyID,
	})
	if err != nil {
		return fmt.Errorf("could not connect: %w", err)
	}

	cfg.ServerURL = client.BaseURL()
	cfg.DeviceID = resp.DeviceID
	cfg.DeviceToken = resp.DeviceToken
	cfg.DeviceName = deviceName
	cfg.RefreshDetectedRoots()
	if *encrypt {
		cfg.Encryption = config.Encryption{Enabled: true, KeyID: keyID, RecoveryCode: recoveryCode}
	}
	if err := cfg.Save(); err != nil {
		return err
	}

	fmt.Printf("Connected to %s as %q.\n\n", cfg.ServerURL, deviceName)
	printRoots(cfg)
	if *encrypt {
		fmt.Printf(`
End-to-end encryption is on. Write this recovery code down and keep it somewhere
safe - it is the only way to read your backups if this device is lost, and the
server does not have a copy:

    %s

`, recoveryCode)
	}
	fmt.Println("\nNext step: 'openbackup service install' to back up automatically in the background,")
	fmt.Println("or 'openbackup backup' to run one backup now.")
	return nil
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	once := fs.Bool("once", false, "run a single backup and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := requireEnrolled()
	if err != nil {
		return err
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

	// The control channel lets 'openbackup status' and the tray talk to this
	// process.
	control, err := ipc.Listen(stateDir, eng.Control())
	if err != nil {
		log.Warn("could not start the local control channel", "error", err)
	} else {
		defer control.Close()
	}
	return eng.Run(ctx)
}

func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if !cfg.Enrolled() {
		fmt.Println("OpenBackup is installed but not connected to a server yet.")
		fmt.Println()
		fmt.Println("Connect it with:")
		fmt.Println("  openbackup connect --server https://your-server --code YOUR-CODE")
		return nil
	}

	fmt.Printf("Server:     %s\n", cfg.ServerURL)
	fmt.Printf("Device:     %s\n", cfg.DeviceName)
	fmt.Printf("Encryption: %s\n", encryptionSummary(cfg))

	stateDir, err := config.StateDir()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Local view: is the agent running, and what is it doing?
	if client, err := ipc.Dial(stateDir); err == nil {
		var status engine.Status
		if err := client.Status(ctx, &status); err == nil {
			fmt.Printf("Agent:      %s\n", describeStatus(status))
			if status.CurrentPath != "" {
				fmt.Printf("Working on: %s\n", status.CurrentPath)
			}
			if status.LastError != "" {
				fmt.Printf("Last error: %s\n", status.LastError)
			}
		}
	} else {
		fmt.Println("Agent:      not running (start it with 'openbackup service install' or 'openbackup run')")
	}

	// Server view: what has actually arrived?
	client, err := api.NewClient(cfg.ServerURL, cfg.DeviceToken)
	if err != nil {
		return err
	}
	snapshots, err := client.ListSnapshots(ctx)
	if err != nil {
		fmt.Printf("Server:     unreachable (%v)\n", err)
		return nil
	}
	var newest *api.Snapshot
	for i := range snapshots {
		if snapshots[i].Status != api.SnapshotStatusComplete {
			continue
		}
		if newest == nil || snapshots[i].StartedAt.After(newest.StartedAt) {
			newest = &snapshots[i]
		}
	}
	if newest == nil {
		fmt.Println("Backups:    none completed yet")
		return nil
	}
	fmt.Printf("Last backup: %s (%s ago), %d files, %s\n",
		newest.StartedAt.Local().Format("2006-01-02 15:04"),
		time.Since(newest.StartedAt).Round(time.Minute),
		newest.FileCount, humanBytes(newest.TotalBytes))
	fmt.Printf("Backups kept: %d\n", len(snapshots))
	return nil
}

func cmdBackup(args []string) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	wait := fs.Bool("wait", false, "wait for the backup to finish")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := requireEnrolled()
	if err != nil {
		return err
	}
	stateDir, err := config.StateDir()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Prefer the running agent: two processes backing up at once would fight
	// over the local index.
	if client, err := ipc.Dial(stateDir); err == nil {
		if err := client.BackupNow(ctx); err != nil {
			return err
		}
		fmt.Println("Backup requested. Watch it with 'openbackup status'.")
		return nil
	}

	fmt.Println("The agent is not running, so this backup runs here in the foreground.")
	log := logx.New(logx.Options{Level: cfg.LogLevel})
	eng, err := engine.New(ctx, engine.Options{Config: cfg, Logger: log, StateDir: stateDir})
	if err != nil {
		return err
	}
	defer eng.Close()
	_ = wait
	return eng.RunOnce(ctx)
}

func cmdPause(args []string) error {
	fs := flag.NewFlagSet("pause", flag.ContinueOnError)
	duration := fs.Duration("for", 0, "pause for a period, for example 2h (default: until resumed)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := requireEnrolled()
	if err != nil {
		return err
	}
	stateDir, err := config.StateDir()
	if err != nil {
		return err
	}
	ctx := context.Background()

	if client, err := ipc.Dial(stateDir); err == nil {
		if err := client.Pause(ctx, *duration); err != nil {
			return err
		}
	} else {
		// No agent running: record the pause so it takes effect when one starts.
		cfg.Paused = true
		if err := cfg.Save(); err != nil {
			return err
		}
	}
	if *duration > 0 {
		fmt.Printf("Paused for %s.\n", *duration)
	} else {
		fmt.Println("Paused. Run 'openbackup resume' when you want backups again.")
	}
	return nil
}

func cmdResume(args []string) error {
	cfg, err := requireEnrolled()
	if err != nil {
		return err
	}
	stateDir, err := config.StateDir()
	if err != nil {
		return err
	}
	if client, err := ipc.Dial(stateDir); err == nil {
		if err := client.Resume(context.Background()); err != nil {
			return err
		}
	}
	cfg.Paused = false
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Println("Resumed.")
	return nil
}

func cmdSnapshots(args []string) error {
	fs := flag.NewFlagSet("snapshots", flag.ContinueOnError)
	limit := fs.Int("limit", 20, "how many to show")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := requireEnrolled()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := api.NewClient(cfg.ServerURL, cfg.DeviceToken)
	if err != nil {
		return err
	}
	snapshots, err := client.ListSnapshots(ctx)
	if err != nil {
		return err
	}
	if len(snapshots) == 0 {
		fmt.Println("No backups yet.")
		return nil
	}
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].StartedAt.After(snapshots[j].StartedAt)
	})
	if len(snapshots) > *limit {
		snapshots = snapshots[:*limit]
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "WHEN\tID\tKIND\tFILES\tSIZE\tSTATUS")
	for _, s := range snapshots {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\n",
			s.StartedAt.Local().Format("2006-01-02 15:04"), shortID(s.ID), s.Kind,
			s.FileCount, humanBytes(s.TotalBytes), s.Status)
	}
	return tw.Flush()
}

func cmdFind(args []string) error {
	fs := flag.NewFlagSet("find", flag.ContinueOnError)
	snapshot := fs.String("snapshot", "latest", "which backup to search")
	limit := fs.Int("limit", 25, "how many results to show")
	if err := fs.Parse(args); err != nil {
		return err
	}
	query := strings.Join(fs.Args(), " ")
	if query == "" {
		return errors.New("usage: openbackup find <text in the file name>")
	}
	cfg, err := requireEnrolled()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client, err := api.NewClient(cfg.ServerURL, cfg.DeviceToken)
	if err != nil {
		return err
	}
	snap, err := restore.FindSnapshot(ctx, client, *snapshot)
	if err != nil {
		return err
	}
	entries, err := restore.Search(ctx, client, snap.ID, query, *limit)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Printf("Nothing matching %q in the backup from %s.\n",
			query, snap.StartedAt.Local().Format("2006-01-02 15:04"))
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SIZE\tMODIFIED\tPATH")
	for _, e := range entries {
		fmt.Fprintf(tw, "%s\t%s\t%s\n",
			humanBytes(e.Size), e.ModTime.Local().Format("2006-01-02 15:04"), e.Path)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Printf("\nRestore one with:\n  openbackup restore --path \"%s\" --to .\n", entries[0].Path)
	return nil
}

func cmdRestore(args []string) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	snapshot := fs.String("snapshot", "latest", "which backup to restore from")
	pathFlag := fs.String("path", "", "file or folder inside the backup (default: everything)")
	to := fs.String("to", "", "where to write the files (default: ./restored-<date>)")
	overwrite := fs.Bool("overwrite", false, "replace files that already exist")
	keepBoth := fs.Bool("keep-both", false, "write restored copies alongside existing files")
	dryRun := fs.Bool("dry-run", false, "show what would be restored without writing anything")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := requireEnrolled()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client, err := api.NewClient(cfg.ServerURL, cfg.DeviceToken)
	if err != nil {
		return err
	}
	snap, err := restore.FindSnapshot(ctx, client, *snapshot)
	if err != nil {
		return err
	}

	codecOpts := codec.Options{}
	if cfg.Encryption.Enabled {
		key, err := codec.KeyFromRecoveryCode(cfg.Encryption.RecoveryCode)
		if err != nil {
			return fmt.Errorf("cannot use the local encryption key: %w", err)
		}
		codecOpts.Key = key
	}
	c, err := codec.New(codecOpts)
	if err != nil {
		return err
	}
	defer c.Close()

	target := *to
	if target == "" {
		target = "restored-" + time.Now().Format("2006-01-02-1504")
	}
	conflict := restore.ConflictSkip
	switch {
	case *overwrite:
		conflict = restore.ConflictOverwrite
	case *keepBoth:
		conflict = restore.ConflictRename
	}

	fmt.Printf("Restoring from the backup taken %s into %s\n",
		snap.StartedAt.Local().Format("2006-01-02 15:04"), target)
	if conflict == restore.ConflictSkip {
		fmt.Println("Existing files will be left alone (use --overwrite or --keep-both to change that).")
	}

	lastPrint := time.Now()
	result, err := restore.Run(ctx, restore.Options{
		Client:     client,
		Codec:      c,
		SnapshotID: snap.ID,
		Prefix:     *pathFlag,
		Target:     target,
		Conflict:   conflict,
		DryRun:     *dryRun,
		Progress: func(p restore.Progress) {
			// Throttle the output so restoring a million files does not spend its
			// time writing to the terminal.
			if time.Since(lastPrint) < 200*time.Millisecond {
				return
			}
			lastPrint = time.Now()
			fmt.Printf("\r%d files, %s   ", p.FilesDone, humanBytes(p.BytesWritten))
		},
	})
	fmt.Println()
	if err != nil {
		return err
	}

	if *dryRun {
		fmt.Printf("Would restore %d files (%s).\n", result.FilesRestored, humanBytes(result.BytesWritten))
		return nil
	}
	fmt.Printf("Restored %d files (%s) in %s.\n",
		result.FilesRestored, humanBytes(result.BytesWritten), result.Duration.Round(time.Second))
	if result.FilesSkipped > 0 {
		fmt.Printf("Left %d existing files alone.\n", result.FilesSkipped)
	}
	if len(result.Failed) > 0 {
		fmt.Printf("\n%d files could not be restored:\n", len(result.Failed))
		for _, f := range result.Failed[:min(len(result.Failed), 20)] {
			fmt.Println("  ", f)
		}
		return errors.New("the restore finished with errors")
	}
	return nil
}

func cmdFolders(args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if len(args) == 0 {
		printRoots(cfg)
		return nil
	}
	switch args[0] {
	case "add":
		if len(args) < 2 {
			return errors.New("usage: openbackup folders add <path>")
		}
		if err := cfg.AddRoot(args[1]); err != nil {
			return err
		}
		if err := cfg.Save(); err != nil {
			return err
		}
		fmt.Printf("Added %s.\n", args[1])
		return requestRescan()

	case "remove", "rm":
		if len(args) < 2 {
			return errors.New("usage: openbackup folders remove <path>")
		}
		if err := cfg.RemoveRoot(args[1]); err != nil {
			return err
		}
		if err := cfg.Save(); err != nil {
			return err
		}
		fmt.Printf("Stopped backing up %s. Existing backups are kept.\n", args[1])
		return nil

	default:
		return fmt.Errorf("unknown folders subcommand %q (try add or remove)", args[0])
	}
}

func cmdEncrypt(args []string) error {
	fs := flag.NewFlagSet("encrypt", flag.ContinueOnError)
	recovery := fs.String("recovery-code", "", "use an existing recovery code instead of generating one")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := requireEnrolled()
	if err != nil {
		return err
	}
	if cfg.Encryption.Enabled && *recovery == "" {
		fmt.Println("End-to-end encryption is already on for this device.")
		return nil
	}

	var key *codec.Key
	if *recovery != "" {
		// Joining an existing encrypted account: the key must match the one the
		// other devices use, or their data would be unreadable here.
		key, err = codec.KeyFromRecoveryCode(*recovery)
		if err != nil {
			return fmt.Errorf("that recovery code is not valid: %w", err)
		}
	} else {
		key, err = codec.NewRandomKey()
		if err != nil {
			return err
		}
	}

	cfg.Encryption = config.Encryption{
		Enabled:      true,
		KeyID:        key.ID(),
		RecoveryCode: key.RecoveryCode(),
	}
	if err := cfg.Save(); err != nil {
		return err
	}

	fmt.Println("End-to-end encryption is on. New backups are encrypted before they leave this device.")
	if *recovery == "" {
		fmt.Printf(`
Write this recovery code down and keep it safe. It is the only way to read your
backups if this device is lost, and the server does not have a copy:

    %s

`, cfg.Encryption.RecoveryCode)
	}
	fmt.Println("Data that was already uploaded stays unencrypted until the next full backup.")
	return requestRescan()
}

func cmdLimit(args []string) error {
	fs := flag.NewFlagSet("limit", flag.ContinueOnError)
	upload := fs.String("upload", "", "maximum upload speed, for example 5MB or 500KB (0 for unlimited)")
	cpu := fs.Float64("cpu", -1, "pause while the machine is busier than this percentage")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	changed := false
	if *upload != "" {
		n, err := parseBytes(*upload)
		if err != nil {
			return err
		}
		cfg.Limits.UploadBytesPerSec = n
		changed = true
	}
	if *cpu >= 0 {
		cfg.Limits.MaxCPUPercent = *cpu
		changed = true
	}
	if !changed {
		fmt.Printf("Upload limit: %s\n", rateSummary(cfg.Limits.UploadBytesPerSec))
		fmt.Printf("Pause above:  %.0f%% CPU\n", cfg.Limits.MaxCPUPercent)
		fmt.Printf("On battery:   %s\n", batterySummary(cfg))
		fmt.Printf("On metered:   %s\n", pauseSummary(cfg.Limits.PauseOnMetered))
		return nil
	}
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Printf("Upload limit is now %s.\n", rateSummary(cfg.Limits.UploadBytesPerSec))
	fmt.Println("Restart the agent, or wait for the next backup, for this to take effect.")
	return nil
}

func cmdRules(args []string) error {
	fs := flag.NewFlagSet("rules", flag.ContinueOnError)
	category := fs.String("category", "", "show one category only")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	fmt.Println("OpenBackup never backs up these, because they are either part of the")
	fmt.Println("operating system, or can be regenerated from what it does back up:")
	fmt.Println()

	byCategory := ignore.DefaultRuleSet()
	categories := make([]ignore.Category, 0, len(byCategory))
	for c := range byCategory {
		categories = append(categories, c)
	}
	sort.Slice(categories, func(i, j int) bool { return categories[i] < categories[j] })

	for _, c := range categories {
		if *category != "" && string(c) != *category {
			continue
		}
		fmt.Printf("%s\n", strings.ToUpper(string(c)))
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		for _, rule := range byCategory[c] {
			fmt.Fprintf(tw, "  %s\t%s\n", rule.Pattern, rule.Reason)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
		fmt.Println()
	}

	if len(cfg.Ignore.Exclude) > 0 {
		fmt.Println("YOUR OWN EXCLUSIONS")
		for _, p := range cfg.Ignore.Exclude {
			fmt.Printf("  %s\n", p)
		}
		fmt.Println()
	}
	if len(cfg.Ignore.Include) > 0 {
		fmt.Println("YOUR OWN OVERRIDES (backed up even if a rule above excludes them)")
		for _, p := range cfg.Ignore.Include {
			fmt.Printf("  %s\n", p)
		}
		fmt.Println()
	}
	fmt.Println("Source code is always backed up. Only dependency and build folders inside a")
	fmt.Println("recognised project are skipped, and only when their marker file is present")
	fmt.Println("(package.json, go.mod, Cargo.toml, and so on).")
	return nil
}

func cmdDoctor(args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ok := true
	check := func(label string, passed bool, detail string) {
		mark := "ok  "
		if !passed {
			mark = "FAIL"
			ok = false
		}
		fmt.Printf("[%s] %s", mark, label)
		if detail != "" {
			fmt.Printf(" - %s", detail)
		}
		fmt.Println()
	}

	fmt.Printf("OpenBackup %s on %s/%s\n\n", version.Version, runtime.GOOS, runtime.GOARCH)
	check("configuration file", true, cfg.Path())
	check("connected to a server", cfg.Enrolled(), cfg.ServerURL)

	if cfg.Enrolled() {
		client, err := api.NewClient(cfg.ServerURL, cfg.DeviceToken)
		if err == nil {
			err = client.Health(ctx)
		}
		check("server reachable", err == nil, errText(err))

		if err == nil {
			_, hbErr := client.Heartbeat(ctx, api.HeartbeatRequest{State: api.StateIdle, AgentVersion: version.Version})
			check("device credentials accepted", hbErr == nil, errText(hbErr))
		}
	}

	roots := cfg.EnabledRoots()
	check("folders to back up", len(roots) > 0, fmt.Sprintf("%d folder(s)", len(roots)))
	for _, missing := range cfg.MissingRoots() {
		check("folder available: "+missing.Path, false, "not found (drive disconnected?)")
	}

	stateDir, err := config.StateDir()
	if err == nil {
		_, statErr := os.Stat(filepath.Join(stateDir, "index.db"))
		check("local index", statErr == nil || os.IsNotExist(statErr),
			map[bool]string{true: "not created yet (no backup has run)", false: stateDir}[os.IsNotExist(statErr)])
		if _, err := ipc.Dial(stateDir); err == nil {
			check("agent running", true, "")
		} else {
			check("agent running", false, "start it with 'openbackup service install'")
		}
	}

	fmt.Println()
	if ok {
		fmt.Println("Everything looks healthy.")
		return nil
	}
	return errors.New("some checks failed; see above")
}

// requestRescan asks a running agent to pick up a configuration change.
func requestRescan() error {
	stateDir, err := config.StateDir()
	if err != nil {
		return nil
	}
	client, err := ipc.Dial(stateDir)
	if err != nil {
		return nil
	}
	if err := client.BackupNow(context.Background()); err == nil {
		fmt.Println("The running agent will pick this up on its next pass.")
	}
	return nil
}

func printRoots(cfg *config.Config) {
	if len(cfg.Roots) == 0 {
		fmt.Println("No folders are configured.")
		return
	}
	fmt.Println("Folders:")
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, root := range cfg.Roots {
		state := "backed up"
		if !root.Enabled {
			state = "off"
		}
		if _, err := os.Stat(root.Path); err != nil {
			state = "missing"
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", root.Name, state, root.Path)
	}
	_ = tw.Flush()
}

func describeStatus(status engine.Status) string {
	if status.Paused {
		if status.PauseReason != "" {
			return "paused (" + status.PauseReason + ")"
		}
		return "paused"
	}
	switch status.State {
	case api.StateUploading:
		return fmt.Sprintf("uploading (%d files, %s so far)", status.FilesDone, humanBytes(status.BytesUploaded))
	case api.StateScanning:
		return "looking for changes"
	case api.StateError:
		return "error: " + status.LastError
	default:
		return "idle, watching for changes"
	}
}

func encryptionSummary(cfg *config.Config) string {
	if cfg.Encryption.Enabled {
		return "on (end-to-end, key id " + cfg.Encryption.KeyID + ")"
	}
	return "off (data is compressed and sent over TLS, but the server can read it)"
}

func batterySummary(cfg *config.Config) string {
	if cfg.Limits.PauseOnBattery {
		return "paused"
	}
	return fmt.Sprintf("runs until %d%% charge", cfg.Limits.MinBatteryPercent)
}

func pauseSummary(pause bool) string {
	if pause {
		return "paused"
	}
	return "runs"
}

func rateSummary(bytesPerSec int64) string {
	if bytesPerSec <= 0 {
		return "unlimited"
	}
	return humanBytes(bytesPerSec) + "/s"
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func shortID(id string) string {
	if len(id) > 14 {
		return id[:14]
	}
	return id
}

func currentPlatform() api.Platform {
	switch runtime.GOOS {
	case "windows":
		return api.PlatformWindows
	case "darwin":
		return api.PlatformDarwin
	case "android":
		return api.PlatformAndroid
	default:
		return api.PlatformLinux
	}
}

// parseBytes accepts sizes such as "5MB", "500KB" or a plain byte count.
func parseBytes(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	multiplier := int64(1)
	switch {
	case strings.HasSuffix(s, "GB"), strings.HasSuffix(s, "G"):
		multiplier = 1 << 30
	case strings.HasSuffix(s, "MB"), strings.HasSuffix(s, "M"):
		multiplier = 1 << 20
	case strings.HasSuffix(s, "KB"), strings.HasSuffix(s, "K"):
		multiplier = 1 << 10
	}
	digits := strings.TrimRight(s, "KMGB ")
	var value float64
	if _, err := fmt.Sscanf(digits, "%f", &value); err != nil {
		return 0, fmt.Errorf("cannot understand the size %q; try something like 5MB", s)
	}
	return int64(value * float64(multiplier)), nil
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value := float64(n)
	for _, suffix := range []string{"KB", "MB", "GB", "TB", "PB"} {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, suffix)
		}
	}
	return fmt.Sprintf("%.1f EB", value/unit)
}
