// Package scanner walks the backed-up folders and decides what belongs in a
// snapshot.
//
// The walk is deliberately conservative: it never follows symlinks out of a
// root, never descends into protected system locations even if a root points at
// one, and treats an unreadable file as a reported skip rather than a failed
// backup. A backup agent that stops at the first permission error is a backup
// agent that never finishes.
package scanner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/foisalislambd/openbackup/internal/agent/config"
	"github.com/foisalislambd/openbackup/internal/api"
	"github.com/foisalislambd/openbackup/internal/ignore"
)

// Item is one entry found by the walk.
type Item struct {
	// AbsPath is the path on this machine.
	AbsPath string
	// SnapPath is the path inside the snapshot: "<root name>/<relative path>".
	SnapPath string
	Type     api.EntryType
	Size     int64
	ModTime  time.Time
	Mode     uint32
	// Inode is the file identity on this machine. Zero means unknown.
	// Together with size and mtime it lets a later scan skip the read.
	Inode uint64
	// LinkTarget is set for symlinks.
	LinkTarget string
}

// Skip records something that was deliberately left out.
type Skip struct {
	SnapPath string
	Reason   string
	Rule     string
	Category string
	IsDir    bool
	Bytes    int64
}

// Result summarises a walk.
type Result struct {
	Files   int64
	Dirs    int64
	Bytes   int64
	Skipped int64
	// SkippedBytes is how much data the ignore rules kept out of the backup,
	// which is the number that shows users the rules are earning their place.
	SkippedBytes int64
	// Samples holds a bounded sample of skips for the activity log.
	Samples  []Skip
	Errors   []string
	Projects []*ignore.Project
	Duration time.Duration
}

// maxSamples bounds how many skip explanations are collected per scan.
const maxSamples = 200

// maxErrors bounds reported walk errors.
const maxErrors = 50

// Scanner walks roots applying an ignore policy.
type Scanner struct {
	matcher *ignore.Matcher
	// Emit is called for every item that belongs in the backup.
	Emit func(Item) error
}

// New builds a Scanner from a settings snapshot. A snapshot rather than the live
// configuration keeps one walk consistent: the rules cannot change halfway
// through a scan and leave part of a folder judged by different criteria.
func New(set config.Settings) *Scanner {
	return NewWithMatcher(ignore.New(set.IgnoreConfig()))
}

// NewWithMatcher walks with an existing matcher so the watcher, the full scan
// and a delta all share one project registry and one set of .openbackupignore
// rules. A nil matcher gets an empty policy.
func NewWithMatcher(m *ignore.Matcher) *Scanner {
	if m == nil {
		m = ignore.New(ignore.Config{})
	}
	return &Scanner{matcher: m}
}

// Matcher exposes the compiled rules so the watcher can apply the same policy.
func (s *Scanner) Matcher() *ignore.Matcher { return s.matcher }

// Walk scans every enabled root.
func (s *Scanner) Walk(ctx context.Context, roots []config.Root) (Result, error) {
	start := time.Now()
	var result Result
	for _, root := range roots {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if err := s.walkRoot(ctx, root, &result); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return result, err
			}
			result.addError(fmt.Sprintf("%s: %v", root.Path, err))
		}
	}
	result.Projects = s.matcher.Projects()
	result.Duration = time.Since(start)
	return result, nil
}

func (s *Scanner) walkRoot(ctx context.Context, root config.Root, result *Result) error {
	absRoot, err := filepath.Abs(root.Path)
	if err != nil {
		return err
	}
	// Refuse a root that is itself a protected location: a user who types C:\ or
	// /usr should get a clear refusal, not a machine-wide read. Soft prefixes
	// such as /tmp only refuse the exact path, so TempDir-based tests work.
	if d := s.matcher.IsForbiddenRoot(absRoot); d.Skip {
		return fmt.Errorf("refusing to back up a protected system location (%s)", d.Reason)
	}

	return s.walkDir(ctx, root, absRoot, absRoot, "", result)
}

// walkDir lists dir once. That listing both feeds project detection and the
// walk, so an accepted directory is not enumerated twice, and a name-based
// exclusion does not stat the file.
func (s *Scanner) walkDir(ctx context.Context, root config.Root, absRoot, dir, rel string, result *Result) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		result.addError(fmt.Sprintf("%s: %v", dir, err))
		return nil
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	s.matcher.ObserveDirectory(rel, names, func() ([]byte, error) {
		return readIgnoreFile(filepath.Join(dir, ignore.IgnoreFileName))
	})

	for _, entry := range entries {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		name := entry.Name()
		abs := filepath.Join(dir, name)
		childRel := name
		if rel != "" {
			childRel = rel + "/" + name
		}
		snapPath := path.Join(root.Name, childRel)

		if d := s.matcher.SystemPathOutside(abs, absRoot); d.Skip {
			result.recordSkip(Skip{SnapPath: snapPath, Reason: d.Reason, Rule: d.Rule,
				Category: string(d.Category), IsDir: entry.IsDir()})
			continue
		}

		// Name rules do not need a stat. Size rules do, and they are applied
		// after Info below. Directory-only patterns are tested with isDir from
		// the directory entry, which on most filesystems does not stat either.
		if d := s.matcher.Match(childRel, entry.IsDir()); d.Skip {
			result.recordSkip(Skip{SnapPath: snapPath, Reason: d.Reason, Rule: d.Rule,
				Category: string(d.Category), IsDir: entry.IsDir()})
			continue
		}

		if entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				result.addError(fmt.Sprintf("%s: %v", abs, err))
				continue
			}
			result.Dirs++
			if err := s.emit(Item{
				AbsPath:  abs,
				SnapPath: snapPath,
				Type:     api.EntryDir,
				ModTime:  info.ModTime().UTC().Truncate(time.Second),
				Mode:     uint32(info.Mode().Perm()),
			}); err != nil {
				return err
			}
			if err := s.walkDir(ctx, root, absRoot, abs, childRel, result); err != nil {
				return err
			}
			continue
		}

		info, err := entry.Info()
		if err != nil {
			result.addError(fmt.Sprintf("%s: %v", abs, err))
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			// The name was already accepted. Readlink only happens for links
			// we are going to record.
			target, err := os.Readlink(abs)
			if err != nil {
				result.addError(fmt.Sprintf("%s: %v", abs, err))
				continue
			}
			if err := s.emit(Item{
				AbsPath:    abs,
				SnapPath:   snapPath,
				Type:       api.EntrySymlink,
				ModTime:    info.ModTime().UTC().Truncate(time.Second),
				LinkTarget: target,
			}); err != nil {
				return err
			}
			continue
		}
		if !info.Mode().IsRegular() {
			result.recordSkip(Skip{SnapPath: snapPath, Reason: "not a regular file", Rule: "special-file"})
			continue
		}
		if d := s.matcher.MatchFile(childRel, info.Size()); d.Skip {
			result.recordSkip(Skip{SnapPath: snapPath, Reason: d.Reason, Rule: d.Rule,
				Category: string(d.Category), Bytes: info.Size()})
			continue
		}
		result.Files++
		result.Bytes += info.Size()
		if err := s.emit(Item{
			AbsPath:  abs,
			SnapPath: snapPath,
			Type:     api.EntryFile,
			Size:     info.Size(),
			ModTime:  info.ModTime().UTC().Truncate(time.Second),
			Mode:     uint32(info.Mode().Perm()),
			Inode:    fileID(info),
		}); err != nil {
			return err
		}
	}
	return nil
}

func readIgnoreFile(p string) ([]byte, error) {
	info, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > ignoreMaxFile {
		return nil, errors.New("ignore file is not a small regular file")
	}
	return os.ReadFile(p)
}

const ignoreMaxFile = 256 << 10

func fileID(info os.FileInfo) uint64 { return fileIdent(info) }

// FileIdent is the filesystem identity used to notice a replaced file that
// kept the same size and timestamp. Zero means the platform did not report one.
func FileIdent(info os.FileInfo) uint64 { return fileIdent(info) }

func (s *Scanner) emit(item Item) error {
	if s.Emit == nil {
		return nil
	}
	return s.Emit(item)
}

// snapshotPath maps an absolute path to its path inside the snapshot.
func snapshotPath(root config.Root, absRoot, abs string) (string, bool) {
	rel, ok := ignore.RelPath(absRoot, abs)
	if !ok || rel == "." {
		return "", false
	}
	return path.Join(root.Name, rel), true
}

// SnapshotPath is the exported form used by the watcher, which sees absolute
// paths from the filesystem and must map them the same way the scanner does.
func SnapshotPath(root config.Root, abs string) (string, bool) {
	absRoot, err := filepath.Abs(root.Path)
	if err != nil {
		return "", false
	}
	return snapshotPath(root, absRoot, abs)
}

// recordSkip accounts a skip and keeps a bounded sample.
func (r *Result) recordSkip(skip Skip) {
	r.Skipped++
	r.SkippedBytes += skip.Bytes
	if len(r.Samples) < maxSamples {
		r.Samples = append(r.Samples, skip)
	}
}

func (r *Result) addError(msg string) {
	if len(r.Errors) < maxErrors {
		r.Errors = append(r.Errors, msg)
	}
}
