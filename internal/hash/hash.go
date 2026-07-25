// Package hash centralises the content addressing scheme used by OpenBackup.
//
// Every chunk, file and snapshot object is addressed by a BLAKE3-256 digest
// rendered as lowercase hex. BLAKE3 is used because it is several times faster
// than SHA-256 on the low-power devices the agent runs on, which keeps CPU
// usage inside the "<1% idle" budget.
package hash

import (
	"encoding/hex"
	"errors"
	"io"

	"github.com/zeebo/blake3"
)

// Size is the digest length in bytes.
const Size = 32

// HexLen is the digest length when hex encoded.
const HexLen = Size * 2

// ErrInvalidDigest is returned when a caller supplies a malformed digest.
var ErrInvalidDigest = errors.New("hash: invalid digest")

// Sum returns the hex digest of b.
func Sum(b []byte) string {
	sum := blake3.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Hasher incrementally hashes data.
type Hasher struct{ h *blake3.Hasher }

// NewHasher returns a reusable incremental hasher.
func NewHasher() *Hasher { return &Hasher{h: blake3.New()} }

// Write feeds data into the hasher.
func (h *Hasher) Write(p []byte) (int, error) { return h.h.Write(p) }

// Hex returns the current digest as hex without resetting.
func (h *Hasher) Hex() string {
	var out [Size]byte
	h.h.Digest().Read(out[:])
	return hex.EncodeToString(out[:])
}

// Reset prepares the hasher for reuse.
func (h *Hasher) Reset() { h.h.Reset() }

// Stream hashes everything readable from r and reports the byte count.
func Stream(r io.Reader) (string, int64, error) {
	h := blake3.New()
	n, err := io.Copy(h, r)
	if err != nil {
		return "", n, err
	}
	var out [Size]byte
	h.Digest().Read(out[:])
	return hex.EncodeToString(out[:]), n, nil
}

// Validate checks that s looks like a digest produced by this package.
func Validate(s string) error {
	if len(s) != HexLen {
		return ErrInvalidDigest
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return ErrInvalidDigest
		}
	}
	return nil
}
