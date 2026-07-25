// Command openbackup-server is the self-hosted OpenBackup server: agent
// protocol, dashboard API, and the embedded web UI in a single binary.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/openbackup/openbackup/internal/idgen"
	"github.com/openbackup/openbackup/internal/logx"
	"github.com/openbackup/openbackup/internal/server/auth"
	"github.com/openbackup/openbackup/internal/server/config"
	"github.com/openbackup/openbackup/internal/server/httpapi"
	"github.com/openbackup/openbackup/internal/server/maintenance"
	"github.com/openbackup/openbackup/internal/server/store"
	"github.com/openbackup/openbackup/internal/server/web"
	"github.com/openbackup/openbackup/internal/version"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	command := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command, args = args[0], args[1:]
	}
	switch command {
	case "serve":
		return runServe(args)
	case "check":
		return runCheck(args)
	case "invite":
		return runInvite(args)
	case "user":
		return runUser(args)
	case "version":
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
	fmt.Print(`openbackup-server - self-hosted backup server

Usage:
  openbackup-server [serve]            Run the server (default)
  openbackup-server check [--fix]      Verify that stored data matches the index
  openbackup-server invite [--email]   Create a device enrolment code
  openbackup-server user add           Create an account
  openbackup-server version            Print the build version

Configuration comes from the environment; every value has a working default:
  OPENBACKUP_ADDR              listen address              (default :8080)
  OPENBACKUP_DATA_DIR          data directory              (default ./data)
  OPENBACKUP_PUBLIC_URL        externally reachable URL
  OPENBACKUP_ADMIN_EMAIL       bootstrap the first account
  OPENBACKUP_ADMIN_PASSWORD    bootstrap password
  OPENBACKUP_RETENTION_DAYS    default snapshot retention  (default 30)
  OPENBACKUP_QUOTA_BYTES       per-account quota, e.g. 500GB
  OPENBACKUP_TRUST_PROXY       honour X-Forwarded-For      (default false)
  OPENBACKUP_S3_ENDPOINT       store blobs in S3/MinIO instead of local disk
  OPENBACKUP_S3_BUCKET         bucket name
  OPENBACKUP_S3_ACCESS_KEY     access key
  OPENBACKUP_S3_SECRET_KEY     secret key
  OPENBACKUP_S3_USE_SSL        TLS to the object store     (default true)
`)
}

// deps holds the dependencies shared by every command.
type deps struct {
	cfg   config.Config
	db    *store.DB
	blobs store.Blobs
	log   *slog.Logger
}

// setup loads the configuration and opens storage.
func setup(ctx context.Context) (*deps, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	log := logx.New(logx.Options{Level: cfg.LogLevel, JSON: cfg.LogJSON})

	db, err := store.OpenDB(ctx, cfg.DBPath())
	if err != nil {
		return nil, err
	}
	blobs, err := openBlobs(ctx, cfg)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	log.Info("storage ready", "blobs", blobs.Location(), "database", cfg.DBPath())
	return &deps{cfg: cfg, db: db, blobs: blobs, log: log}, nil
}

func (d *deps) Close() {
	if d.db != nil {
		_ = d.db.Close()
	}
}

// openBlobs selects the storage backend. Local disk is the default because it
// needs no credentials and no third party.
func openBlobs(ctx context.Context, cfg config.Config) (store.Blobs, error) {
	if endpoint := os.Getenv("OPENBACKUP_S3_ENDPOINT"); endpoint != "" {
		useSSL := true
		if v := os.Getenv("OPENBACKUP_S3_USE_SSL"); v != "" {
			useSSL = v != "false" && v != "0"
		}
		return store.NewS3Blobs(ctx, store.S3Config{
			Endpoint:  endpoint,
			Bucket:    os.Getenv("OPENBACKUP_S3_BUCKET"),
			Prefix:    os.Getenv("OPENBACKUP_S3_PREFIX"),
			AccessKey: os.Getenv("OPENBACKUP_S3_ACCESS_KEY"),
			SecretKey: os.Getenv("OPENBACKUP_S3_SECRET_KEY"),
			Region:    os.Getenv("OPENBACKUP_S3_REGION"),
			UseSSL:    useSSL,
		})
	}
	return store.NewFSBlobs(cfg.BlobDir())
}

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", "", "listen address (overrides OPENBACKUP_ADDR)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	d, err := setup(ctx)
	if err != nil {
		return err
	}
	defer d.Close()

	if *addr != "" {
		d.cfg.Addr = *addr
	}
	if err := bootstrapAdmin(ctx, d); err != nil {
		return err
	}

	ui := web.Handler()
	if ui == nil {
		d.log.Warn("no dashboard bundled in this build; the API is still fully usable")
	}
	srv, err := httpapi.New(httpapi.Options{
		Config: d.cfg,
		DB:     d.db,
		Blobs:  d.blobs,
		Logger: d.log,
		WebFS:  ui,
	})
	if err != nil {
		return err
	}
	defer srv.Close()

	// Maintenance runs in the background: retention, garbage collection, and
	// cleaning up after agents that died mid-backup.
	go maintenance.New(d.db, d.blobs, d.log).Start(ctx, d.cfg.GCInterval)

	httpServer := &http.Server{
		Addr:    d.cfg.Addr,
		Handler: srv.Handler(),
		// Generous write timeout: a folder restore streams a ZIP that can take
		// hours on a slow link, and cutting it off would be worse than useless.
		ReadHeaderTimeout: 20 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		d.log.Info("server listening", "addr", d.cfg.Addr, "version", version.Version, "public_url", d.cfg.PublicURL)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		d.log.Info("shutting down")
		// Give in-flight uploads and restores a moment to finish cleanly.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	}
}

// bootstrapAdmin creates the first account from the environment, which is what
// lets a one-command install end with a usable login.
func bootstrapAdmin(ctx context.Context, d *deps) error {
	if d.cfg.AdminEmail == "" || d.cfg.AdminPassword == "" {
		return nil
	}
	count, err := d.db.CountUsers(ctx)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	hash, err := auth.HashPassword(d.cfg.AdminPassword)
	if err != nil {
		return fmt.Errorf("bootstrap admin: %w", err)
	}
	user, err := d.db.CreateUser(ctx, d.cfg.AdminEmail, hash, true)
	if err != nil {
		return fmt.Errorf("bootstrap admin: %w", err)
	}
	d.log.Info("created the first administrator account", "email", user.Email)
	return nil
}

func runCheck(args []string) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fix := fs.Bool("fix", false, "delete stored objects that the index does not reference")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()
	d, err := setup(ctx)
	if err != nil {
		return err
	}
	defer d.Close()

	result, err := maintenance.New(d.db, d.blobs, d.log).Check(ctx, *fix)
	if err != nil {
		return err
	}
	fmt.Printf("scanned %d stored objects in %s\n", result.ScannedBlobs, result.Duration.Round(time.Millisecond))
	fmt.Printf("orphan objects: %d%s\n", len(result.OrphanBlobs), map[bool]string{true: " (deleted)", false: ""}[*fix])
	fmt.Printf("missing objects: %d\n", len(result.MissingBlobs))
	fmt.Printf("snapshots that would fail to restore: %d\n", len(result.UnrestorableSnapshots))
	for _, id := range result.UnrestorableSnapshots {
		fmt.Println("  ", id)
	}
	if len(result.MissingBlobs) > 0 {
		return errors.New("repository is missing data; the snapshots listed above cannot be fully restored")
	}
	return nil
}

func runInvite(args []string) error {
	fs := flag.NewFlagSet("invite", flag.ContinueOnError)
	email := fs.String("email", "", "account to enrol the device into (defaults to the only account)")
	label := fs.String("label", "", "note shown next to the code in the dashboard")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()
	d, err := setup(ctx)
	if err != nil {
		return err
	}
	defer d.Close()

	user, err := resolveUser(ctx, d, *email)
	if err != nil {
		return err
	}
	code, err := idgen.JoinCode()
	if err != nil {
		return err
	}
	token, err := d.db.CreateJoinToken(ctx, user.ID, auth.HashToken(idgen.NormalizeJoinCode(code)), *label, d.cfg.JoinTokenTTL)
	if err != nil {
		return err
	}
	serverURL := d.cfg.PublicURL
	if serverURL == "" {
		serverURL = "http://localhost" + d.cfg.Addr
	}
	fmt.Printf(`Enrolment code for %s (valid until %s):

    %s

Run this on the device you want to back up:

    openbackup connect --server %s --code %s

`, user.Email, token.ExpiresAt.Local().Format(time.RFC1123), code, serverURL, code)
	return nil
}

func runUser(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: openbackup-server user add --email <email> [--password <password>]")
	}
	switch args[0] {
	case "add":
	default:
		return fmt.Errorf("unknown user subcommand %q", args[0])
	}
	fs := flag.NewFlagSet("user add", flag.ContinueOnError)
	email := fs.String("email", "", "email address")
	password := fs.String("password", "", "password (generated when omitted)")
	admin := fs.Bool("admin", true, "grant administrator rights")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *email == "" {
		return errors.New("--email is required")
	}
	generated := false
	if *password == "" {
		secret, err := idgen.Secret(12)
		if err != nil {
			return err
		}
		*password = secret
		generated = true
	}

	ctx := context.Background()
	d, err := setup(ctx)
	if err != nil {
		return err
	}
	defer d.Close()

	hash, err := auth.HashPassword(*password)
	if err != nil {
		return err
	}
	user, err := d.db.CreateUser(ctx, *email, hash, *admin)
	if err != nil {
		return err
	}
	fmt.Printf("created account %s\n", user.Email)
	if generated {
		fmt.Printf("password: %s\n", *password)
	}
	return nil
}

// resolveUser finds the account to act on, defaulting to the only one when a
// server has just one, which is the common self-hosted case.
func resolveUser(ctx context.Context, d *deps, email string) (*store.User, error) {
	if email != "" {
		return d.db.UserByEmail(ctx, email)
	}
	count, err := d.db.CountUsers(ctx)
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, errors.New("no accounts exist yet; create one with 'openbackup-server user add --email you@example.com'")
	}
	if count > 1 {
		return nil, errors.New("several accounts exist; pass --email to choose one")
	}
	return d.db.FirstUser(ctx)
}
