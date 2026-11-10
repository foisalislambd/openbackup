package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/foisalislambd/openbackup/internal/api"
	"github.com/foisalislambd/openbackup/internal/hash"
)

func TestClaimDoesNotDeleteAReferencedChunk(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	user, err := db.CreateUser(ctx, "gc@example.com", "hash", true)
	if err != nil {
		t.Fatal(err)
	}
	dev, err := db.CreateDevice(ctx, Device{UserID: user.ID, Name: "box"}, "token")
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("referenced-chunk")
	digest := hash.Sum(payload)
	dir := t.TempDir()
	blobs, err := NewFSBlobs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := blobs.Put(ctx, digest, payload); err != nil {
		t.Fatal(err)
	}
	if err := db.RegisterChunks(ctx, user.ID, []ChunkRegistration{{
		Digest: digest, StoredBytes: int64(len(payload)), PlainBytes: int64(len(payload)),
	}}); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour).UnixMilli()
	if _, err := db.sql.ExecContext(ctx, `UPDATE chunks SET created_at = ? WHERE digest = ?`, old, digest); err != nil {
		t.Fatal(err)
	}
	listed, err := db.UnreferencedChunks(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].Digest != digest {
		t.Fatalf("candidate list: %+v", listed)
	}
	snap, _, _, err := db.StartSnapshot(ctx, user.ID, dev.ID, api.StartSnapshotRequest{Kind: api.SnapshotFull})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AddEntries(ctx, snap, []api.Entry{{
		Path: "notes.txt", Type: api.EntryFile, Size: int64(len(payload)), Chunks: []string{digest}, Digest: digest,
	}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.CompleteSnapshot(ctx, snap, api.CompleteSnapshotRequest{FileCount: 1}); err != nil {
		t.Fatal(err)
	}
	claimed, err := db.ClaimUnreferencedChunk(ctx, digest, func() error {
		return blobs.Delete(ctx, digest)
	})
	if err != nil {
		t.Fatal(err)
	}
	if claimed {
		t.Fatal("claim deleted a chunk a completed snapshot references")
	}
	got, err := blobs.Get(ctx, digest)
	if err != nil || string(got) != string(payload) {
		t.Fatalf("blob after refused claim: %v", err)
	}
	var status string
	if err := db.sql.QueryRowContext(ctx, `SELECT status FROM snapshots WHERE id = ?`, snap).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != api.SnapshotStatusComplete {
		t.Fatalf("status %s", status)
	}
}

func TestRunningSnapshotProtectsItsChunks(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	user, err := db.CreateUser(ctx, "run@example.com", "hash", true)
	if err != nil {
		t.Fatal(err)
	}
	dev, err := db.CreateDevice(ctx, Device{UserID: user.ID, Name: "box"}, "token")
	if err != nil {
		t.Fatal(err)
	}
	digest := hash.Sum([]byte("in-flight"))
	if err := db.RegisterChunks(ctx, user.ID, []ChunkRegistration{{
		Digest: digest, StoredBytes: 8, PlainBytes: 8,
	}}); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-20 * time.Minute).UnixMilli()
	snap, _, _, err := db.StartSnapshot(ctx, user.ID, dev.ID, api.StartSnapshotRequest{Kind: api.SnapshotFull})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.sql.ExecContext(ctx, `UPDATE chunks SET created_at = ? WHERE digest = ?`, when, digest); err != nil {
		t.Fatal(err)
	}
	if _, err := db.sql.ExecContext(ctx, `UPDATE snapshots SET started_at = ? WHERE id = ?`, when, snap); err != nil {
		t.Fatal(err)
	}
	listed, err := db.UnreferencedChunks(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 0 {
		t.Fatalf("running snapshot did not protect the chunk: %+v", listed)
	}
	claimed, err := db.ClaimUnreferencedChunk(ctx, digest, func() error {
		t.Fatal("blob delete ran for a protected chunk")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if claimed {
		t.Fatal("claimed a chunk belonging to a running snapshot")
	}
}

func TestClaimHoldsTheWriteLockAcrossBlobDelete(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	user, err := db.CreateUser(ctx, "lock@example.com", "hash", true)
	if err != nil {
		t.Fatal(err)
	}
	dev, err := db.CreateDevice(ctx, Device{UserID: user.ID, Name: "box"}, "token")
	if err != nil {
		t.Fatal(err)
	}
	digest := hash.Sum([]byte("race"))
	if err := db.RegisterChunks(ctx, user.ID, []ChunkRegistration{{
		Digest: digest, StoredBytes: 4, PlainBytes: 4,
	}}); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour).UnixMilli()
	if _, err := db.sql.ExecContext(ctx, `UPDATE chunks SET created_at = ? WHERE digest = ?`, old, digest); err != nil {
		t.Fatal(err)
	}
	snap, _, _, err := db.StartSnapshot(ctx, user.ID, dev.ID, api.StartSnapshotRequest{Kind: api.SnapshotFull})
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	claimDone := make(chan error, 1)
	go func() {
		_, err := db.ClaimUnreferencedChunk(ctx, digest, func() error {
			close(started)
			<-release
			return nil
		})
		claimDone <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("claim did not reach the blob delete")
	}
	addDone := make(chan error, 1)
	go func() {
		addDone <- db.AddEntries(ctx, snap, []api.Entry{{
			Path: "a.txt", Type: api.EntryFile, Chunks: []string{digest},
		}}, nil)
	}()
	select {
	case err := <-addDone:
		t.Fatalf("AddEntries finished while the claim still held the write lock: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	if err := <-claimDone; err != nil {
		t.Fatal(err)
	}
	if err := <-addDone; err != nil {
		t.Fatal(err)
	}
	// The claim committed before AddEntries could insert the reference, so
	// completion must refuse the snapshot instead of marking it complete.
	err = db.CompleteSnapshot(ctx, snap, api.CompleteSnapshotRequest{FileCount: 1})
	if err == nil {
		t.Fatal("completion succeeded after the chunk row was claimed")
	}
	var status string
	if err := db.sql.QueryRowContext(ctx, `SELECT status FROM snapshots WHERE id = ?`, snap).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != api.SnapshotStatusFailed {
		t.Fatalf("snapshot status %s, want failed", status)
	}
}

func TestClaimRollsBackWhenBlobDeleteFails(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	user, err := db.CreateUser(ctx, "rollback@example.com", "hash", true)
	if err != nil {
		t.Fatal(err)
	}
	digest := hash.Sum([]byte("keep-row"))
	if err := db.RegisterChunks(ctx, user.ID, []ChunkRegistration{{
		Digest: digest, StoredBytes: 4, PlainBytes: 4,
	}}); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour).UnixMilli()
	if _, err := db.sql.ExecContext(ctx, `UPDATE chunks SET created_at = ? WHERE digest = ?`, old, digest); err != nil {
		t.Fatal(err)
	}
	claimed, err := db.ClaimUnreferencedChunk(ctx, digest, func() error {
		return errors.New("disk full")
	})
	if err == nil || claimed {
		t.Fatalf("claimed=%v err=%v", claimed, err)
	}
	known, err := db.IsChunkKnown(ctx, digest)
	if err != nil || !known {
		t.Fatalf("row after failed delete: known=%v err=%v", known, err)
	}
}

func TestSQLiteSyncFull(t *testing.T) {
	t.Setenv("OPENBACKUP_SQLITE_SYNC", "FULL")
	db := openTestDB(t)
	var mode int
	if err := db.sql.QueryRow(`PRAGMA synchronous`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != 2 {
		t.Fatalf("synchronous mode %d, want FULL (2)", mode)
	}
}

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := OpenDB(context.Background(), t.TempDir()+"/openbackup.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
