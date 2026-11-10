package httpapi_test

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/foisalislambd/openbackup/internal/api"
	"github.com/foisalislambd/openbackup/internal/hash"
	"github.com/foisalislambd/openbackup/internal/server/config"
	"github.com/foisalislambd/openbackup/internal/server/maintenance"
	"github.com/foisalislambd/openbackup/internal/server/store"
)

// TestS3RetentionRestoreAndDelete runs against a local S3-compatible server.
// CI points it at Adobe S3 mock. It does not contact AWS. The default test
// run skips it; OB_S3_REQUIRED=1 makes a missing endpoint a failure.
func TestS3RetentionRestoreAndDelete(t *testing.T) {
	endpoint := os.Getenv("OB_S3_ENDPOINT")
	if endpoint == "" {
		if os.Getenv("OB_S3_REQUIRED") == "1" {
			t.Fatal("OB_S3_ENDPOINT is required in this job")
		}
		t.Skip("set OB_S3_ENDPOINT to a local S3-compatible server; this test does not use AWS")
	}
	ctx := context.Background()
	prefix := "retention-" + time.Now().UTC().Format("20060102T150405.000000000")
	blobs, err := store.NewS3Blobs(ctx, store.S3Config{
		Endpoint:  endpoint,
		Bucket:    envOr("OB_S3_BUCKET", "openbackup-test"),
		Prefix:    prefix,
		AccessKey: envOr("OB_S3_ACCESS_KEY", "any"),
		SecretKey: envOr("OB_S3_SECRET_KEY", "any"),
		Region:    envOr("OB_S3_REGION", "us-east-1"),
		UseSSL:    os.Getenv("OB_S3_USE_SSL") == "true",
	})
	if err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.RetentionDays = 30
	dir := t.TempDir()
	h := newHarnessDir(t, cfg, dir, blobs)
	h.setupAccount()
	client := h.enroll("s3-box")

	oldBody := []byte("old-only")
	keptBody := []byte("kept-by-the-newer-snapshot")
	oldID := h.backup(client, api.SnapshotFull, "", map[string][]byte{"docs/old.txt": oldBody}, nil)
	keptID := h.backup(client, api.SnapshotFull, "", map[string][]byte{"docs/kept.txt": keptBody}, nil)

	got, err := client.GetChunk(ctx, hash.Sum(keptBody))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := h.codec.Decode(got)
	if err != nil || string(plain) != string(keptBody) {
		t.Fatalf("restore through the S3 server: %v", err)
	}

	old := time.Now().AddDate(-2, 0, 0).UnixMilli()
	if _, err := h.db.SQL().ExecContext(ctx, `UPDATE snapshots SET started_at = ?, completed_at = ? WHERE id = ?`, old, old, oldID); err != nil {
		t.Fatal(err)
	}
	aged := time.Now().Add(-time.Hour).UnixMilli()
	if _, err := h.db.SQL().ExecContext(ctx, `UPDATE chunks SET created_at = ?`, aged); err != nil {
		t.Fatal(err)
	}

	if _, err := maintenance.New(h.db, h.blobs, slog.New(slog.DiscardHandler)).Run(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.SnapshotByID(ctx, snapshotOwner(t, h, keptID), keptID); err != nil {
		t.Fatalf("newer snapshot: %v", err)
	}
	if _, err := h.db.SnapshotByID(ctx, snapshotOwner(t, h, oldID), oldID); err == nil {
		t.Fatal("retention left the expired snapshot")
	}
	if _, ok := blobs.Has(ctx, hash.Sum(oldBody)); ok {
		t.Fatal("expired blob still in object storage")
	}
	if _, ok := blobs.Has(ctx, hash.Sum(keptBody)); !ok {
		t.Fatal("retention removed the blob the newer snapshot still needs")
	}

	// An explicit delete of the remaining snapshot, then another collection,
	// must remove that object too.
	resp := h.do("DELETE", "/api/v1/ui/snapshots/"+keptID, nil, nil)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("delete snapshot returned %d", resp.StatusCode)
	}
	if _, err := maintenance.New(h.db, h.blobs, slog.New(slog.DiscardHandler)).Run(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := blobs.Has(ctx, hash.Sum(keptBody)); ok {
		t.Fatal("deleted snapshot's blob remained in object storage")
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
