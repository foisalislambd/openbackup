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
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
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
	return &Scanner{matcher: ignore.New(set.IgnoreConfig())}
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
	// / should get a clear refusal, not a machine-wide read.
	if d := s.matcher.IsSystemPath(absRoot); d.Skip {
		return fmt.Errorf("refusing to back up a protected system location (%s)", d.Reason)
	}

	return filepath.WalkDir(absRoot, func(abs string, entry fs.DirEntry, walkErr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if walkErr != nil {
			// Unreadable directories are common (locked system folders, files
			// held open by another process). Report and continue.
			result.addError(fmt.Sprintf("%s: %v", abs, walkErr))
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if abs == absRoot {
			if entry.IsDir() {
				s.detectProject(root, absRoot, abs)
			}
			return nil
		}

		snapPath, ok := snapshotPath(root, absRoot, abs)
		if !ok {
			return nil
		}
		relPath := strings.TrimPrefix(snapPath, root.Name+"/")

		// Protected locations win over everything, including user includes: a
		// junction or symlink pointing into C:\Windows must not drag the OS in.
		if d := s.matcher.IsSystemPath(abs); d.Skip {
			result.recordSkip(Skip{SnapPath: snapPath, Reason: d.Reason, Rule: d.Rule,
				Category: string(d.Category), IsDir: entry.IsDir()})
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		if entry.IsDir() {
			if d := s.matcher.Match(relPath, true); d.Skip {
				result.recordSkip(Skip{SnapPath: snapPath, Reason: d.Reason, Rule: d.Rule,
					Category: string(d.Category), IsDir: true})
				return fs.SkipDir
			}
			// Register a project before descending, so its build directories are
			// excluded on the way down rather than after they are walked.
			s.detectProject(root, absRoot, abs)
			info, err := entry.Info()
			if err != nil {
				result.addError(fmt.Sprintf("%s: %v", abs, err))
				return nil
			}
			result.Dirs++
			return s.emit(Item{
				AbsPath:  abs,
				SnapPath: snapPath,
				Type:     api.EntryDir,
				ModTime:  info.ModTime().UTC().Truncate(time.Second),
				Mode:     uint32(info.Mode().Perm()),
			})
		}

		info, err := entry.Info()
		if err != nil {
			result.addError(fmt.Sprintf("%s: %v", abs, err))
			return nil
		}

		// Symlinks are recorded, never followed. Following them would duplicate
		// data, risk infinite loops, and can reach outside the backup root.
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(abs)
			if err != nil {
				result.addError(fmt.Sprintf("%s: %v", abs, err))
				return nil
			}
			if d := s.matcher.Match(relPath, false); d.Skip {
				result.recordSkip(Skip{SnapPath: snapPath, Reason: d.Reason, Rule: d.Rule,
					Category: string(d.Category)})
				return nil
			}
			return s.emit(Item{
				AbsPath:    abs,
				SnapPath:   snapPath,
				Type:       api.EntrySymlink,
				ModTime:    info.ModTime().UTC().Truncate(time.Second),
				LinkTarget: target,
			})
		}

		// Sockets, devices, pipes and other special files have no meaning once
		// restored elsewhere.
		if !info.Mode().IsRegular() {
			result.recordSkip(Skip{SnapPath: snapPath, Reason: "not a regular file", Rule: "special-file"})
			return nil
		}

		if d := s.matcher.MatchFile(relPath, info.Size()); d.Skip {
			result.recordSkip(Skip{SnapPath: snapPath, Reason: d.Reason, Rule: d.Rule,
				Category: string(d.Category), Bytes: info.Size()})
			return nil
		}

		result.Files++
		result.Bytes += info.Size()
		return s.emit(Item{
			AbsPath:  abs,
			SnapPath: snapPath,
			Type:     api.EntryFile,
			Size:     info.Size(),
			ModTime:  info.ModTime().UTC().Truncate(time.Second),
			Mode:     uint32(info.Mode().Perm()),
		})
	})
}

func (s *Scanner) emit(item Item) error {
	if s.Emit == nil {
		return nil
	}
	return s.Emit(item)
}

// detectProject looks for project markers in a directory and registers the
// resulting exclusions.
func (s *Scanner) detectProject(root config.Root, absRoot, dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	rel, ok := ignore.RelPath(absRoot, dir)
	if !ok {
		return
	}
	if rel == "." {
		rel = ""
	}
	if project := ignore.DetectProject(rel, names); project != nil {
		s.matcher.RegisterProject(project)
	}
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

// TopSkipReasons summarises the sampled skips for a human-readable log line.
func (r *Result) TopSkipReasons(limit int) []string {
	counts := map[string]int{}
	for _, s := range r.Samples {
		counts[s.Reason]++
	}
	reasons := make([]string, 0, len(counts))
	for reason := range counts {
		reasons = append(reasons, reason)
	}
	sort.Slice(reasons, func(i, j int) bool {
		if counts[reasons[i]] != counts[reasons[j]] {
			return counts[reasons[i]] > counts[reasons[j]]
		}
		return reasons[i] < reasons[j]
	})
	if limit > 0 && len(reasons) > limit {
		reasons = reasons[:limit]
	}
	return reasons
}
