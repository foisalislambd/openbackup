package store

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/foisalislambd/openbackup/internal/hash"
)

func TestPackStoresSmallBlobsAndKeepsLargeOnesLoose(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	blobs, err := NewFSBlobs(dir)
	if err != nil {
		t.Fatal(err)
	}
	small := bytes.Repeat([]byte("s"), 100)
	large := bytes.Repeat([]byte("L"), looseBlobThreshold+10)
	smallDigest := hash.Sum(small)
	largeDigest := hash.Sum(large)
	if _, err := blobs.Put(ctx, smallDigest, small); err != nil {
		t.Fatal(err)
	}
	if _, err := blobs.Put(ctx, largeDigest, large); err != nil {
		t.Fatal(err)
	}
	loose := filepath.Join(dir, "blobs", largeDigest[:2], largeDigest[2:4], largeDigest)
	if _, err := os.Stat(loose); err != nil {
		t.Fatalf("large blob should stay a loose file: %v", err)
	}
	packedPath := filepath.Join(dir, "blobs", smallDigest[:2], smallDigest[2:4], smallDigest)
	if _, err := os.Stat(packedPath); err == nil {
		t.Fatal("small blob should not be a loose file")
	}
	got, err := blobs.Get(ctx, smallDigest)
	if err != nil || !bytes.Equal(got, small) {
		t.Fatalf("get packed: %v", err)
	}
	got, err = blobs.Get(ctx, largeDigest)
	if err != nil || !bytes.Equal(got, large) {
		t.Fatalf("get loose: %v", err)
	}

	// A missing index is rebuilt from the pack on the next open.
	matches, err := filepath.Glob(filepath.Join(dir, "packs", "*", "*.idx"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("index files: %v %v", matches, err)
	}
	if err := os.Remove(matches[0]); err != nil {
		t.Fatal(err)
	}
	again, err := NewFSBlobs(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err = again.Get(ctx, smallDigest)
	if err != nil || !bytes.Equal(got, small) {
		t.Fatalf("get after index rebuild: %v", err)
	}
	if err := again.Delete(ctx, smallDigest); err != nil {
		t.Fatal(err)
	}
	if _, ok := again.Has(ctx, smallDigest); ok {
		t.Fatal("deleted packed blob still present")
	}
	if _, ok := again.Has(ctx, largeDigest); !ok {
		t.Fatal("deleting a packed blob removed the loose one")
	}
}
