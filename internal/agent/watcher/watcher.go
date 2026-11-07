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
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/foisalislambd/openbackup/internal/agent/config"
	"github.com/foisalislambd/openbackup/internal/ignore"
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
	w.dropIgnoredWatches()
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
		if err := w.watchTree(ctx, matcher, root, absRoot, absRoot, "", false); err != nil && !errors.Is(err, context.Canceled) {
			w.log.Debug("register watches", "root", root.Path, "error", err)
		}
	}
	w.dropIgnoredWatches()
	w.log.Info("watching for changes", "directories", w.WatchedDirs())
	return nil
}

// watchTree registers dir and its children. The directory is listed once:
// project markers and .openbackupignore are applied before any child watch is
// added, so a build directory next to package.json is never watched.
func (w *Watcher) watchTree(ctx context.Context, matcher *ignore.Matcher, root config.Root, absRoot, dir, rel string, queueFiles bool) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	matcher.ObserveDirectory(rel, names, func() ([]byte, error) {
		return readRulesFile(filepath.Join(dir, ignore.IgnoreFileName))
	})
	if d := matcher.SystemPathOutside(dir, absRoot); d.Skip && dir != absRoot {
		return nil
	}
	if rel != "" {
		if d := matcher.Match(rel, true); d.Skip {
			return nil
		}
	}
	if err := w.addDir(dir); err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		childRel := name
		if rel != "" {
			childRel = rel + "/" + name
		}
		abs := filepath.Join(dir, name)
		if entry.IsDir() {
			if err := w.watchTree(ctx, matcher, root, absRoot, abs, childRel, queueFiles); err != nil {
				return err
			}
			continue
		}
		if !queueFiles {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.Mode().IsRegular() && matcher.MatchFile(childRel, info.Size()).Skip {
			continue
		}
		if matcher.Match(childRel, false).Skip {
			continue
		}
		w.mu.Lock()
		w.pending[abs] = Change{AbsPath: abs, Root: root}
		w.mu.Unlock()
	}
	return nil
}

func readRulesFile(p string) ([]byte, error) {
	info, err := os.Lstat(p)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 256<<10 {
		return nil, err
	}
	return os.ReadFile(p)
}

// dropIgnoredWatches releases kernel watches whose directory is now excluded.
// SetRoots only drops watches that left the configured roots; an exclusion
// added inside a root has to be reconciled here.
func (w *Watcher) dropIgnoredWatches() {
	matcher := w.rules()
	for _, dir := range w.watchList() {
		root, ok := w.rootFor(dir)
		if !ok {
			w.removeDir(dir)
			continue
		}
		absRoot, err := filepath.Abs(root.Path)
		if err != nil {
			continue
		}
		if samePath(absRoot, dir) {
			continue
		}
		rel, ok := ignore.RelPath(absRoot, dir)
		if !ok || rel == "." {
			w.removeDir(dir)
			continue
		}
		if d := matcher.SystemPathOutside(dir, absRoot); d.Skip {
			w.removeDir(dir)
			continue
		}
		if d := matcher.Match(rel, true); d.Skip {
			w.removeDir(dir)
		}
	}
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
	// stretchUntil caps how long continuous churn may postpone a batch, so an
	// endless stream of changes (a build, a sync client) still gets backed up.
	var stretchUntil time.Time

	for {
		select {
		case <-ctx.Done():
			return

		case event, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			if !w.handleEvent(ctx, event) {
				continue
			}
			// Reset on every accepted event, so the batch measures quiet time
			// from the LAST change rather than the first — otherwise a file
			// being saved repeatedly uploads mid-write. The total delay is
			// capped so constant activity cannot starve backups entirely.
			now := time.Now()
			if !timerRunning {
				timer = resetTimer(timer, w.debounce)
				stretchUntil = now.Add(10 * w.debounce)
				timerRunning = true
			} else if now.Add(w.debounce).Before(stretchUntil) {
				timer = resetTimer(timer, w.debounce)
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

// resetTimer stops a timer, drains a pending fire if there is one, and starts
// it again. Returning the timer keeps this a single expression at call sites.
func resetTimer(t *time.Timer, d time.Duration) *time.Timer {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
	return t
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
	if d := matcher.SystemPathOutside(event.Name, absRoot); d.Skip {
		return false
	}
	rel, ok := ignore.RelPath(absRoot, event.Name)
	if !ok {
		return false
	}

	// A name rule can be decided before the stat. Directory-only patterns and
	// the size limit still need the file info, which is why oversized files
	// are rejected here, before they enter the debounce queue.
	if d := matcher.Match(rel, false); d.Skip {
		return false
	}

	removed := event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename)
	info, statErr := os.Lstat(event.Name)
	isDir := false
	if statErr != nil {
		// The path is gone. A directory-only rule does not match when isDir is
		// false, so test both shapes before queueing a deletion of an ignored
		// directory.
		if matcher.Match(rel, true).Skip {
			return false
		}
	} else {
		removed = false
		isDir = info.IsDir()
		if info.Mode().IsRegular() {
			if matcher.MatchFile(rel, info.Size()).Skip {
				return false
			}
		} else if matcher.Match(rel, isDir).Skip {
			return false
		}
	}

	if isDir && statErr == nil && event.Has(fsnotify.Create) {
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
	rel, ok := ignore.RelPath(absRoot, dir)
	if !ok {
		return err
	}
	if rel == "." {
		rel = ""
	}
	return w.watchTree(ctx, w.rules(), root, absRoot, dir, rel, true)
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
