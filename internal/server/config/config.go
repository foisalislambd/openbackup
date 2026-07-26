// Package config loads the server configuration.
//
// Everything has a working default and can be overridden by an environment
// variable, because the target deployment is a single docker compose file on a
// VPS where editing a config file is friction the user should not need.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config is the server configuration.
type Config struct {
	// Addr is the listen address.
	Addr string
	// DataDir holds the SQLite database and the blob store.
	DataDir string
	// PublicURL is the externally reachable base URL, shown in enrolment
	// instructions and used to build absolute links.
	PublicURL string
	// TrustProxy makes the server honour X-Forwarded-For, which is required
	// behind Caddy, nginx or Cloudflare and dangerous when exposed directly.
	TrustProxy bool
	// SessionTTL is how long a dashboard login lasts.
	SessionTTL time.Duration
	// JoinTokenTTL is how long an enrolment code stays valid.
	JoinTokenTTL time.Duration
	// AllowSignup permits self-registration. After the first account exists the
	// default is to require an invite, so an exposed server is not an open
	// storage relay.
	AllowSignup bool
	// MaxChunkBytes bounds a single chunk upload.
	MaxChunkBytes int64
	// MaxBodyBytes bounds JSON request bodies.
	MaxBodyBytes int64
	// RetentionDays is the default retention for new accounts.
	RetentionDays int
	// GCInterval is how often the retention and garbage collection job runs.
	GCInterval time.Duration
	// QuotaBytes is the default per-account quota; 0 means unlimited.
	QuotaBytes int64
	// RequireEncryption refuses plaintext chunks server-wide. Accounts can also
	// require it individually from the dashboard; this is the harder switch, for
	// an operator who never wants readable data on the disk they administer.
	RequireEncryption bool
	// LogLevel is debug, info, warn or error.
	LogLevel string
	// LogJSON emits structured logs, which is what you want in a container.
	LogJSON bool
	// SecureCookies forces the Secure flag. Enabled automatically when
	// PublicURL is https.
	SecureCookies bool
	// AdminEmail and AdminPassword bootstrap the first account on an empty
	// database, which is what makes the one-command install work end to end.
	AdminEmail    string
	AdminPassword string
}

// Default returns the built-in configuration.
func Default() Config {
	return Config{
		Addr:          ":18200",
		DataDir:       "./data",
		SessionTTL:    30 * 24 * time.Hour,
		JoinTokenTTL:  24 * time.Hour,
		AllowSignup:   true,
		MaxChunkBytes: 16 << 20,
		MaxBodyBytes:  32 << 20,
		RetentionDays: 30,
		GCInterval:    time.Hour,
		LogLevel:      "info",
		LogJSON:       true,
	}
}

// Load builds a configuration from the environment, applying defaults.
func Load() (Config, error) {
	cfg := Default()
	cfg.Addr = env("OPENBACKUP_ADDR", cfg.Addr)
	cfg.DataDir = env("OPENBACKUP_DATA_DIR", cfg.DataDir)
	cfg.PublicURL = strings.TrimRight(env("OPENBACKUP_PUBLIC_URL", cfg.PublicURL), "/")
	cfg.LogLevel = env("OPENBACKUP_LOG_LEVEL", cfg.LogLevel)
	cfg.AdminEmail = env("OPENBACKUP_ADMIN_EMAIL", "")
	cfg.AdminPassword = env("OPENBACKUP_ADMIN_PASSWORD", "")

	var err error
	if cfg.TrustProxy, err = envBool("OPENBACKUP_TRUST_PROXY", cfg.TrustProxy); err != nil {
		return cfg, err
	}
	if cfg.AllowSignup, err = envBool("OPENBACKUP_ALLOW_SIGNUP", cfg.AllowSignup); err != nil {
		return cfg, err
	}
	if cfg.LogJSON, err = envBool("OPENBACKUP_LOG_JSON", cfg.LogJSON); err != nil {
		return cfg, err
	}
	if cfg.RequireEncryption, err = envBool("OPENBACKUP_REQUIRE_ENCRYPTION", cfg.RequireEncryption); err != nil {
		return cfg, err
	}
	if cfg.SecureCookies, err = envBool("OPENBACKUP_SECURE_COOKIES", strings.HasPrefix(cfg.PublicURL, "https://")); err != nil {
		return cfg, err
	}
	if cfg.SessionTTL, err = envDuration("OPENBACKUP_SESSION_TTL", cfg.SessionTTL); err != nil {
		return cfg, err
	}
	if cfg.JoinTokenTTL, err = envDuration("OPENBACKUP_JOIN_TOKEN_TTL", cfg.JoinTokenTTL); err != nil {
		return cfg, err
	}
	if cfg.GCInterval, err = envDuration("OPENBACKUP_GC_INTERVAL", cfg.GCInterval); err != nil {
		return cfg, err
	}
	if cfg.RetentionDays, err = envInt("OPENBACKUP_RETENTION_DAYS", cfg.RetentionDays); err != nil {
		return cfg, err
	}
	if cfg.MaxChunkBytes, err = envBytes("OPENBACKUP_MAX_CHUNK_BYTES", cfg.MaxChunkBytes); err != nil {
		return cfg, err
	}
	if cfg.MaxBodyBytes, err = envBytes("OPENBACKUP_MAX_BODY_BYTES", cfg.MaxBodyBytes); err != nil {
		return cfg, err
	}
	if cfg.QuotaBytes, err = envBytes("OPENBACKUP_QUOTA_BYTES", cfg.QuotaBytes); err != nil {
		return cfg, err
	}
	return cfg, cfg.Validate()
}

// Validate checks the configuration for values that would fail at runtime.
func (c *Config) Validate() error {
	if c.Addr == "" {
		return errors.New("config: listen address must not be empty")
	}
	if c.DataDir == "" {
		return errors.New("config: data directory must not be empty")
	}
	if c.RetentionDays < 0 {
		return errors.New("config: retention days must not be negative")
	}
	if c.MaxChunkBytes <= 0 {
		return errors.New("config: max chunk bytes must be positive")
	}
	if c.AdminPassword != "" && c.AdminEmail == "" {
		return errors.New("config: OPENBACKUP_ADMIN_PASSWORD requires OPENBACKUP_ADMIN_EMAIL")
	}
	return nil
}

// DBPath returns the SQLite file path.
func (c *Config) DBPath() string { return filepath.Join(c.DataDir, "openbackup.db") }

// BlobDir returns the blob store root.
func (c *Config) BlobDir() string { return filepath.Join(c.DataDir, "blobs") }

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) (bool, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def, fmt.Errorf("config: %s must be a boolean: %w", key, err)
	}
	return b, nil
}

func envInt(key string, def int) (int, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def, fmt.Errorf("config: %s must be an integer: %w", key, err)
	}
	return n, nil
}

func envDuration(key string, def time.Duration) (time.Duration, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def, fmt.Errorf("config: %s must be a duration such as 30m or 24h: %w", key, err)
	}
	return d, nil
}

// envBytes parses a size that may carry a unit suffix, so operators can write
// OPENBACKUP_QUOTA_BYTES=500GB instead of counting zeros.
func envBytes(key string, def int64) (int64, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def, nil
	}
	n, err := ParseBytes(v)
	if err != nil {
		return def, fmt.Errorf("config: %s: %w", key, err)
	}
	return n, nil
}

// ParseBytes parses sizes such as "10", "512K", "4MiB", "2 GB".
func ParseBytes(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	if s == "" {
		return 0, errors.New("empty size")
	}
	multipliers := []struct {
		suffix string
		factor int64
	}{
		{"KIB", 1 << 10}, {"MIB", 1 << 20}, {"GIB", 1 << 30}, {"TIB", 1 << 40},
		{"KB", 1000}, {"MB", 1000 * 1000}, {"GB", 1000 * 1000 * 1000}, {"TB", 1000 * 1000 * 1000 * 1000},
		{"K", 1 << 10}, {"M", 1 << 20}, {"G", 1 << 30}, {"T", 1 << 40}, {"B", 1},
	}
	for _, m := range multipliers {
		if strings.HasSuffix(s, m.suffix) {
			num := strings.TrimSpace(strings.TrimSuffix(s, m.suffix))
			f, err := strconv.ParseFloat(num, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid size %q", s)
			}
			return int64(f * float64(m.factor)), nil
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	return n, nil
}
