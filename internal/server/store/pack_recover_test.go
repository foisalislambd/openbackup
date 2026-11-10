package store

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/foisalislambd/openbackup/internal/hash"
)

func TestTruncatedPackKeepsOnlyCompleteFrames(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	blobs, err := NewFSBlobs(dir)
	if err != nil {
		t.Fatal(err)
	}
	a := bytes.Repeat([]byte("a"), 1000)
	b := bytes.Repeat([]byte("b"), 1000)
	da, db := hash.Sum(a), hash.Sum(b)
	if _, err := blobs.PutMany(ctx, []BlobPart{{Digest: da, Data: a}, {Digest: db, Data: b}}); err != nil {
		t.Fatal(err)
	}
	pack := onlyPack(t, dir)
	raw, err := os.ReadFile(pack)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 40 {
		t.Fatalf("pack too small: %d", len(raw))
	}
	// Cut the tail so the last frame's checksum cannot match.
	if err := os.WriteFile(pack, raw[:len(raw)-24], 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(pack + ".idx"); err != nil {
		t.Fatal(err)
	}
	again, err := NewFSBlobs(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := again.Get(ctx, da)
	if err != nil || !bytes.Equal(got, a) {
		t.Fatalf("first frame after truncation: %v", err)
	}
	if _, ok := again.Has(ctx, db); ok {
		t.Fatal("truncated frame was advertised")
	}
}

func TestTruncatedIndexDoesNotDropAFrame(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	blobs, err := NewFSBlobs(dir)
	if err != nil {
		t.Fatal(err)
	}
	a := bytes.Repeat([]byte("a"), 40)
	b := bytes.Repeat([]byte("b"), 40)
	da, db := hash.Sum(a), hash.Sum(b)
	if _, err := blobs.PutMany(ctx, []BlobPart{{Digest: da, Data: a}, {Digest: db, Data: b}}); err != nil {
		t.Fatal(err)
	}
	pack := onlyPack(t, dir)
	idx, err := os.ReadFile(pack + ".idx")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pack+".idx", idx[:len(idx)/2], 0o640); err != nil {
		t.Fatal(err)
	}
	again, err := NewFSBlobs(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, dig := range []string{da, db} {
		if _, ok := again.Has(ctx, dig); !ok {
			t.Fatalf("digest %s missing after a short index", dig)
		}
	}
}

func TestDeleteCompactsPackAndTombstoneSurvivesIndexLoss(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	blobs, err := NewFSBlobs(dir)
	if err != nil {
		t.Fatal(err)
	}
	a := bytes.Repeat([]byte("a"), 100_000)
	b := bytes.Repeat([]byte("b"), 100_000)
	da, db := hash.Sum(a), hash.Sum(b)
	if _, err := blobs.PutMany(ctx, []BlobPart{{Digest: da, Data: a}, {Digest: db, Data: b}}); err != nil {
		t.Fatal(err)
	}
	before := packPayloadBytes(t, dir)
	if err := blobs.Delete(ctx, da); err != nil {
		t.Fatal(err)
	}
	after := packPayloadBytes(t, dir)
	if after >= before-50_000 {
		t.Fatalf("pack bytes %d -> %d, deletion did not reclaim the payload", before, after)
	}
	if _, ok := blobs.Has(ctx, da); ok {
		t.Fatal("deleted digest still present")
	}
	got, err := blobs.Get(ctx, db)
	if err != nil || !bytes.Equal(got, b) {
		t.Fatalf("survivor: %v", err)
	}
	// Losing the index must not resurrect the deleted digest.
	pack := onlyPack(t, dir)
	if err := os.Remove(pack + ".idx"); err != nil {
		t.Fatal(err)
	}
	again, err := NewFSBlobs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := again.Has(ctx, da); ok {
		t.Fatal("tombstone did not survive a missing index")
	}
	got, err = again.Get(ctx, db)
	if err != nil || !bytes.Equal(got, b) {
		t.Fatalf("survivor after restart: %v", err)
	}
}

func TestVersion1PackStillRestores(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	payload := []byte("legacy-pack-payload")
	digest := hash.Sum(payload)
	id := "0123456789abcdef0123456789abcdef"
	packDir := filepath.Join(dir, "packs", id[:2])
	if err := os.MkdirAll(packDir, 0o750); err != nil {
		t.Fatal(err)
	}
	rawDig, err := hex.DecodeString(digest)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	buf.WriteString(packMagic)
	buf.Write([]byte{packVersion, 0, 0, 0})
	buf.Write(rawDig)
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], uint64(len(payload)))
	buf.Write(tmp[:n])
	buf.Write(payload)
	if err := os.WriteFile(filepath.Join(packDir, id), buf.Bytes(), 0o640); err != nil {
		t.Fatal(err)
	}
	blobs, err := NewFSBlobs(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := blobs.Get(ctx, digest)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("v1 pack: %v %q", err, got)
	}
}

func onlyPack(t *testing.T, root string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, "packs", "*", "*"))
	if err != nil {
		t.Fatal(err)
	}
	var packs []string
	for _, match := range matches {
		base := filepath.Base(match)
		if len(base) == 32 {
			packs = append(packs, match)
		}
	}
	if len(packs) != 1 {
		t.Fatalf("packs: %v", packs)
	}
	return packs[0]
}

func packPayloadBytes(t *testing.T, root string) int64 {
	t.Helper()
	var n int64
	matches, err := filepath.Glob(filepath.Join(root, "packs", "*", "*"))
	if err != nil {
		t.Fatal(err)
	}
	for _, match := range matches {
		base := filepath.Base(match)
		if len(base) != 32 {
			continue
		}
		st, err := os.Stat(match)
		if err != nil {
			t.Fatal(err)
		}
		n += st.Size()
	}
	return n
}

func writeV1Pack(dir, digest string, payload []byte) error {
	id := "0123456789abcdef0123456789abcdef"
	packDir := filepath.Join(dir, "packs", id[:2])
	if err := os.MkdirAll(packDir, 0o750); err != nil {
		return err
	}
	rawDig, err := hex.DecodeString(digest)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	buf.WriteString(packMagic)
	buf.Write([]byte{packVersion, 0, 0, 0})
	buf.Write(rawDig)
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], uint64(len(payload)))
	buf.Write(tmp[:n])
	buf.Write(payload)
	return os.WriteFile(filepath.Join(packDir, id), buf.Bytes(), 0o640)
}
