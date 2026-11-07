package engine

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/foisalislambd/openbackup/internal/agent/config"
	"github.com/foisalislambd/openbackup/internal/agent/index"
	"github.com/foisalislambd/openbackup/internal/api"
)

// seedIndex fills an empty local cache from the newest complete full snapshot.
// A wiped cache would otherwise re-read every file. The rows are stored at
// generation 0 so a file that no longer exists is still reported deleted by
// the scan that follows. Inode is left at 0: the snapshot does not carry it,
// and a zero inode does not force a re-read when size and mtime match.
func (e *Engine) seedIndex(ctx context.Context, roots []config.Root) error {
	has, err := e.idx.HasFiles(ctx)
	if err != nil || has {
		return err
	}
	snaps, err := e.client.ListSnapshots(ctx)
	if err != nil {
		return err
	}
	byID := make(map[string]api.Snapshot, len(snaps))
	for _, snap := range snaps {
		byID[snap.ID] = snap
	}
	full, ok := newestFull(snaps, byID)
	if !ok {
		return nil
	}

	cursor := ""
	batch := make([]index.Change, 0, 500)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := e.idx.Apply(ctx, batch); err != nil {
			return err
		}
		batch = batch[:0]
		return nil
	}
	for {
		entries, next, err := e.client.SnapshotEntries(ctx, full.ID, api.EntryQuery{Cursor: cursor, Limit: 2000})
		if err != nil {
			return err
		}
		for _, ent := range entries {
			if ent.Type != api.EntryFile || len(ent.Chunks) == 0 {
				continue
			}
			abs, ok := absFromSnapshot(roots, ent.Path)
			if !ok {
				continue
			}
			batch = append(batch, index.Change{
				Put:        true,
				Generation: 0,
				State: index.FileState{
					Path:    abs,
					Size:    ent.Size,
					ModTime: ent.ModTime.UTC().Truncate(time.Second),
					Digest:  ent.Digest,
					Chunks:  ent.Chunks,
					Mode:    ent.Mode,
				},
			})
			if len(batch) >= 500 {
				if err := flush(); err != nil {
					return err
				}
			}
		}
		if next == "" {
			break
		}
		cursor = next
	}
	if err := flush(); err != nil {
		return err
	}
	e.log.Info("reused the previous snapshot so unchanged files are not re-read", "snapshot", full.ID)
	return nil
}

func newestFull(snaps []api.Snapshot, byID map[string]api.Snapshot) (api.Snapshot, bool) {
	for _, snap := range snaps {
		if snap.Status != api.SnapshotStatusComplete || snap.Error != "" {
			continue
		}
		cur := snap
		seen := map[string]bool{}
		for {
			if cur.Kind == api.SnapshotFull || cur.ParentID == "" {
				if cur.Kind == api.SnapshotFull && cur.Status == api.SnapshotStatusComplete && cur.Error == "" {
					return cur, true
				}
				break
			}
			if seen[cur.ID] {
				break
			}
			seen[cur.ID] = true
			parent, ok := byID[cur.ParentID]
			if !ok || parent.Status != api.SnapshotStatusComplete {
				break
			}
			cur = parent
		}
	}
	return api.Snapshot{}, false
}

func absFromSnapshot(roots []config.Root, snapPath string) (string, bool) {
	for _, root := range roots {
		prefix := root.Name + "/"
		if !strings.HasPrefix(snapPath, prefix) {
			continue
		}
		rel := snapPath[len(prefix):]
		if rel == "" || strings.Contains(rel, "\x00") {
			continue
		}
		absRoot, err := filepath.Abs(root.Path)
		if err != nil {
			continue
		}
		return filepath.Join(absRoot, filepath.FromSlash(rel)), true
	}
	return "", false
}
