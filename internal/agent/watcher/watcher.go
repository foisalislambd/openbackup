// Package watcher turns filesystem events into a debounced set of paths to back
// up.
//
// Real-time watching is what replaces periodic full-disk scanning: a saved
// document is backed up seconds later, and an idle machine does no work at all.
// The two hard parts are handled here: recursive watch registration (fsnotify is
// per-directory on Linux and Windows), and debouncing, because a single "save"
// in most applications produces a burst of create, write, rename and delete
// events for temporary files.
package watcher

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/openbackup/openbackup/internal/agent/config"
	"github.com/openbackup/openbackup/internal/ignore"
)

// Change is a debounced filesystem change.
type Change struct {
	// AbsPath is the affected path.
	AbsPath string
	// Root is the backup root it belongs to.
	Root config.Root
	// Removed reports that the path no longer exists.
	Removed bool
	// IsDir reports whether the path was a directory, when known.
	IsDir bool
}

// Watcher watches the backup roots.
type Watcher struct {
	fsw     *fsnotify.Watcher
	matcher *ignore.Matcher
	log     *slog.Logger

	debounce time.Duration

	mu      sync.Mutex
	pending map[string]Change
	roots   []config.Root
	// watched is the set of registered directories, for diagnostics, to enforce a
	// sane ceiling, and so re-registering a folder is free rather than a leak.
	watched map[string]bool

	changes chan []Change
	// overflow signals that the watch could not keep up and a full rescan is
	// needed. Pretending otherwise would silently miss changes.
	overflow chan struct{}
}

// maxWatchedDirs caps watch registrations.
//
// Every watched directory costs a kernel handle (an inotify watch on Linux, a
// directory handle on Windows). A developer's home directory can contain
// hundreds of thousands of directories, and exhausting the limit would break
// watching entirely, so past this ceiling the agent falls back to periodic
// scanning and says so.
const maxWatchedDirs = 20000

// New creates a watcher.
func New(matcher *ignore.Matcher, debounce time.Duration, log *slog.Logger) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if debounce <= 0 {
		debounce = 15 * time.Second
	}
	if log == nil {
		log = slog.Default()
	}
	return &Watcher{
		fsw:      fsw,
		matcher:  matcher,
		log:      log,
		debounce: debounce,
		pending:  make(map[string]Change),
		watched:  make(map[string]bool),
		changes:  make(chan []Change, 8),
		overflow: make(chan struct{}, 1),
	}, nil
}

// Close stops watching.
func (w *Watcher) Close() error { return w.fsw.Close() }

// Changes yields debounced batches of changes.
func (w *Watcher) Changes() <-chan []Change { return w.changes }

// Overflow signals that a full rescan is required.
func (w *Watcher) Overflow() <-chan struct{} { return w.overflow }

// WatchedDirs reports how many directories are registered.
func (w *Watcher) WatchedDirs() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.watched)
}

// SetMatcher replaces the ignore rules. It is called when the user changes what
// to exclude, so the watcher stops waking up for files that are no longer backed
// up.
func (w *Watcher) SetMatcher(m *ignore.Matcher) {
	if m == nil {
		return
	}
	w.mu.Lock()
	w.matcher = m
	w.mu.Unlock()
}

// rules returns the current ignore rules.
func (w *Watcher) rules() *ignore.Matcher {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.matcher
}

// SetRoots makes the live watch match this exact set of roots, adding folders
// that are new and releasing the kernel handles for folders that are gone.
//
// Dropping the old watches matters: without it, removing a folder in the app
// would keep costing watch handles for the life of the process, and events from
// an unconfigured folder would still wake the agent up.
func (w *Watcher) SetRoots(ctx context.Context, roots []config.Root) error {
	for _, dir := range w.watchList() {
		if !underAnyRoot(dir, roots) {
			w.removeDir(dir)
		}
	}
	return w.AddRoots(ctx, roots)
}

func (w *Watcher) watchList() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]string, 0, len(w.watched))
	for dir := range w.watched {
		out = append(out, dir)
	}
	return out
}

func (w *Watcher) removeDir(abs string) {
	// fsnotify already dropped the watch for a directory that was deleted, so an
	// error here is expected and not worth reporting.
	_ = w.fsw.Remove(abs)
	w.mu.Lock()
	delete(w.watched, abs)
	w.mu.Unlock()
}

// underAnyRoot reports whether a directory belongs to one of the roots.
func underAnyRoot(dir string, roots []config.Root) bool {
	for _, root := range roots {
		abs, err := filepath.Abs(root.Path)
		if err != nil {
			continue
		}
		if _, ok := ignore.RelPath(abs, dir); ok {
			return true
		}
		if samePath(abs, dir) {
			return true
		}
	}
	return false
}

func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// AddRoots registers every directory under each root, skipping anything the
// ignore rules exclude. Watching node_modules would be both pointless and the
// fastest way to run out of kernel watches.
func (w *Watcher) AddRoots(ctx context.Context, roots []config.Root) error {
	w.mu.Lock()
	w.roots = append([]config.Root(nil), roots...)
	w.mu.Unlock()

	matcher := w.rules()
	for _, root := range roots {
		absRoot, err := filepath.Abs(root.Path)
		if err != nil {
			continue
		}
		err = filepath.WalkDir(absRoot, func(abs string, entry fs.DirEntry, walkErr error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if walkErr != nil {
				if entry != nil && entry.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if !entry.IsDir() {
				return nil
			}
			if d := matcher.IsSystemPath(abs); d.Skip {
				return fs.SkipDir
			}
			if abs != absRoot {
				rel, ok := ignore.RelPath(absRoot, abs)
				if !ok {
					return fs.SkipDir
				}
				if d := matcher.Match(rel, true); d.Skip {
					return fs.SkipDir
				}
			}
			return w.addDir(abs)
		})
		if err != nil && !errors.Is(err, context.Canceled) {
			w.log.Debug("register watches", "root", root.Path, "error", err)
		}
	}
	w.log.Info("watching for changes", "directories", w.WatchedDirs())
	return nil
}

// addDir registers one directory.
func (w *Watcher) addDir(abs string) error {
	w.mu.Lock()
	already := w.watched[abs]
	full := len(w.watched) >= maxWatchedDirs
	w.mu.Unlock()
	if already {
		return nil
	}
	if full {
		return fs.SkipDir
	}

	if err := w.fsw.Add(abs); err != nil {
		// A directory that vanished between the walk and the registration is
		// normal, not an error worth surfacing.
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		w.log.Debug("watch directory", "path", abs, "error", err)
		return nil
	}
	w.mu.Lock()
	w.watched[abs] = true
	w.mu.Unlock()
	return nil
}

// Run processes events until ctx is cancelled.
func (w *Watcher) Run(ctx context.Context) {
	// The timer fires once a burst of events has gone quiet.
	timer := time.NewTimer(w.debounce)
	if !timer.Stop() {
		<-timer.C
	}
	timerRunning := false

	for {
		select {
		case <-ctx.Done():
			return

		case event, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			if w.handleEvent(ctx, event) && !timerRunning {
				timer.Reset(w.debounce)
				timerRunning = true
			}

		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			// A kernel event queue overflow means events were dropped, so the
			// only honest response is to ask for a full rescan.
			w.log.Warn("watcher error; scheduling a full rescan", "error", err)
			select {
			case w.overflow <- struct{}{}:
			default:
			}

		case <-timer.C:
			timerRunning = false
			batch := w.drain()
			if len(batch) == 0 {
				continue
			}
			select {
			case w.changes <- batch:
			case <-ctx.Done():
				return
			default:
				// The engine is busy with a previous batch. Put the changes back
				// and try again after another debounce interval rather than
				// dropping them.
				w.restore(batch)
				timer.Reset(w.debounce)
				timerRunning = true
			}
		}
	}
}

// handleEvent records an event, reporting whether it is worth a backup pass.
func (w *Watcher) handleEvent(ctx context.Context, event fsnotify.Event) bool {
	// Chmod alone changes no content; ignoring it avoids waking up for metadata
	// churn from indexers and antivirus scanners.
	if event.Op == fsnotify.Chmod {
		return false
	}

	root, ok := w.rootFor(event.Name)
	if !ok {
		return false
	}
	absRoot, err := filepath.Abs(root.Path)
	if err != nil {
		return false
	}
	matcher := w.rules()
	if d := matcher.IsSystemPath(event.Name); d.Skip {
		return false
	}
	rel, ok := ignore.RelPath(absRoot, event.Name)
	if !ok {
		return false
	}

	removed := event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename)
	isDir := false
	if info, err := os.Lstat(event.Name); err == nil {
		isDir = info.IsDir()
		removed = false
	}

	// Ignore rules apply to watcher events exactly as they do to a scan, so a
	// build that writes ten thousand files into dist/ causes no work.
	if d := matcher.Match(rel, isDir); d.Skip {
		return false
	}

	if isDir && event.Has(fsnotify.Create) {
		// A new directory needs its own watch, and its existing contents need to
		// be picked up: the create event for the directory is all we get.
		_ = w.addNewTree(ctx, root, event.Name)
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending[event.Name] = Change{AbsPath: event.Name, Root: root, Removed: removed, IsDir: isDir}
	// A pathological event storm (a build in a watched folder, or a sync client)
	// must not grow this map without bound.
	if len(w.pending) > 50000 {
		w.pending = make(map[string]Change)
		select {
		case w.overflow <- struct{}{}:
		default:
		}
		return false
	}
	return true
}

// addNewTree registers watches for a newly created directory and queues its
// contents.
func (w *Watcher) addNewTree(ctx context.Context, root config.Root, dir string) error {
	absRoot, err := filepath.Abs(root.Path)
	if err != nil {
		return err
	}
	return filepath.WalkDir(dir, func(abs string, entry fs.DirEntry, walkErr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if walkErr != nil {
			return nil
		}
		rel, ok := ignore.RelPath(absRoot, abs)
		if !ok {
			return nil
		}
		if d := w.rules().Match(rel, entry.IsDir()); d.Skip {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return w.addDir(abs)
		}
		w.mu.Lock()
		w.pending[abs] = Change{AbsPath: abs, Root: root}
		w.mu.Unlock()
		return nil
	})
}

// rootFor finds the backup root containing a path.
func (w *Watcher) rootFor(abs string) (config.Root, bool) {
	w.mu.Lock()
	roots := w.roots
	w.mu.Unlock()

	best := config.Root{}
	found := false
	for _, root := range roots {
		absRoot, err := filepath.Abs(root.Path)
		if err != nil {
			continue
		}
		if _, ok := ignore.RelPath(absRoot, abs); !ok {
			continue
		}
		// Prefer the most specific root when they nest.
		if !found || len(absRoot) > len(best.Path) {
			best = config.Root{Name: root.Name, Path: absRoot, Enabled: root.Enabled, Detected: root.Detected}
			found = true
		}
	}
	return best, found
}

// drain takes the pending changes.
func (w *Watcher) drain() []Change {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pending) == 0 {
		return nil
	}
	batch := make([]Change, 0, len(w.pending))
	for _, c := range w.pending {
		batch = append(batch, c)
	}
	w.pending = make(map[string]Change)
	return batch
}

// restore puts a batch back when the engine could not accept it.
func (w *Watcher) restore(batch []Change) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, c := range batch {
		if _, exists := w.pending[c.AbsPath]; !exists {
			w.pending[c.AbsPath] = c
		}
	}
}

// AtWatchLimit reports whether the watch ceiling was reached, in which case the
// agent must keep scanning periodically to stay correct.
func (w *Watcher) AtWatchLimit() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.watched) >= maxWatchedDirs
}

// Describe returns a short status string for the CLI.
func (w *Watcher) Describe() string {
	dirs := w.WatchedDirs()
	if w.AtWatchLimit() {
		return strings.Join([]string{
			"watching", strconv.Itoa(dirs), "directories (limit reached; relying on periodic scans for the rest)",
		}, " ")
	}
	return "watching " + strconv.Itoa(dirs) + " directories"
}
