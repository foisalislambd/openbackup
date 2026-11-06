package maintenance

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/foisalislambd/openbackup/internal/server/store"
)

func TestApplyRetentionHonoursDailyCount(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.OpenDB(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	blobs, err := store.NewFSBlobs(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatal(err)
	}

	user, err := db.CreateUser(ctx, "owner@example.com", "not-a-real-hash", true)
	if err != nil {
		t.Fatal(err)
	}
	user.Policy.RetentionDays = 30
	user.Policy.KeepDaily = 1
	if err := db.UpdateUserPolicy(ctx, user.ID, user.Policy); err != nil {
		t.Fatal(err)
	}
	device, err := db.CreateDevice(ctx, store.Device{
		UserID: user.ID, Name: "laptop", Platform: "linux",
	}, "token-hash")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	for i, id := range []string{"snap-new", "snap-yday", "snap-old"} {
		at := now.Add(-time.Duration(i) * 26 * time.Hour)
		if _, err := db.SQL().ExecContext(ctx,
			`INSERT INTO snapshots (id, user_id, device_id, kind, status, started_at, completed_at, file_count, total_bytes)
			 VALUES (?, ?, ?, 'full', 'complete', ?, ?, 1, 10)`,
			id, user.ID, device.ID, at.UnixMilli(), at.UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := New(db, blobs, slog.New(slog.DiscardHandler)).Run(ctx); err != nil {
		t.Fatal(err)
	}
	left, err := db.ListSnapshots(ctx, user.ID, device.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 || left[0].ID != "snap-new" {
		ids := make([]string, len(left))
		for i, s := range left {
			ids[i] = s.ID
		}
		t.Fatalf("daily retention kept %v, want only snap-new", ids)
	}
}
