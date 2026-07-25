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

	"github.com/klauspost/compress/zstd"
	"golang.org/x/crypto/chacha20poly1305"
)

const formatVersion = 1

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
	enc *zstd.Encoder
	dec *zstd.Decoder
	key *Key
	// level is retained for reporting only.
	level zstd.EncoderLevel
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
	c.enc.Close()
	c.dec.Close()
}

// Encrypted reports whether this codec seals blobs.
func (c *Codec) Encrypted() bool { return c.key != nil }

// Encode packs plain into a blob.
//
// plainDigest is the BLAKE3 digest of plain. It is used to derive a
// deterministic nonce so that identical plaintext always produces identical
// ciphertext, which is what allows deduplication to keep working with
// encryption enabled. The trade-off is that an attacker holding the same key
// can confirm whether a guessed chunk exists; since the key never leaves the
// user's devices, that is an acceptable price for cross-device dedup.
func (c *Codec) Encode(plain []byte, plainDigest string) ([]byte, error) {
	flags := byte(0)
	payload := c.enc.EncodeAll(plain, nil)
	if len(payload) < len(plain) {
		flags |= flagCompressed
	} else {
		// Already-compressed data (JPEG, MP4, ZIP) gets bigger under Zstd;
		// store it verbatim instead of paying for the expansion.
		payload = plain
	}

	header := make([]byte, 0, 2+binary.MaxVarintLen64+chacha20poly1305.NonceSizeX)
	header = append(header, formatVersion, 0)
	header = binary.AppendUvarint(header, uint64(len(plain)))

	if c.key == nil {
		header[1] = flags
		return append(header, payload...), nil
	}

	flags |= flagEncrypted
	header[1] = flags
	aead, err := chacha20poly1305.NewX(c.key.Data[:])
	if err != nil {
		return nil, err
	}
	nonce, err := c.key.Nonce(plainDigest)
	if err != nil {
		return nil, err
	}
	header = append(header, nonce...)
	// The header is authenticated so flags and length cannot be tampered with.
	return aead.Seal(header, nonce, payload, header), nil
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
