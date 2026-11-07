package store

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/foisalislambd/openbackup/internal/hash"
)

// Pack files group many small content-addressed blobs into one durable file.
//
// A pack is self-describing so a crash between the file rename and the index
// rename is repaired on the next start by scanning the frames. The blob bytes
// inside a frame are the existing codec format, unchanged, which is why an
// older repository of one-file-per-blob still restores and why encryption is
// unaffected.
//
//	magic "OBPK" | version u8=1 | reserved 3 bytes
//	repeated:
//	    digest 32 bytes
//	    uvarint payload length
//	    payload
//
// The companion ".idx" file is a cache of (digest, offset, length) so reads
// do not scan the pack. It is rebuilt from the pack when it is missing.

const (
	packMagic   = "OBPK"
	packVersion = 1
	idxMagic    = "OIDX"
	idxVersion  = 1

	// looseBlobThreshold is the size at which a blob stays a file of its own.
	// Below this, directory entries and per-blob fsyncs dominate the cost.
	looseBlobThreshold = 512 << 10
)

var errNotPacked = errors.New("store: blob is not in a pack")

// BlobPart is one blob in a PutMany batch.
type BlobPart struct {
	Digest string
	Data   []byte
}

type packLoc struct {
	id     string
	offset int64
	length int64
}

type packInfo struct {
	records []packRecord
}

type packRecord struct {
	digest string
	offset int64
	length int64
}

// PutMany stores many blobs, packing the small ones into a single fsynced
// file. The returned slice is aligned with parts; an entry is 0 when that
// digest was already stored.
func (b *FSBlobs) PutMany(_ context.Context, parts []BlobPart) ([]int64, error) {
	written := make([]int64, len(parts))
	small := make([]int, 0, len(parts))
	for i, part := range parts {
		if err := hash.Validate(part.Digest); err != nil {
			return nil, err
		}
		if len(part.Data) >= looseBlobThreshold {
			n, err := b.putLoose(part.Digest, part.Data)
			if err != nil {
				return nil, err
			}
			written[i] = n
			continue
		}
		small = append(small, i)
	}
	if len(small) == 0 {
		return written, nil
	}
	if err := b.sealSmall(parts, small, written); err != nil {
		return nil, err
	}
	return written, nil
}

func (b *FSBlobs) sealSmall(parts []BlobPart, indexes []int, written []int64) error {
	b.packMu.Lock()
	defer b.packMu.Unlock()

	var buf bytes.Buffer
	buf.WriteString(packMagic)
	buf.Write([]byte{packVersion, 0, 0, 0})
	var recs []packRecord
	var fresh []int
	for _, i := range indexes {
		part := parts[i]
		if _, ok := b.packed[part.Digest]; ok {
			continue
		}
		if _, err := os.Stat(b.path(part.Digest)); err == nil {
			continue
		}
		raw, err := hex.DecodeString(part.Digest)
		if err != nil || len(raw) != hash.Size {
			return hash.ErrInvalidDigest
		}
		buf.Write(raw)
		var tmp [binary.MaxVarintLen64]byte
		n := binary.PutUvarint(tmp[:], uint64(len(part.Data)))
		buf.Write(tmp[:n])
		recs = append(recs, packRecord{
			digest: part.Digest,
			offset: int64(buf.Len()),
			length: int64(len(part.Data)),
		})
		buf.Write(part.Data)
		fresh = append(fresh, i)
	}
	if len(recs) == 0 {
		return nil
	}

	id, err := newPackID()
	if err != nil {
		return err
	}
	dir := filepath.Join(b.root, "packs", id[:2])
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmpDir := filepath.Join(b.root, "tmp")
	packPath := filepath.Join(dir, id)
	if err := writeDurable(tmpDir, packPath, buf.Bytes()); err != nil {
		return err
	}
	idxBytes, err := encodeIdx(recs)
	if err != nil {
		_ = os.Remove(packPath)
		return err
	}
	if err := writeDurable(tmpDir, packPath+".idx", idxBytes); err != nil {
		_ = os.Remove(packPath)
		return err
	}
	if err := syncDir(dir); err != nil {
		return err
	}
	b.packs[id] = &packInfo{records: recs}
	for _, rec := range recs {
		b.packed[rec.digest] = packLoc{id: id, offset: rec.offset, length: rec.length}
	}
	for _, i := range fresh {
		written[i] = int64(len(parts[i].Data))
	}
	return nil
}

func newPackID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func writeDurable(tmpDir, final string, data []byte) error {
	tmp, err := os.CreateTemp(tmpDir, "pack-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, final); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = f.Sync()
	_ = f.Close()
	return err
}

func encodeIdx(recs []packRecord) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(idxMagic)
	buf.WriteByte(idxVersion)
	for _, rec := range recs {
		raw, err := hex.DecodeString(rec.digest)
		if err != nil || len(raw) != hash.Size {
			return nil, hash.ErrInvalidDigest
		}
		buf.Write(raw)
		var tmp [8]byte
		binary.LittleEndian.PutUint64(tmp[:], uint64(rec.offset))
		buf.Write(tmp[:])
		binary.LittleEndian.PutUint64(tmp[:], uint64(rec.length))
		buf.Write(tmp[:])
	}
	return buf.Bytes(), nil
}

func (b *FSBlobs) packHas(digest string) (int64, bool) {
	b.packMu.Lock()
	defer b.packMu.Unlock()
	loc, ok := b.packed[digest]
	if !ok {
		return 0, false
	}
	return loc.length, true
}

func (b *FSBlobs) packGet(digest string) ([]byte, bool, error) {
	b.packMu.Lock()
	loc, ok := b.packed[digest]
	b.packMu.Unlock()
	if !ok {
		return nil, false, nil
	}
	f, err := os.Open(b.packPath(loc.id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, true, ErrBlobNotFound
		}
		return nil, true, err
	}
	defer f.Close()
	buf := make([]byte, loc.length)
	_, err = f.ReadAt(buf, loc.offset)
	if err != nil {
		return nil, true, err
	}
	return buf, true, nil
}

func (b *FSBlobs) packOpen(digest string) (io.ReadCloser, int64, bool, error) {
	b.packMu.Lock()
	loc, ok := b.packed[digest]
	b.packMu.Unlock()
	if !ok {
		return nil, 0, false, nil
	}
	f, err := os.Open(b.packPath(loc.id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, 0, true, ErrBlobNotFound
		}
		return nil, 0, true, err
	}
	return &sectionCloser{SectionReader: io.NewSectionReader(f, loc.offset, loc.length), f: f}, loc.length, true, nil
}

type sectionCloser struct {
	*io.SectionReader
	f *os.File
}

func (s *sectionCloser) Close() error { return s.f.Close() }

func (b *FSBlobs) packDelete(digest string) error {
	b.packMu.Lock()
	defer b.packMu.Unlock()
	loc, ok := b.packed[digest]
	if !ok {
		return errNotPacked
	}
	delete(b.packed, digest)
	info := b.packs[loc.id]
	if info == nil {
		return nil
	}
	old := info.records
	kept := make([]packRecord, 0, len(old))
	for _, rec := range old {
		if rec.digest != digest {
			kept = append(kept, rec)
		}
	}
	info.records = kept
	if len(info.records) == 0 {
		_ = os.Remove(b.packPath(loc.id))
		_ = os.Remove(b.packPath(loc.id) + ".idx")
		delete(b.packs, loc.id)
		return nil
	}
	raw, err := encodeIdx(info.records)
	if err != nil {
		return err
	}
	return writeDurable(filepath.Join(b.root, "tmp"), b.packPath(loc.id)+".idx", raw)
}

func (b *FSBlobs) packPath(id string) string {
	return filepath.Join(b.root, "packs", id[:2], id)
}

func (b *FSBlobs) walkPacks(ctx context.Context, fn func(digest string, size int64) error) error {
	b.packMu.Lock()
	type item struct {
		digest string
		length int64
	}
	items := make([]item, 0, len(b.packed))
	for digest, loc := range b.packed {
		items = append(items, item{digest, loc.length})
	}
	b.packMu.Unlock()
	for _, it := range items {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := fn(it.digest, it.length); err != nil {
			return err
		}
	}
	return nil
}

func (b *FSBlobs) loadPacks() error {
	root := filepath.Join(b.root, "packs")
	if err := os.MkdirAll(root, 0o750); err != nil {
		return err
	}
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if strings.HasSuffix(name, ".idx") || strings.HasPrefix(name, "tmp-") || strings.HasPrefix(name, "pack-") {
			return nil
		}
		if len(name) != 32 {
			return nil
		}
		recs, err := readIdx(path + ".idx")
		if err != nil {
			recs, err = scanPack(path)
			if err != nil {
				return fmt.Errorf("store: pack %s: %w", name, err)
			}
			raw, err := encodeIdx(recs)
			if err != nil {
				return err
			}
			if err := writeDurable(filepath.Join(b.root, "tmp"), path+".idx", raw); err != nil {
				return err
			}
		}
		b.packs[name] = &packInfo{records: recs}
		for _, rec := range recs {
			b.packed[rec.digest] = packLoc{id: name, offset: rec.offset, length: rec.length}
		}
		return nil
	})
}

func readIdx(path string) ([]packRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	magic := make([]byte, 4)
	if _, err := io.ReadFull(r, magic); err != nil {
		return nil, err
	}
	if string(magic) != idxMagic {
		return nil, errors.New("store: bad pack index magic")
	}
	ver, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	if ver != idxVersion {
		return nil, fmt.Errorf("store: pack index version %d", ver)
	}
	var recs []packRecord
	for {
		var dig [hash.Size]byte
		if _, err := io.ReadFull(r, dig[:]); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			return nil, err
		}
		var offb, lenb [8]byte
		if _, err := io.ReadFull(r, offb[:]); err != nil {
			return nil, err
		}
		if _, err := io.ReadFull(r, lenb[:]); err != nil {
			return nil, err
		}
		recs = append(recs, packRecord{
			digest: hex.EncodeToString(dig[:]),
			offset: int64(binary.LittleEndian.Uint64(offb[:])),
			length: int64(binary.LittleEndian.Uint64(lenb[:])),
		})
	}
	return recs, nil
}

func scanPack(path string) ([]packRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	header := make([]byte, 8)
	if _, err := io.ReadFull(f, header); err != nil {
		return nil, err
	}
	if string(header[:4]) != packMagic || header[4] != packVersion {
		return nil, errors.New("store: bad pack header")
	}
	var recs []packRecord
	off := int64(8)
	for off < st.Size() {
		var dig [hash.Size]byte
		if _, err := io.ReadFull(f, dig[:]); err != nil {
			return nil, err
		}
		off += hash.Size
		length, n, err := readUvarintFile(f)
		if err != nil {
			return nil, err
		}
		off += int64(n)
		recs = append(recs, packRecord{
			digest: hex.EncodeToString(dig[:]),
			offset: off,
			length: int64(length),
		})
		if _, err := f.Seek(int64(length), io.SeekCurrent); err != nil {
			return nil, err
		}
		off += int64(length)
	}
	return recs, nil
}

func readUvarintFile(f *os.File) (uint64, int, error) {
	var buf [binary.MaxVarintLen64]byte
	for i := range buf {
		var one [1]byte
		if _, err := io.ReadFull(f, one[:]); err != nil {
			return 0, 0, err
		}
		buf[i] = one[0]
		if one[0] < 0x80 {
			v, n := binary.Uvarint(buf[:i+1])
			if n <= 0 {
				return 0, 0, errors.New("store: bad pack length")
			}
			return v, n, nil
		}
	}
	return 0, 0, errors.New("store: oversized pack length")
}
