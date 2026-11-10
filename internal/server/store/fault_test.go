package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"

	"github.com/foisalislambd/openbackup/internal/hash"
)

func TestSyncDirOnARealDirectory(t *testing.T) {
	if err := syncDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

func TestPackPublishFaultLeavesNothingBehind(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	blobs, err := NewFSBlobs(dir)
	if err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("p"), 1000)
	digest := hash.Sum(payload)
	Fault = func(stage string) error {
		if stage == "pack-publish" {
			return errors.New("disk full")
		}
		return nil
	}
	t.Cleanup(func() { Fault = nil })
	if _, err := blobs.Put(ctx, digest, payload); err == nil {
		t.Fatal("expected the pack publish to fail")
	}
	if _, ok := blobs.Has(ctx, digest); ok {
		t.Fatal("failed publish was visible")
	}
	again, err := NewFSBlobs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := again.Has(ctx, digest); ok {
		t.Fatal("failed publish survived restart")
	}
}

func TestIndexFaultIsRepairedOnRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	blobs, err := NewFSBlobs(dir)
	if err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("i"), 1000)
	digest := hash.Sum(payload)
	Fault = func(stage string) error {
		if stage == "idx-publish" {
			return errors.New("index write failed")
		}
		return nil
	}
	if _, err := blobs.Put(ctx, digest, payload); err == nil {
		t.Fatal("expected the index publish to fail")
	}
	Fault = nil
	if _, ok := blobs.Has(ctx, digest); ok {
		t.Fatal("index failure updated memory")
	}
	again, err := NewFSBlobs(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := again.Get(ctx, digest)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("restart after index fault: %v", err)
	}
}

func TestTombstoneFaultDoesNotAcknowledgeOrResurrect(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	blobs, err := NewFSBlobs(dir)
	if err != nil {
		t.Fatal(err)
	}
	a := bytes.Repeat([]byte("a"), 20_000)
	b := bytes.Repeat([]byte("b"), 20_000)
	da, db := hash.Sum(a), hash.Sum(b)
	if _, err := blobs.PutMany(ctx, []BlobPart{{Digest: da, Data: a}, {Digest: db, Data: b}}); err != nil {
		t.Fatal(err)
	}
	Fault = func(stage string) error {
		if stage == "after-tombstone" {
			return errors.New("crash after tombstone")
		}
		return nil
	}
	if err := blobs.Delete(ctx, da); err == nil {
		t.Fatal("deletion was acknowledged after a crash")
	}
	// The process that crashed still has the old map. A restart must honour
	// the tombstone and keep the survivor.
	Fault = nil
	again, err := NewFSBlobs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := again.Has(ctx, da); ok {
		t.Fatal("acknowledged-on-disk deletion came back after index rebuild")
	}
	got, err := again.Get(ctx, db)
	if err != nil || !bytes.Equal(got, b) {
		t.Fatalf("survivor: %v", err)
	}
}

func TestCompactionFaultKeepsTheOldPackUntilRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	blobs, err := NewFSBlobs(dir)
	if err != nil {
		t.Fatal(err)
	}
	a := bytes.Repeat([]byte("a"), 20_000)
	b := bytes.Repeat([]byte("b"), 20_000)
	da, db := hash.Sum(a), hash.Sum(b)
	if _, err := blobs.PutMany(ctx, []BlobPart{{Digest: da, Data: a}, {Digest: db, Data: b}}); err != nil {
		t.Fatal(err)
	}
	before := packPayloadBytes(t, dir)
	Fault = func(stage string) error {
		if stage == "compact-publish" {
			return errors.New("compaction failed")
		}
		return nil
	}
	if err := blobs.Delete(ctx, da); err == nil {
		t.Fatal("compaction failure was treated as success")
	}
	if _, ok := blobs.Has(ctx, da); !ok {
		t.Fatal("failed compaction dropped the blob from memory")
	}
	Fault = nil
	again, err := NewFSBlobs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := again.Has(ctx, da); ok {
		t.Fatal("tombstone did not hide the blob after a failed compaction")
	}
	got, err := again.Get(ctx, db)
	if err != nil || !bytes.Equal(got, b) {
		t.Fatalf("survivor: %v", err)
	}
	after := packPayloadBytes(t, dir)
	if after >= before {
		t.Fatalf("restart did not reclaim the deleted payload: %d -> %d", before, after)
	}
	_ = os.Remove(onlyPack(t, dir) + ".idx")
	rebuilt, err := NewFSBlobs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rebuilt.Has(ctx, da); ok {
		t.Fatal("index rebuild resurrected the deletion")
	}
}

func TestMixedLooseV1AndV2PacksRestore(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	loose := bytes.Repeat([]byte("L"), looseBlobThreshold+8)
	ld := hash.Sum(loose)
	blobs, err := NewFSBlobs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := blobs.Put(ctx, ld, loose); err != nil {
		t.Fatal(err)
	}
	v2 := []byte("version-two-payload")
	v2d := hash.Sum(v2)
	if _, err := blobs.Put(ctx, v2d, v2); err != nil {
		t.Fatal(err)
	}
	v1 := []byte("version-one-payload")
	v1d := hash.Sum(v1)
	if err := writeV1Pack(dir, v1d, v1); err != nil {
		t.Fatal(err)
	}
	again, err := NewFSBlobs(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		digest  string
		payload []byte
	}{{ld, loose}, {v2d, v2}, {v1d, v1}} {
		got, err := again.Get(ctx, tc.digest)
		if err != nil || !bytes.Equal(got, tc.payload) {
			t.Fatalf("digest %s: %v", tc.digest, err)
		}
	}
}
