package api

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"github.com/foisalislambd/openbackup/internal/hash"
)

// Chunk batch wire format (version 1). One request carries many small blobs so
// the server can fsync a single pack instead of a file per chunk. Older
// servers do not know this path; agents fall back to one PUT per chunk.
//
//	magic "OBB1"
//	uvarint count
//	repeated:
//	    digest 32 bytes
//	    uvarint plaintext length
//	    uvarint blob length
//	    blob bytes
const chunkBatchMagic = "OBB1"

// ChunkBlob is one encoded chunk inside a batch upload.
type ChunkBlob struct {
	Digest   string
	PlainLen int
	Blob     []byte
}

// MaxChunkBatch is how many chunks one batch request may carry.
const MaxChunkBatch = 256

// WriteChunkBatch serialises blobs to w.
func WriteChunkBatch(w io.Writer, blobs []ChunkBlob) error {
	if len(blobs) > MaxChunkBatch {
		return fmt.Errorf("api: chunk batch of %d exceeds %d", len(blobs), MaxChunkBatch)
	}
	if _, err := io.WriteString(w, chunkBatchMagic); err != nil {
		return err
	}
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], uint64(len(blobs)))
	if _, err := w.Write(tmp[:n]); err != nil {
		return err
	}
	for _, blob := range blobs {
		if err := hash.Validate(blob.Digest); err != nil {
			return err
		}
		raw, err := hex.DecodeString(blob.Digest)
		if err != nil {
			return err
		}
		if _, err := w.Write(raw); err != nil {
			return err
		}
		n = binary.PutUvarint(tmp[:], uint64(blob.PlainLen))
		if _, err := w.Write(tmp[:n]); err != nil {
			return err
		}
		n = binary.PutUvarint(tmp[:], uint64(len(blob.Blob)))
		if _, err := w.Write(tmp[:n]); err != nil {
			return err
		}
		if _, err := w.Write(blob.Blob); err != nil {
			return err
		}
	}
	return nil
}

// ReadChunkBatch parses a batch. limit is the maximum accepted blob size.
func ReadChunkBatch(r io.Reader, limit int) ([]ChunkBlob, error) {
	magic := make([]byte, 4)
	if _, err := io.ReadFull(r, magic); err != nil {
		return nil, err
	}
	if string(magic) != chunkBatchMagic {
		return nil, errors.New("api: unknown chunk batch format")
	}
	count, err := readUvarint(r)
	if err != nil {
		return nil, err
	}
	if count > MaxChunkBatch {
		return nil, fmt.Errorf("api: chunk batch of %d exceeds %d", count, MaxChunkBatch)
	}
	out := make([]ChunkBlob, 0, count)
	for i := uint64(0); i < count; i++ {
		var dig [hash.Size]byte
		if _, err := io.ReadFull(r, dig[:]); err != nil {
			return nil, err
		}
		plainLen, err := readUvarint(r)
		if err != nil {
			return nil, err
		}
		blobLen, err := readUvarint(r)
		if err != nil {
			return nil, err
		}
		if blobLen == 0 || blobLen > uint64(limit) || plainLen > uint64(limit) {
			return nil, fmt.Errorf("api: chunk batch entry exceeds %d bytes", limit)
		}
		blob := make([]byte, blobLen)
		if _, err := io.ReadFull(r, blob); err != nil {
			return nil, err
		}
		out = append(out, ChunkBlob{
			Digest:   hex.EncodeToString(dig[:]),
			PlainLen: int(plainLen),
			Blob:     blob,
		})
	}
	return out, nil
}

func readUvarint(r io.Reader) (uint64, error) {
	var buf [binary.MaxVarintLen64]byte
	for i := range buf {
		if _, err := io.ReadFull(r, buf[i:i+1]); err != nil {
			return 0, err
		}
		if buf[i] < 0x80 {
			v, n := binary.Uvarint(buf[:i+1])
			if n <= 0 {
				return 0, errors.New("api: bad chunk batch length")
			}
			return v, nil
		}
	}
	return 0, errors.New("api: oversized chunk batch length")
}
