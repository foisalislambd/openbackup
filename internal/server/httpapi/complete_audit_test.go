package httpapi_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/foisalislambd/openbackup/internal/api"
	"github.com/foisalislambd/openbackup/internal/hash"
	"github.com/foisalislambd/openbackup/internal/server/config"
	"github.com/foisalislambd/openbackup/internal/server/store"
)

func TestCompleteRejectsMissingAndCorruptBlobs(t *testing.T) {
	h := newHarness(t)
	h.setupAccount()
	client := h.enroll("laptop")
	ctx := context.Background()

	content := []byte("complete-me-intact")
	digest := hash.Sum(content)
	blob, err := h.codec.Encode(content, digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.PutChunk(ctx, digest, blob, len(content)); err != nil {
		t.Fatal(err)
	}
	if err := h.blobs.Delete(ctx, digest); err != nil {
		t.Fatal(err)
	}
	id := openSnapshot(t, client)
	if err := client.AddEntries(ctx, id, api.AddEntriesRequest{Entries: []api.Entry{{
		Path: "Documents/a.txt", Type: api.EntryFile, Size: int64(len(content)), Chunks: []string{digest}, Digest: digest,
	}}}); err != nil {
		t.Fatal(err)
	}
	if err := client.CompleteSnapshot(ctx, id, api.CompleteSnapshotRequest{FileCount: 1}); err == nil {
		t.Fatal("completed a snapshot whose blob was deleted")
	}
	assertSnapshotStatus(t, h, id, api.SnapshotStatusFailed)

	content = []byte("complete-me-corrupt")
	digest = hash.Sum(content)
	blob, err = h.codec.Encode(content, digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.PutChunk(ctx, digest, blob, len(content)); err != nil {
		t.Fatal(err)
	}
	corruptPackPayload(t, h.blobs.Location())
	id = openSnapshot(t, client)
	if err := client.AddEntries(ctx, id, api.AddEntriesRequest{Entries: []api.Entry{{
		Path: "Documents/b.txt", Type: api.EntryFile, Size: int64(len(content)), Chunks: []string{digest}, Digest: digest,
	}}}); err != nil {
		t.Fatal(err)
	}
	if err := client.CompleteSnapshot(ctx, id, api.CompleteSnapshotRequest{FileCount: 1}); err == nil {
		t.Fatal("completed a snapshot whose blob was corrupt")
	}
	assertSnapshotStatus(t, h, id, api.SnapshotStatusFailed)
}

func TestBatchPlaintextOverBudgetIsRejected(t *testing.T) {
	cfg := config.Default()
	cfg.RetentionDays = 30
	cfg.UploadBudgetBytes = 64
	h := newHarnessCfg(t, cfg)
	h.setupAccount()
	client := h.enroll("laptop")
	plain := bytes.Repeat([]byte("aaaaaaaa"), 512)
	digest := hash.Sum(plain)
	blob, err := h.codec.Encode(plain, digest)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(blob)) >= cfg.UploadBudgetBytes {
		t.Fatalf("fixture did not compress below the budget: %d", len(blob))
	}
	err = client.PutChunk(context.Background(), digest, blob, 1)
	if err == nil {
		t.Fatal("plaintext larger than the in-flight budget was accepted")
	}
	if _, ok := h.blobs.Has(context.Background(), digest); ok {
		t.Fatal("rejected chunk was stored")
	}
}

func TestBatchUnderReportedPlaintextIsRejected(t *testing.T) {
	cfg := config.Default()
	cfg.RetentionDays = 30
	cfg.UploadBudgetBytes = 64
	h := newHarnessCfg(t, cfg)
	h.setupAccount()
	client := h.enroll("laptop")
	plain := bytes.Repeat([]byte("bbbbbbbb"), 512)
	digest := hash.Sum(plain)
	blob, err := h.codec.Encode(plain, digest)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(blob)) >= cfg.UploadBudgetBytes {
		t.Fatalf("fixture did not compress below the budget: %d", len(blob))
	}
	err = client.PutChunks(context.Background(), []api.ChunkBlob{{
		Digest: digest, PlainLen: 1, Blob: blob,
	}})
	if err == nil {
		t.Fatal("batch whose header plaintext exceeds the budget was accepted")
	}
	if _, ok := h.blobs.Has(context.Background(), digest); ok {
		t.Fatal("rejected batch chunk was stored")
	}
}

func TestUploadRefusedWhenDiskIsLow(t *testing.T) {
	h := newHarness(t)
	h.setupAccount()
	client := h.enroll("laptop")
	store.FreeBytesOverride = func() int64 { return 1 << 20 }
	t.Cleanup(func() { store.FreeBytesOverride = nil })
	plain := []byte("disk-full")
	digest := hash.Sum(plain)
	blob, err := h.codec.Encode(plain, digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.PutChunk(context.Background(), digest, blob, len(plain)); err == nil {
		t.Fatal("upload succeeded with less than 8 MiB free")
	}
	if _, ok := h.blobs.Has(context.Background(), digest); ok {
		t.Fatal("refused chunk was stored")
	}
}

func openSnapshot(t *testing.T, client *api.Client) string {
	t.Helper()
	started, err := client.StartSnapshot(context.Background(), api.StartSnapshotRequest{
		Kind: api.SnapshotFull, StartedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return started.SnapshotID
}

func assertSnapshotStatus(t *testing.T, h *harness, id, want string) {
	t.Helper()
	snap, err := h.db.SnapshotByID(context.Background(), snapshotOwner(t, h, id), id)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Status != want {
		t.Fatalf("status %s, want %s (%s)", snap.Status, want, snap.Error)
	}
}

func corruptPackPayload(t *testing.T, root string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, "packs", "*", "*"))
	if err != nil {
		t.Fatal(err)
	}
	var pack string
	for _, match := range matches {
		if len(filepath.Base(match)) == 32 {
			pack = match
			break
		}
	}
	if pack == "" {
		t.Fatal("no pack to corrupt")
	}
	raw, err := os.ReadFile(pack)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 48 {
		t.Fatalf("pack too small: %d", len(raw))
	}
	// The header and digest sit at the front. Zero the rest so the payload
	// the server will read at its recorded offset cannot hash to the digest.
	for i := 48; i < len(raw); i++ {
		raw[i] = 0
	}
	if err := os.WriteFile(pack, raw, 0o640); err != nil {
		t.Fatal(err)
	}
}
