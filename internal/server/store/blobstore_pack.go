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
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

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
//	magic "OBPK" | version u8 | reserved 3 bytes
//	repeated:
//	    digest 32 bytes
//	    uvarint payload length
//	    payload
//	    crc32 of the frame   (version 2 only)
//	footer "OBPF" | uint32 count | uint32 crc   (version 2 only)
//
// Version 1 packs, written by the first cut of this format, are still read.
// A short trailing frame is ignored. The ".idx" file records offsets so a
// read does not scan the pack; it is not the record of which blobs are alive.
// That record is packs/deleted, an append-only list of digest and crc32.
// A missing or torn index is rebuilt from the pack, and a digest in the
// deletion log is not resurrected. Deleting a member writes the tombstone,
// then rewrites the surviving frames into a new pack.

const (
	packMagic     = "OBPK"
	packVersion   = 1
	packVersionV2 = 2
	packFooter    = "OBPF"
	idxMagic      = "OIDX"
	idxVersion    = 1
	deletionMagic = "OBTD"

	// looseBlobThreshold is the size at which a blob stays a file of its own.
	// Below this, directory entries and per-blob fsyncs dominate the cost.
	looseBlobThreshold = 512 << 10
	// maxPackFrame refuses a length that cannot be a real chunk. It also
	// stops a corrupt index from allocating an absurd buffer.
	maxPackFrame = 16 << 20
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

	fresh := make([]BlobPart, 0, len(indexes))
	freshIdx := make([]int, 0, len(indexes))
	for _, i := range indexes {
		part := parts[i]
		if _, ok := b.packed[part.Digest]; ok {
			continue
		}
		if _, err := os.Stat(b.path(part.Digest)); err == nil {
			continue
		}
		fresh = append(fresh, part)
		freshIdx = append(freshIdx, i)
	}
	if len(fresh) == 0 {
		return nil
	}
	raw, recs, err := buildPack(fresh)
	if err != nil {
		return err
	}
	id, err := newPackID()
	if err != nil {
		return err
	}
	dir := filepath.Join(b.root, "packs", id[:2])
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	if err := syncDir(filepath.Join(b.root, "packs")); err != nil {
		return err
	}
	tmpDir := filepath.Join(b.root, "tmp")
	packPath := filepath.Join(dir, id)
	if err := fault("pack-publish"); err != nil {
		return err
	}
	if err := writeDurable(tmpDir, packPath, raw); err != nil {
		return err
	}
	idxBytes, err := encodeIdx(recs)
	if err != nil {
		_ = os.Remove(packPath)
		return err
	}
	if err := fault("idx-publish"); err != nil {
		// The pack bytes are durable. Leaving them in place is the crash
		// between the pack rename and the index rename; the next open scans.
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
	for _, i := range freshIdx {
		written[i] = int64(len(parts[i].Data))
	}
	return notePackFormat(b.root)
}

func newPackID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

// Fault, when set, fails a named stage of a pack write. Production leaves it
// nil. Tests use it to simulate a crash or a full disk before the matching
// durable publish. The store must not update its in-memory index, and a later
// open must not resurrect a deletion that was already made durable.
//
// Stages: "pack-publish", "idx-publish", "tombstone-publish", "after-tombstone",
// "compact-publish".
var Fault func(stage string) error

func fault(stage string) error {
	// A real process kill, used by the crash tests. Production leaves both
	// variables unset. This is not a power-loss test: the kernel can still
	// hold bytes that a power cut would drop, and the kill happens at the
	// named stage rather than at a random instruction.
	crashWait(stage)
	if Fault == nil {
		return nil
	}
	return Fault(stage)
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
	// Windows rejects FlushFileBuffers on a directory opened for read
	// (ERROR_ACCESS_DENIED or ERROR_INVALID_FUNCTION). The file itself was
	// already synced before rename. There is no stronger directory flush
	// available, so this is not a failed fsync of file bytes.
	if err != nil && dirSyncUnsupported(err) {
		return nil
	}
	return err
}

func dirSyncUnsupported(err error) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	return errno == 5 || errno == 1
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
	buf, err := readBounded(f, loc.offset, loc.length)
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
	// The deletion log is the record that survives a missing index. It is
	// written before the pack changes so a crash cannot resurrect the blob.
	if err := b.appendDeletion(digest); err != nil {
		return err
	}
	info := b.packs[loc.id]
	if info == nil {
		delete(b.packed, digest)
		return nil
	}
	live := make([]packRecord, 0, len(info.records))
	for _, rec := range info.records {
		if rec.digest != digest {
			live = append(live, rec)
		}
	}
	oldPath := b.packPath(loc.id)
	if len(live) == 0 {
		if err := removePackFiles(oldPath); err != nil {
			return err
		}
		_ = syncDir(filepath.Dir(oldPath))
		delete(b.packs, loc.id)
		delete(b.packed, digest)
		return nil
	}
	payloads, err := readRecordPayloads(oldPath, live)
	if err != nil {
		return err
	}
	if err := b.installCompacted(loc.id, payloads); err != nil {
		return err
	}
	return nil
}

// installCompacted replaces pack id with a pack that contains only payloads.
// The in-memory map changes after the new file is durable. Callers hold packMu.
func (b *FSBlobs) installCompacted(oldID string, payloads []BlobPart) error {
	raw, recs, err := buildPack(payloads)
	if err != nil {
		return err
	}
	id, err := newPackID()
	if err != nil {
		return err
	}
	dir := filepath.Join(b.root, "packs", id[:2])
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	_ = syncDir(filepath.Join(b.root, "packs"))
	tmpDir := filepath.Join(b.root, "tmp")
	packPath := filepath.Join(dir, id)
	if err := fault("compact-publish"); err != nil {
		return err
	}
	if err := writeDurable(tmpDir, packPath, raw); err != nil {
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
	oldPath := b.packPath(oldID)
	delete(b.packs, oldID)
	for digest, loc := range b.packed {
		if loc.id == oldID {
			delete(b.packed, digest)
		}
	}
	b.packs[id] = &packInfo{records: recs}
	for _, rec := range recs {
		b.packed[rec.digest] = packLoc{id: id, offset: rec.offset, length: rec.length}
	}
	if err := removePackFiles(oldPath); err != nil {
		return err
	}
	_ = syncDir(filepath.Dir(oldPath))
	return notePackFormat(b.root)
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
	dead, err := loadDeletionLog(b.deletionPath())
	if err != nil {
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
		if strings.HasSuffix(name, ".idx") || strings.HasSuffix(name, ".tomb") || name == "deleted" ||
			strings.HasPrefix(name, "tmp-") || strings.HasPrefix(name, "pack-") {
			return nil
		}
		if len(name) != 32 {
			return nil
		}
		recs, err := scanPack(path)
		if err != nil {
			return fmt.Errorf("store: pack %s: %w", name, err)
		}
		live := make([]packRecord, 0, len(recs))
		var dropped []packRecord
		for _, rec := range recs {
			if _, gone := dead[rec.digest]; gone {
				dropped = append(dropped, rec)
				continue
			}
			live = append(live, rec)
		}
		if len(dropped) > 0 {
			parts, err := readRecordPayloads(path, live)
			if err != nil {
				return err
			}
			if len(parts) == 0 {
				if err := removePackFiles(path); err != nil {
					return err
				}
				return nil
			}
			if err := b.installCompacted(name, parts); err != nil {
				return err
			}
			return nil
		}
		idxBytes, err := encodeIdx(live)
		if err != nil {
			return err
		}
		if err := writeDurable(filepath.Join(b.root, "tmp"), path+".idx", idxBytes); err != nil {
			return err
		}
		b.packs[name] = &packInfo{records: live}
		for _, rec := range recs {
			if _, gone := dead[rec.digest]; gone {
				continue
			}
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
			if errors.Is(err, io.EOF) {
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
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < 8 {
		return nil, errors.New("store: short pack")
	}
	if string(data[:4]) != packMagic {
		return nil, errors.New("store: bad pack header")
	}
	ver := data[4]
	if ver != packVersion && ver != packVersionV2 {
		return nil, fmt.Errorf("store: pack version %d", ver)
	}
	off := 8
	var recs []packRecord
	for off < len(data) {
		if ver == packVersionV2 && len(data)-off == 12 && string(data[off:off+4]) == packFooter {
			break
		}
		if len(data)-off < hash.Size {
			break
		}
		digest := hex.EncodeToString(data[off : off+hash.Size])
		frameStart := off
		off += hash.Size
		length, n := binary.Uvarint(data[off:])
		if n <= 0 {
			break
		}
		off += n
		if length > maxPackFrame || off > len(data) || int(length) > len(data)-off {
			break
		}
		frame := data[frameStart : off+int(length)]
		payloadOff := off
		off += int(length)
		if ver == packVersionV2 {
			if off+4 > len(data) {
				break
			}
			want := binary.LittleEndian.Uint32(data[off : off+4])
			off += 4
			if crc32.ChecksumIEEE(frame) != want {
				break
			}
		}
		recs = append(recs, packRecord{digest: digest, offset: int64(payloadOff), length: int64(length)})
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

func buildPack(parts []BlobPart) ([]byte, []packRecord, error) {
	var buf bytes.Buffer
	buf.WriteString(packMagic)
	buf.Write([]byte{packVersionV2, 0, 0, 0})
	recs := make([]packRecord, 0, len(parts))
	for _, part := range parts {
		raw, err := hex.DecodeString(part.Digest)
		if err != nil || len(raw) != hash.Size {
			return nil, nil, hash.ErrInvalidDigest
		}
		frameStart := buf.Len()
		buf.Write(raw)
		var tmp [binary.MaxVarintLen64]byte
		n := binary.PutUvarint(tmp[:], uint64(len(part.Data)))
		buf.Write(tmp[:n])
		offset := buf.Len()
		buf.Write(part.Data)
		sum := crc32.ChecksumIEEE(buf.Bytes()[frameStart:])
		var cbuf [4]byte
		binary.LittleEndian.PutUint32(cbuf[:], sum)
		buf.Write(cbuf[:])
		recs = append(recs, packRecord{digest: part.Digest, offset: int64(offset), length: int64(len(part.Data))})
	}
	buf.WriteString(packFooter)
	var tail [8]byte
	binary.LittleEndian.PutUint32(tail[:4], uint32(len(recs)))
	binary.LittleEndian.PutUint32(tail[4:], crc32.ChecksumIEEE(buf.Bytes()[8:buf.Len()-len(packFooter)]))
	buf.Write(tail[:])
	return buf.Bytes(), recs, nil
}

func readBounded(f *os.File, offset, length int64) ([]byte, error) {
	if offset < 0 || length < 0 || length > maxPackFrame {
		return nil, errors.New("store: pack location out of range")
	}
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if offset > st.Size() || length > st.Size()-offset {
		return nil, errors.New("store: pack location out of range")
	}
	buf := make([]byte, length)
	if _, err := f.ReadAt(buf, offset); err != nil {
		return nil, err
	}
	return buf, nil
}

func readRecordPayloads(path string, recs []packRecord) ([]BlobPart, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := make([]BlobPart, 0, len(recs))
	for _, rec := range recs {
		buf, err := readBounded(f, rec.offset, rec.length)
		if err != nil {
			return nil, err
		}
		out = append(out, BlobPart{Digest: rec.digest, Data: buf})
	}
	return out, nil
}

func removePackFiles(packPath string) error {
	if err := os.Remove(packPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Remove(packPath + ".idx"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (b *FSBlobs) deletionPath() string {
	return filepath.Join(b.root, "packs", "deleted")
}

func (b *FSBlobs) appendDeletion(digest string) error {
	raw, err := hex.DecodeString(digest)
	if err != nil || len(raw) != hash.Size {
		return hash.ErrInvalidDigest
	}
	var rec [hash.Size + 4]byte
	copy(rec[:hash.Size], raw)
	binary.LittleEndian.PutUint32(rec[hash.Size:], crc32.ChecksumIEEE(raw))
	path := b.deletionPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o640)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.Size() == 0 {
		if _, err := f.Write([]byte(deletionMagic + "\x01")); err != nil {
			return err
		}
	} else if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	if err := fault("tombstone-publish"); err != nil {
		return err
	}
	if _, err := f.Write(rec[:]); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := syncDir(filepath.Dir(path)); err != nil {
		return err
	}
	return fault("after-tombstone")
}

func loadDeletionLog(path string) (map[string]struct{}, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]struct{}{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]struct{}{}
	if len(data) < 5 || string(data[:4]) != deletionMagic || data[4] != 1 {
		return out, nil
	}
	body := data[5:]
	rec := hash.Size + 4
	n := len(body) / rec
	for i := 0; i < n; i++ {
		item := body[i*rec : (i+1)*rec]
		sum := binary.LittleEndian.Uint32(item[hash.Size:])
		if crc32.ChecksumIEEE(item[:hash.Size]) != sum {
			continue
		}
		out[hex.EncodeToString(item[:hash.Size])] = struct{}{}
	}
	return out, nil
}
