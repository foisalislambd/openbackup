// Package config manages the agent's on-disk configuration and state paths.
//
// The guiding rule is that a new install must work with an empty config: the
// agent discovers the user's folders itself, applies the default ignore rules,
// and only writes a file once there is something worth remembering (the server
// URL, the device token, and any folders the user added by hand).
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/foisalislambd/openbackup/internal/ignore"
	"github.com/foisalislambd/openbackup/internal/userdirs"
)

// FileName is the configuration file name.
const FileName = "config.json"

// EnvConfigPath overrides the configuration location, used by the service
// installer and by tests.
const EnvConfigPath = "OPENBACKUP_CONFIG"

// Root is one backed-up folder.
type Root struct {
	// Name is a stable identifier used in snapshots, for example "documents".
	Name string `json:"name"`
	Path string `json:"path"`
	// Enabled allows a user to keep a folder configured but paused.
	Enabled bool `json:"enabled"`
	// Detected marks folders discovered automatically, so a later version can
	// re-detect them without touching the user's own additions.
	Detected bool `json:"detected,omitempty"`
}

// Ignore mirrors the ignore engine's user-facing settings.
type Ignore struct {
	DisabledCategories []string `json:"disabled_categories,omitempty"`
	Exclude            []string `json:"exclude,omitempty"`
	Include            []string `json:"include,omitempty"`
	// MaxFileSizeBytes skips very large files; 0 uses the default, -1 disables
	// the limit.
	MaxFileSizeBytes int64 `json:"max_file_size_bytes,omitempty"`
	SkipHidden       bool  `json:"skip_hidden,omitempty"`
}

// Limits keeps the agent a good citizen on the user's machine.
//
// These defaults are the difference between software people keep and software
// they uninstall: a backup that slows a game or eats a metered connection gets
// blamed for everything.
type Limits struct {
	// UploadBytesPerSec throttles uploads; 0 means unlimited.
	UploadBytesPerSec int64 `json:"upload_bytes_per_sec"`
	// MaxCPUPercent pauses work while the machine is busier than this.
	MaxCPUPercent float64 `json:"max_cpu_percent"`
	// PauseOnMetered avoids mobile hotspots and capped connections.
	PauseOnMetered bool `json:"pause_on_metered"`
	// PauseOnBattery avoids draining a laptop that is unplugged.
	PauseOnBattery bool `json:"pause_on_battery"`
	// MinBatteryPercent pauses below this level even while charging.
	MinBatteryPercent int `json:"min_battery_percent"`
	// PauseWhileFullscreen pauses during games and presentations.
	PauseWhileFullscreen bool `json:"pause_while_fullscreen"`
	// UploadConcurrency is how many chunks upload at once.
	UploadConcurrency int `json:"upload_concurrency"`
}

// Schedule controls when work happens.
type Schedule struct {
	// FullScanInterval is how often the whole tree is re-walked to catch changes
	// the watcher missed (an offline period, a dropped event, an external drive).
	FullScanInterval time.Duration `json:"full_scan_interval"`
	// Debounce is how long to wait after a file stops changing before backing it
	// up, which avoids uploading a document forty times while it is being saved.
	Debounce time.Duration `json:"debounce"`
	// HeartbeatInterval is how often the agent reports in.
	HeartbeatInterval time.Duration `json:"heartbeat_interval"`
	// MaxDeltaChainLength forces a full snapshot after this many deltas.
	MaxDeltaChainLength int `json:"max_delta_chain_length"`
}

// Encryption holds the end-to-end encryption state.
type Encryption struct {
	Enabled bool `json:"enabled"`
	// KeyID is the public identifier of the key, safe to store and log.
	KeyID string `json:"key_id,omitempty"`
	// RecoveryCode is the master key in transcribable form.
	//
	// Storing it locally is unavoidable for an unattended agent: it must be able
	// to encrypt after a reboot with nobody logged in. The file is written with
	// owner-only permissions, and the server never receives it. Users who want
	// the key off the disk entirely can leave encryption off and rely on
	// full-disk encryption plus TLS instead.
	RecoveryCode string `json:"recovery_code,omitempty"`
}

// Config is the agent configuration.
type Config struct {
	ServerURL   string `json:"server_url"`
	DeviceID    string `json:"device_id,omitempty"`
	DeviceToken string `json:"device_token,omitempty"`
	DeviceName  string `json:"device_name,omitempty"`
	Roots       []Root `json:"roots"`
	// RootsChosen records that the folder list is the user's own, so an empty
	// list stays empty. Without it, removing the last folder would look like a
	// fresh install and every detected folder would come back.
	RootsChosen bool       `json:"roots_chosen,omitempty"`
	Ignore      Ignore     `json:"ignore"`
	Limits      Limits     `json:"limits"`
	Schedule    Schedule   `json:"schedule"`
	Encryption  Encryption `json:"encryption"`
	// Paused stops all backup work until the user resumes.
	Paused bool `json:"paused"`
	// LogLevel is debug, info, warn or error.
	LogLevel string `json:"log_level,omitempty"`

	// path is where this config was loaded from, not serialised.
	path string `json:"-"`
	mu   sync.Mutex
}

// Default returns the configuration a fresh install runs with.
func Default() *Config {
	return &Config{
		Roots: nil, // filled in by DetectRoots
		Limits: Limits{
			UploadBytesPerSec: 0,
			MaxCPUPercent:     70,
			PauseOnMetered:    true,
			// Backing up on battery is allowed above the floor: laptops are
			// often never plugged in, and refusing to back them up would be a
			// silent failure.
			PauseOnBattery:       false,
			MinBatteryPercent:    20,
			PauseWhileFullscreen: true,
			UploadConcurrency:    3,
		},
		Schedule: Schedule{
			FullScanInterval:    12 * time.Hour,
			Debounce:            15 * time.Second,
			HeartbeatInterval:   time.Minute,
			MaxDeltaChainLength: 24,
		},
		LogLevel: "info",
	}
}

// DefaultPath returns the configuration path for the current platform.
func DefaultPath() (string, error) {
	if v := os.Getenv(EnvConfigPath); v != "" {
		return v, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "OpenBackup", FileName), nil
}

// StateDir returns the directory for the local index and logs. It is separate
// from the configuration so a user can delete a corrupt cache without losing
// their enrolment.
func StateDir() (string, error) {
	if v := os.Getenv("OPENBACKUP_STATE_DIR"); v != "" {
		return v, nil
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "OpenBackup"), nil
}

// Load reads the configuration, returning defaults when the file is absent.
func Load(path string) (*Config, error) {
	if path == "" {
		var err error
		path, err = DefaultPath()
		if err != nil {
			return nil, err
		}
	}
	cfg := Default()
	cfg.path = path

	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		cfg.Roots = DetectRoots()
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	if err := json.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	cfg.path = path
	cfg.normalize()
	return cfg, nil
}

// normalize fills in anything missing after a partial or older config file.
func (c *Config) normalize() {
	def := Default()
	if c.Schedule.FullScanInterval <= 0 {
		c.Schedule.FullScanInterval = def.Schedule.FullScanInterval
	}
	if c.Schedule.Debounce <= 0 {
		c.Schedule.Debounce = def.Schedule.Debounce
	}
	if c.Schedule.HeartbeatInterval <= 0 {
		c.Schedule.HeartbeatInterval = def.Schedule.HeartbeatInterval
	}
	if c.Schedule.MaxDeltaChainLength <= 0 {
		c.Schedule.MaxDeltaChainLength = def.Schedule.MaxDeltaChainLength
	}
	if c.Limits.UploadConcurrency <= 0 {
		c.Limits.UploadConcurrency = def.Limits.UploadConcurrency
	}
	if c.Limits.MaxCPUPercent <= 0 {
		c.Limits.MaxCPUPercent = def.Limits.MaxCPUPercent
	}
	if c.LogLevel == "" {
		c.LogLevel = def.LogLevel
	}
	if len(c.Roots) == 0 && !c.RootsChosen {
		c.Roots = DetectRoots()
	}
}

// Path returns where the configuration lives.
func (c *Config) Path() string { return c.path }

// Settings is a consistent copy of everything that can change while the agent is
// running.
//
// The running agent reads its settings from copies rather than from the live
// struct: the desktop app and the command line can both change folders or limits
// at any moment, and a scan that read half of an update would back up the wrong
// set of files.
type Settings struct {
	Roots      []Root
	Ignore     Ignore
	Limits     Limits
	Schedule   Schedule
	Encryption Encryption
	Paused     bool
}

// Settings returns a snapshot of the mutable settings.
func (c *Config) Settings() Settings {
	c.mu.Lock()
	defer c.mu.Unlock()
	roots := make([]Root, len(c.Roots))
	copy(roots, c.Roots)
	s := Settings{
		Roots:      roots,
		Ignore:     c.Ignore,
		Limits:     c.Limits,
		Schedule:   c.Schedule,
		Encryption: c.Encryption,
		Paused:     c.Paused,
	}
	// The slices inside Ignore are shared with the config otherwise, and a
	// caller appending to one would mutate the live configuration.
	s.Ignore.Exclude = append([]string(nil), c.Ignore.Exclude...)
	s.Ignore.Include = append([]string(nil), c.Ignore.Include...)
	s.Ignore.DisabledCategories = append([]string(nil), c.Ignore.DisabledCategories...)
	return s
}

// Update mutates the configuration under the lock and writes it to disk, so a
// change is never half-applied and never lost.
func (c *Config) Update(fn func(*Config) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if fn != nil {
		if err := fn(c); err != nil {
			return err
		}
	}
	return c.saveLocked()
}

// Save writes the configuration atomically with owner-only permissions.
func (c *Config) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.saveLocked()
}

func (c *Config) saveLocked() error {
	if c.path == "" {
		return errors.New("config: no path set")
	}
	// Anything written to disk is a deliberate configuration, folder list
	// included.
	c.RootsChosen = true
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')

	tmp := c.path + ".tmp"
	// 0600: the file holds the device token and, when encryption is on, the
	// master key.
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, c.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Enrolled reports whether this agent has device credentials.
func (c *Config) Enrolled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ServerURL != "" && c.DeviceToken != "" && c.DeviceID != ""
}

// EnabledRoots returns the folders that should be backed up, skipping any that
// no longer exist so a detached external drive is not treated as a deletion.
func (c *Config) EnabledRoots() []Root {
	return c.Settings().EnabledRoots()
}

// EnabledRoots filters a settings snapshot to the folders worth walking.
func (s Settings) EnabledRoots() []Root {
	out := make([]Root, 0, len(s.Roots))
	for _, r := range s.Roots {
		if !r.Enabled {
			continue
		}
		st, err := os.Stat(r.Path)
		if err != nil || !st.IsDir() {
			continue
		}
		out = append(out, r)
	}
	return out
}

// MissingRoots returns configured folders that are currently unavailable, so the
// agent can report them instead of silently backing up less than the user thinks.
func (c *Config) MissingRoots() []Root {
	return c.Settings().MissingRoots()
}

// MissingRoots lists the enabled folders in a snapshot that are not there.
func (s Settings) MissingRoots() []Root {
	var out []Root
	for _, r := range s.Roots {
		if !r.Enabled {
			continue
		}
		if st, err := os.Stat(r.Path); err != nil || !st.IsDir() {
			out = append(out, r)
		}
	}
	return out
}

// AddRoot adds a folder, refusing paths that are protected or already covered.
//
// AddRoot and the other plain mutators do not take the lock themselves: pass them
// to Update when the agent is running, so the change is atomic and persisted.
func (c *Config) AddRoot(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("config: %s cannot be read: %w", abs, err)
	}
	if !st.IsDir() {
		return fmt.Errorf("config: %s is not a folder", abs)
	}
	matcher := ignore.New(ignore.Config{})
	if d := matcher.IsForbiddenRoot(abs); d.Skip {
		return fmt.Errorf("config: %s is a protected system location (%s)", abs, d.Reason)
	}
	for _, r := range c.Roots {
		if pathsOverlap(r.Path, abs) {
			return fmt.Errorf("config: %s is already covered by %s", abs, r.Path)
		}
	}
	c.Roots = append(c.Roots, Root{Name: rootName(abs), Path: abs, Enabled: true})
	return nil
}

// RemoveRoot drops a folder by path or name.
func (c *Config) RemoveRoot(pathOrName string) error {
	target := strings.ToLower(strings.TrimSpace(pathOrName))
	for i, r := range c.Roots {
		if strings.ToLower(r.Path) == target || strings.ToLower(r.Name) == target {
			c.Roots = append(c.Roots[:i], c.Roots[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("config: no backed-up folder matches %q", pathOrName)
}

// DetectRoots discovers the user's well-known folders.
func DetectRoots() []Root {
	dirs := userdirs.Detect()
	out := make([]Root, 0, len(dirs))
	for _, d := range dirs {
		out = append(out, Root{
			Name:     string(d.Kind),
			Path:     d.Path,
			Enabled:  d.DefaultOn && d.Exists,
			Detected: true,
		})
	}
	return out
}

// RefreshDetectedRoots re-runs discovery, preserving the user's choices: an
// enabled or disabled state the user set is never overwritten, and folders they
// added by hand are left alone.
func (c *Config) RefreshDetectedRoots() {
	existing := make(map[string]Root, len(c.Roots))
	for _, r := range c.Roots {
		existing[strings.ToLower(r.Path)] = r
	}
	for _, found := range DetectRoots() {
		key := strings.ToLower(found.Path)
		if _, ok := existing[key]; ok {
			continue
		}
		c.Roots = append(c.Roots, found)
	}
}

// IgnoreConfig converts the stored settings into the ignore engine's config.
func (c *Config) IgnoreConfig() ignore.Config { return c.Settings().IgnoreConfig() }

// IgnoreConfig converts a settings snapshot into the ignore engine's config.
func (s Settings) IgnoreConfig() ignore.Config {
	categories := make([]ignore.Category, 0, len(s.Ignore.DisabledCategories))
	for _, name := range s.Ignore.DisabledCategories {
		categories = append(categories, ignore.Category(name))
	}
	return ignore.Config{
		DisabledCategories: categories,
		Exclude:            s.Ignore.Exclude,
		Include:            s.Ignore.Include,
		MaxFileSize:        s.Ignore.MaxFileSizeBytes,
		SkipHidden:         s.Ignore.SkipHidden,
	}
}

// pathsOverlap reports whether one path contains the other.
func pathsOverlap(a, b string) bool {
	a = normalizeForCompare(a)
	b = normalizeForCompare(b)
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

func normalizeForCompare(p string) string {
	p = strings.ReplaceAll(filepath.Clean(p), `\`, "/")
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		p = strings.ToLower(p)
	}
	return strings.TrimSuffix(p, "/")
}

// rootName derives a stable snapshot root name from a path.
func rootName(path string) string {
	base := filepath.Base(path)
	base = strings.ToLower(strings.TrimSpace(base))
	base = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r == ' ', r == '_', r == '-':
			return '-'
		default:
			return -1
		}
	}, base)
	base = strings.Trim(base, "-")
	if base == "" {
		return "folder"
	}
	return base
}

// DefaultDeviceName returns a sensible display name for this machine.
func DefaultDeviceName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return runtime.GOOS + " device"
	}
	return host
}
