// Package codec turns plaintext chunks into the blobs that are stored on the
// server, and back again.
//
// A blob is compressed with Zstandard and, when end-to-end encryption is
// enabled, sealed with XChaCha20-Poly1305. The blob is self-describing so a
// repository can hold a mix of encrypted and unencrypted blobs (for example
// after a user turns encryption on) without a migration.
//
// Wire format:
//
//	byte 0        format version (1)
//	byte 1        flags: bit0 = zstd compressed, bit1 = encrypted
//	uvarint       plaintext length
//	[24]byte      XChaCha20-Poly1305 nonce   (only when encrypted)
//	remainder     payload (ciphertext includes the 16 byte Poly1305 tag)
package codec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"

	"github.com/klauspost/compress/zstd"
	"golang.org/x/crypto/chacha20poly1305"

	"github.com/foisalislambd/openbackup/internal/bufpool"
)

const formatVersion = 1

// maxPlaintextLen bounds the plaintext size a blob header may claim. Chunks are
// at most a few MiB by policy, so a corrupt or hostile header claiming
// gigabytes must be rejected before Decode preallocates from it — otherwise a
// malicious server could OOM or panic every agent that restores.
const maxPlaintextLen = 64 << 20

const (
	flagCompressed = 1 << 0
	flagEncrypted  = 1 << 1
)

// Errors returned by Decode.
var (
	ErrMalformed      = errors.New("codec: malformed blob")
	ErrNeedKey        = errors.New("codec: blob is encrypted but no key is configured")
	ErrUnknownVersion = errors.New("codec: unsupported blob format version")
)

// Codec encodes and decodes blobs. It is safe for concurrent use.
type Codec struct {
	enc   *zstd.Encoder
	dec   *zstd.Decoder
	key   *Key
	level zstd.EncoderLevel

	mu    sync.Mutex
	spare []*EncodeSlot
	live  []*zstd.Encoder
}

// EncodeSlot is one worker's encoder and reusable output buffers. EncodeAll
// on a shared encoder serialises, so each in-flight chunk gets its own.
type EncodeSlot struct {
	enc     *zstd.Encoder
	buf     []byte
	scratch []byte
}

// Options configures a Codec.
type Options struct {
	// Level selects the Zstd level. Zero selects SpeedDefault, which is the
	// sweet spot for a background agent: roughly 3x compression on documents
	// at a few hundred MB/s per core.
	Level zstd.EncoderLevel
	// Key enables end-to-end encryption when non-nil.
	Key *Key
	// Concurrency caps the Zstd worker goroutines. Zero means 1, because the
	// agent parallelises at the file level and extra workers cost memory.
	Concurrency int
}

// New builds a Codec.
func New(opts Options) (*Codec, error) {
	if opts.Level == 0 {
		opts.Level = zstd.SpeedDefault
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 1
	}
	enc, err := zstd.NewWriter(nil,
		zstd.WithEncoderLevel(opts.Level),
		zstd.WithEncoderConcurrency(opts.Concurrency),
		// Chunks are at most a few MiB, so a small window keeps memory flat.
		zstd.WithWindowSize(1<<20),
	)
	if err != nil {
		return nil, err
	}
	dec, err := zstd.NewReader(nil,
		zstd.WithDecoderConcurrency(opts.Concurrency),
		zstd.WithDecoderMaxMemory(64<<20),
	)
	if err != nil {
		enc.Close()
		return nil, err
	}
	return &Codec{enc: enc, dec: dec, key: opts.Key, level: opts.Level}, nil
}

// Close releases the Zstd resources.
func (c *Codec) Close() {
	c.mu.Lock()
	live := c.live
	c.live = nil
	c.spare = nil
	c.mu.Unlock()
	c.enc.Close()
	c.dec.Close()
	for _, enc := range live {
		enc.Close()
	}
}

// Encode packs plain into a blob.
//
// plainDigest is the BLAKE3 digest of plain. It is used to derive a
// deterministic nonce so that identical plaintext always produces identical
// ciphertext, which is what allows deduplication to keep working with
// encryption enabled. The trade-off is that an attacker holding the same key
// can confirm whether a guessed chunk exists; since the key never leaves the
// user's devices, that is an acceptable price for cross-device dedup.
func (c *Codec) Encode(plain []byte, plainDigest string) ([]byte, error) {
	slot := c.acquire()
	defer c.release(slot)
	out, err := c.encode(slot, plain, plainDigest)
	if err != nil {
		return nil, err
	}
	// Callers of Encode may retain the slice, so it cannot alias the slot
	// buffer that the next chunk will reuse.
	return append([]byte(nil), out...), nil
}

// EncodeReuse packs plain into a blob using a caller-owned slot from Acquire.
// The returned slice aliases slot storage and is valid only until the next
// EncodeReuse on that slot or until Release.
func (c *Codec) EncodeReuse(slot *EncodeSlot, plain []byte, plainDigest string) ([]byte, error) {
	if slot == nil {
		return nil, errors.New("codec: nil encode slot")
	}
	return c.encode(slot, plain, plainDigest)
}

// Acquire borrows an encoder. Release returns it.
func (c *Codec) Acquire() *EncodeSlot { return c.acquire() }

// Release returns a slot from Acquire.
func (c *Codec) Release(slot *EncodeSlot) { c.release(slot) }

func (c *Codec) acquire() *EncodeSlot {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n := len(c.spare); n > 0 {
		slot := c.spare[n-1]
		c.spare = c.spare[:n-1]
		return slot
	}
	enc, err := zstd.NewWriter(nil,
		zstd.WithEncoderLevel(c.level),
		zstd.WithEncoderConcurrency(1),
		zstd.WithWindowSize(1<<20),
	)
	if err != nil {
		// Fall back to the shared encoder. EncodeAll on it is safe; it just
		// does not run in parallel with other users of the same encoder.
		return &EncodeSlot{enc: c.enc}
	}
	c.live = append(c.live, enc)
	return &EncodeSlot{enc: enc}
}

func (c *Codec) release(slot *EncodeSlot) {
	if slot == nil || slot.enc == c.enc {
		return
	}
	c.mu.Lock()
	if len(c.spare) < 2 {
		c.spare = append(c.spare, slot)
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	c.discard(slot)
}

func (c *Codec) discard(slot *EncodeSlot) {
	if slot == nil {
		return
	}
	if slot.buf != nil {
		bufpool.Put(slot.buf[:0])
		slot.buf = nil
	}
	if slot.scratch != nil {
		bufpool.Put(slot.scratch[:0])
		slot.scratch = nil
	}
	if slot.enc != nil && slot.enc != c.enc {
		_ = slot.enc.Close()
		c.mu.Lock()
		for i, enc := range c.live {
			if enc == slot.enc {
				c.live = append(c.live[:i], c.live[i+1:]...)
				break
			}
		}
		c.mu.Unlock()
	}
}

// Trim closes idle encoders and drops their buffers. Slots the caller still
// holds stay valid; their encoders remain in live.
func (c *Codec) Trim() {
	c.mu.Lock()
	spare := c.spare
	c.spare = nil
	c.mu.Unlock()
	for _, slot := range spare {
		c.discard(slot)
	}
}

func (c *Codec) encode(slot *EncodeSlot, plain []byte, plainDigest string) ([]byte, error) {
	hdrLen := 2 + uvarintLen(uint64(len(plain)))
	encrypted := c.key != nil
	if encrypted {
		hdrLen += chacha20poly1305.NonceSizeX
	}
	// Zstd can expand incompressible input. Reserve the header plus the worst
	// case the encoder documents (a few dozen bytes over the input).
	need := hdrLen + len(plain) + len(plain)/128 + 128
	if cap(slot.buf) < need {
		if slot.buf != nil {
			bufpool.Put(slot.buf[:0])
		}
		slot.buf = bufpool.Get(need)
	}
	dst := slot.buf[:cap(slot.buf)]

	var payloadLen int
	flags := byte(0)
	if !encrypted {
		compressed := slot.enc.EncodeAll(plain, dst[hdrLen:hdrLen])
		if len(compressed) < len(plain) {
			flags = flagCompressed
			if !writtenInPlace(dst, hdrLen, compressed) {
				copy(dst[hdrLen:], compressed)
			}
			payloadLen = len(compressed)
		} else {
			// Already-compressed data (JPEG, MP4, ZIP) gets bigger under Zstd;
			// store it verbatim instead of paying for the expansion.
			copy(dst[hdrLen:], plain)
			payloadLen = len(plain)
		}
		dst[0] = formatVersion
		dst[1] = flags
		n := binary.PutUvarint(dst[2:], uint64(len(plain)))
		if 2+n != hdrLen {
			return nil, errors.New("codec: header length mismatch")
		}
		return dst[:hdrLen+payloadLen], nil
	}

	if cap(slot.scratch) < len(plain)+len(plain)/128+128 {
		if slot.scratch != nil {
			bufpool.Put(slot.scratch[:0])
		}
		slot.scratch = bufpool.Get(len(plain) + len(plain)/128 + 128)
	}
	compressed := slot.enc.EncodeAll(plain, slot.scratch[:0])
	slot.scratch = compressed
	var payload []byte
	if len(compressed) < len(plain) {
		flags = flagCompressed
		payload = compressed
	} else {
		payload = plain
	}

	dst[0] = formatVersion
	dst[1] = flags | flagEncrypted
	n := binary.PutUvarint(dst[2:], uint64(len(plain)))
	headerLen := 2 + n
	aead, err := chacha20poly1305.NewX(c.key.Data[:])
	if err != nil {
		return nil, err
	}
	nonce, err := c.key.Nonce(plainDigest)
	if err != nil {
		return nil, err
	}
	copy(dst[headerLen:], nonce)
	headerLen += len(nonce)
	header := dst[:headerLen]
	// Seal appends the ciphertext. plaintext and dst must not overlap, which
	// is why encrypted chunks compress into scratch first.
	sealed := aead.Seal(header, nonce, payload, header)
	return sealed, nil
}

func uvarintLen(n uint64) int {
	size := 1
	for n >= 0x80 {
		n >>= 7
		size++
	}
	return size
}

// writtenInPlace reports whether EncodeAll appended into dst at hdrLen.
func writtenInPlace(dst []byte, hdrLen int, compressed []byte) bool {
	if len(compressed) == 0 {
		return true
	}
	if hdrLen >= cap(dst) {
		return false
	}
	return &compressed[0] == &dst[hdrLen]
}

// Decode unpacks a blob produced by Encode.
func (c *Codec) Decode(blob []byte) ([]byte, error) {
	if len(blob) < 3 {
		return nil, ErrMalformed
	}
	if blob[0] != formatVersion {
		return nil, fmt.Errorf("%w: %d", ErrUnknownVersion, blob[0])
	}
	flags := blob[1]
	plainLen, n := binary.Uvarint(blob[2:])
	if n <= 0 {
		return nil, ErrMalformed
	}
	if plainLen > maxPlaintextLen {
		return nil, fmt.Errorf("%w: plaintext length %d exceeds the %d byte limit", ErrMalformed, plainLen, maxPlaintextLen)
	}
	headerLen := 2 + n
	rest := blob[headerLen:]

	if flags&flagEncrypted != 0 {
		if c.key == nil {
			return nil, ErrNeedKey
		}
		if len(rest) < chacha20poly1305.NonceSizeX {
			return nil, ErrMalformed
		}
		nonce := rest[:chacha20poly1305.NonceSizeX]
		ciphertext := rest[chacha20poly1305.NonceSizeX:]
		aead, err := chacha20poly1305.NewX(c.key.Data[:])
		if err != nil {
			return nil, err
		}
		aad := blob[:headerLen+chacha20poly1305.NonceSizeX]
		plain, err := aead.Open(nil, nonce, ciphertext, aad)
		if err != nil {
			return nil, fmt.Errorf("codec: decrypt: %w", err)
		}
		rest = plain
	}

	if flags&flagCompressed == 0 {
		if uint64(len(rest)) != plainLen {
			return nil, ErrMalformed
		}
		return rest, nil
	}
	out, err := c.dec.DecodeAll(rest, make([]byte, 0, plainLen))
	if err != nil {
		return nil, fmt.Errorf("codec: decompress: %w", err)
	}
	if uint64(len(out)) != plainLen {
		return nil, ErrMalformed
	}
	return out, nil
}

// IsEncrypted reports whether a stored blob is sealed, without needing a key.
func IsEncrypted(blob []byte) bool {
	return len(blob) >= 2 && blob[1]&flagEncrypted != 0
}
