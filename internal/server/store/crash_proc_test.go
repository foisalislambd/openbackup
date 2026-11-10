package store

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/foisalislambd/openbackup/internal/api"
	"github.com/foisalislambd/openbackup/internal/hash"
)

func TestMain(m *testing.M) {
	if os.Getenv("OB_CRASH_CHILD") == "1" {
		if err := runCrashChild(); err != nil {
			_, _ = os.Stderr.WriteString(err.Error() + "\n")
			os.Exit(2)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runCrashChild() error {
	ctx := context.Background()
	dir := os.Getenv("OB_CRASH_DIR")
	switch os.Getenv("OB_CRASH_CASE") {
	case "put":
		blobs, err := NewFSBlobs(dir)
		if err != nil {
			return err
		}
		payload := bytes.Repeat([]byte("k"), 1000)
		_, err = blobs.Put(ctx, hash.Sum(payload), payload)
		return err
	case "delete":
		blobs, err := NewFSBlobs(dir)
		if err != nil {
			return err
		}
		a := bytes.Repeat([]byte("a"), 20_000)
		b := bytes.Repeat([]byte("b"), 20_000)
		if _, err := blobs.PutMany(ctx, []BlobPart{{Digest: hash.Sum(a), Data: a}, {Digest: hash.Sum(b), Data: b}}); err != nil {
			return err
		}
		return blobs.Delete(ctx, hash.Sum(a))
	case "snapshot":
		return crashSnapshot(ctx, dir)
	case "gc":
		return crashGC(ctx, dir)
	default:
		return os.ErrInvalid
	}
}

func crashSnapshot(ctx context.Context, dir string) error {
	db, blobs, user, dev, err := crashFixture(ctx, dir)
	if err != nil {
		return err
	}
	defer db.Close()
	payload := []byte("snapshot-bytes")
	digest := hash.Sum(payload)
	if _, err := blobs.Put(ctx, digest, payload); err != nil {
		return err
	}
	if err := db.RegisterChunks(ctx, user.ID, []ChunkRegistration{{
		Digest: digest, StoredBytes: int64(len(payload)), PlainBytes: int64(len(payload)),
	}}); err != nil {
		return err
	}
	snap, _, _, err := db.StartSnapshot(ctx, user.ID, dev.ID, api.StartSnapshotRequest{Kind: api.SnapshotFull})
	if err != nil {
		return err
	}
	if err := db.AddEntries(ctx, snap, []api.Entry{{
		Path: "notes.txt", Type: api.EntryFile, Size: int64(len(payload)), Chunks: []string{digest}, Digest: digest,
	}}, nil); err != nil {
		return err
	}
	return db.CompleteSnapshot(ctx, snap, api.CompleteSnapshotRequest{FileCount: 1})
}

func crashGC(ctx context.Context, dir string) error {
	db, blobs, user, dev, err := crashFixture(ctx, dir)
	if err != nil {
		return err
	}
	defer db.Close()
	keep := []byte("referenced-bytes")
	keepDigest := hash.Sum(keep)
	if _, err := blobs.Put(ctx, keepDigest, keep); err != nil {
		return err
	}
	if err := db.RegisterChunks(ctx, user.ID, []ChunkRegistration{{
		Digest: keepDigest, StoredBytes: int64(len(keep)), PlainBytes: int64(len(keep)),
	}}); err != nil {
		return err
	}
	snap, _, _, err := db.StartSnapshot(ctx, user.ID, dev.ID, api.StartSnapshotRequest{Kind: api.SnapshotFull})
	if err != nil {
		return err
	}
	if err := db.AddEntries(ctx, snap, []api.Entry{{
		Path: "keep.txt", Type: api.EntryFile, Size: int64(len(keep)), Chunks: []string{keepDigest}, Digest: keepDigest,
	}}, nil); err != nil {
		return err
	}
	if err := db.CompleteSnapshot(ctx, snap, api.CompleteSnapshotRequest{FileCount: 1}); err != nil {
		return err
	}
	drop := bytes.Repeat([]byte("u"), 1000)
	dropDigest := hash.Sum(drop)
	if _, err := blobs.Put(ctx, dropDigest, drop); err != nil {
		return err
	}
	if err := db.RegisterChunks(ctx, user.ID, []ChunkRegistration{{
		Digest: dropDigest, StoredBytes: int64(len(drop)), PlainBytes: int64(len(drop)),
	}}); err != nil {
		return err
	}
	old := time.Now().Add(-time.Hour).UnixMilli()
	if _, err := db.sql.ExecContext(ctx, `UPDATE chunks SET created_at = ? WHERE digest = ?`, old, dropDigest); err != nil {
		return err
	}
	_, err = db.ClaimUnreferencedChunk(ctx, dropDigest, func() error {
		return blobs.Delete(ctx, dropDigest)
	})
	return err
}

func crashFixture(ctx context.Context, dir string) (*DB, *FSBlobs, *User, *Device, error) {
	db, err := OpenDB(ctx, filepath.Join(dir, "openbackup.db"))
	if err != nil {
		return nil, nil, nil, nil, err
	}
	blobs, err := NewFSBlobs(dir)
	if err != nil {
		_ = db.Close()
		return nil, nil, nil, nil, err
	}
	user, err := db.CreateUser(ctx, "crash@example.com", "hash", true)
	if err != nil {
		_ = db.Close()
		return nil, nil, nil, nil, err
	}
	dev, err := db.CreateDevice(ctx, Device{UserID: user.ID, Name: "box"}, "token")
	if err != nil {
		_ = db.Close()
		return nil, nil, nil, nil, err
	}
	return db, blobs, user, dev, nil
}

func TestProcessKillDuringPackStages(t *testing.T) {
	payload := bytes.Repeat([]byte("k"), 1000)
	digest := hash.Sum(payload)

	t.Run("before pack publish", func(t *testing.T) {
		dir := t.TempDir()
		killChild(t, dir, "pack-publish", "put")
		blobs, err := NewFSBlobs(dir)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := blobs.Has(context.Background(), digest); ok {
			t.Fatal("kill before the pack rename left a blob visible")
		}
	})

	t.Run("after pack rename before index", func(t *testing.T) {
		dir := t.TempDir()
		killChild(t, dir, "idx-publish", "put")
		blobs, err := NewFSBlobs(dir)
		if err != nil {
			t.Fatal(err)
		}
		got, err := blobs.Get(context.Background(), digest)
		if err != nil || !bytes.Equal(got, payload) {
			t.Fatalf("restart after index kill: %v", err)
		}
	})
}

func TestProcessKillDuringDeletion(t *testing.T) {
	a := bytes.Repeat([]byte("a"), 20_000)
	b := bytes.Repeat([]byte("b"), 20_000)
	da, db := hash.Sum(a), hash.Sum(b)

	t.Run("before tombstone", func(t *testing.T) {
		dir := t.TempDir()
		killChild(t, dir, "tombstone-publish", "delete")
		blobs, err := NewFSBlobs(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			digest  string
			payload []byte
		}{{da, a}, {db, b}} {
			got, err := blobs.Get(context.Background(), tc.digest)
			if err != nil || !bytes.Equal(got, tc.payload) {
				t.Fatalf("%s after kill before tombstone: %v", tc.digest, err)
			}
		}
	})

	t.Run("after tombstone", func(t *testing.T) {
		dir := t.TempDir()
		killChild(t, dir, "after-tombstone", "delete")
		assertDeletionStuck(t, dir, da, db, b)
	})

	t.Run("during compaction", func(t *testing.T) {
		dir := t.TempDir()
		killChild(t, dir, "compact-publish", "delete")
		assertDeletionStuck(t, dir, da, db, b)
	})
}

func TestProcessKillDuringSnapshotComplete(t *testing.T) {
	dir := t.TempDir()
	killChild(t, dir, "snapshot-complete", "snapshot")
	db, err := OpenDB(context.Background(), filepath.Join(dir, "openbackup.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var status string
	if err := db.sql.QueryRow(`SELECT status FROM snapshots`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status == api.SnapshotStatusComplete {
		t.Fatal("kill before the status update left a completed snapshot")
	}
	blobs, err := NewFSBlobs(dir)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("snapshot-bytes")
	got, err := blobs.Get(context.Background(), hash.Sum(payload))
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("blob after killed completion: %v", err)
	}
}

func TestProcessKillDuringGC(t *testing.T) {
	dir := t.TempDir()
	killChild(t, dir, "gc-before-commit", "gc")
	db, err := OpenDB(context.Background(), filepath.Join(dir, "openbackup.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var status string
	if err := db.sql.QueryRow(`SELECT status FROM snapshots`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != api.SnapshotStatusComplete {
		t.Fatalf("referenced snapshot status %s", status)
	}
	blobs, err := NewFSBlobs(dir)
	if err != nil {
		t.Fatal(err)
	}
	keep := []byte("referenced-bytes")
	got, err := blobs.Get(context.Background(), hash.Sum(keep))
	if err != nil || !bytes.Equal(got, keep) {
		t.Fatalf("referenced blob after GC kill: %v", err)
	}
	drop := bytes.Repeat([]byte("u"), 1000)
	dropDigest := hash.Sum(drop)
	var refs int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM snapshot_chunks WHERE digest = ?`, dropDigest).Scan(&refs); err != nil {
		t.Fatal(err)
	}
	if refs != 0 {
		t.Fatal("a snapshot references the unacknowledged deletion")
	}
	known, err := db.IsChunkKnown(context.Background(), dropDigest)
	if err != nil {
		t.Fatal(err)
	}
	if !known {
		t.Fatal("kill before the GC commit acknowledged the deletion")
	}
}

func assertDeletionStuck(t *testing.T, dir, deleted, survivor string, payload []byte) {
	t.Helper()
	blobs, err := NewFSBlobs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := blobs.Has(context.Background(), deleted); ok {
		t.Fatal("durable tombstone resurrected the deleted blob")
	}
	got, err := blobs.Get(context.Background(), survivor)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("survivor: %v", err)
	}
}

func killChild(t *testing.T, dir, stage, scenario string) {
	t.Helper()
	signal := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(),
		"OB_CRASH_CHILD=1",
		"OB_CRASH_DIR="+dir,
		"OB_CRASH_AT="+stage,
		"OB_CRASH_SIGNAL="+signal,
		"OB_CRASH_CASE="+scenario,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(signal); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(signal); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("child never reached %s: %s", stage, stderr.String())
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
}
