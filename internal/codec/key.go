package codec

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/zeebo/blake3"
	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"

	"github.com/openbackup/openbackup/internal/hash"
)

// Key sizes and derivation contexts. The context strings are part of the
// format and must not change.
const (
	KeySize = 32

	ctxNonce = "openbackup 2024 chunk-nonce"
	ctxID    = "openbackup 2024 key-id"
	ctxWrap  = "openbackup 2024 key-wrap"
)

// Argon2id parameters for passphrase derivation. These are sized so that
// unlocking takes well under a second on a laptop while costing an attacker
// 64 MiB of memory per guess.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 4
	saltSize     = 16
)

// Key is a repository master key. It never leaves the user's devices in
// plaintext; the server only ever sees a passphrase-wrapped copy.
type Key struct {
	Data [KeySize]byte
	// nonceKey is a subkey used to derive deterministic per-chunk nonces.
	nonceKey [KeySize]byte
}

// NewRandomKey generates a fresh master key.
func NewRandomKey() (*Key, error) {
	var raw [KeySize]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, err
	}
	return KeyFromBytes(raw[:])
}

// KeyFromBytes adopts raw key material.
func KeyFromBytes(raw []byte) (*Key, error) {
	if len(raw) != KeySize {
		return nil, fmt.Errorf("codec: key must be %d bytes, got %d", KeySize, len(raw))
	}
	k := &Key{}
	copy(k.Data[:], raw)
	blake3.DeriveKey(ctxNonce, k.Data[:], k.nonceKey[:])
	return k, nil
}

// DeriveKeyFromPassphrase stretches a human passphrase into a master key.
// Passing a nil salt generates one, which is what happens during enrolment;
// the salt is public and is stored alongside the account.
func DeriveKeyFromPassphrase(passphrase string, salt []byte) (*Key, []byte, error) {
	if len(passphrase) < 8 {
		return nil, nil, errors.New("codec: passphrase must be at least 8 characters")
	}
	if salt == nil {
		salt = make([]byte, saltSize)
		if _, err := rand.Read(salt); err != nil {
			return nil, nil, err
		}
	}
	raw := argon2.IDKey([]byte(passphrase), salt, argonTime, argonMemory, argonThreads, KeySize)
	k, err := KeyFromBytes(raw)
	if err != nil {
		return nil, nil, err
	}
	return k, salt, nil
}

// ID returns a public, non-secret identifier for the key. Devices send it when
// enrolling so the server can refuse a device configured with the wrong
// passphrase before it writes undecryptable blobs.
func (k *Key) ID() string {
	var out [16]byte
	blake3.DeriveKey(ctxID, k.Data[:], out[:])
	return hex.EncodeToString(out[:])
}

// Nonce derives the deterministic 24 byte nonce for a chunk.
func (k *Key) Nonce(plainDigest string) ([]byte, error) {
	if err := hash.Validate(plainDigest); err != nil {
		return nil, err
	}
	h, err := blake3.NewKeyed(k.nonceKey[:])
	if err != nil {
		return nil, err
	}
	if _, err := h.WriteString(plainDigest); err != nil {
		return nil, err
	}
	nonce := make([]byte, chacha20poly1305.NonceSizeX)
	h.Digest().Read(nonce)
	return nonce, nil
}

// RecoveryCode renders the key as a human-transcribable string. Users are told
// to store it; without it (or the passphrase) encrypted backups are lost, which
// is the honest consequence of real end-to-end encryption.
func (k *Key) RecoveryCode() string {
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(k.Data[:])
	var b strings.Builder
	for i, r := range enc {
		if i > 0 && i%8 == 0 {
			b.WriteByte('-')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// KeyFromRecoveryCode parses the output of RecoveryCode.
func KeyFromRecoveryCode(code string) (*Key, error) {
	clean := strings.ToUpper(strings.NewReplacer("-", "", " ", "", "\n", "", "\r", "").Replace(code))
	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(clean)
	if err != nil {
		return nil, fmt.Errorf("codec: invalid recovery code: %w", err)
	}
	return KeyFromBytes(raw)
}

// Wrap seals the master key with a passphrase-derived key so it can be stored
// server-side for cross-device restore. The server cannot open it.
func (k *Key) Wrap(passphrase string) (wrapped, salt []byte, err error) {
	kek, salt, err := DeriveKeyFromPassphrase(passphrase, nil)
	if err != nil {
		return nil, nil, err
	}
	var wrapKey [KeySize]byte
	blake3.DeriveKey(ctxWrap, kek.Data[:], wrapKey[:])
	aead, err := chacha20poly1305.NewX(wrapKey[:])
	if err != nil {
		return nil, nil, err
	}
	nonce := make([]byte, chacha20poly1305.NonceSizeX)
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return aead.Seal(nonce, nonce, k.Data[:], []byte(ctxWrap)), salt, nil
}

// UnwrapKey reverses Wrap.
func UnwrapKey(wrapped, salt []byte, passphrase string) (*Key, error) {
	kek, _, err := DeriveKeyFromPassphrase(passphrase, salt)
	if err != nil {
		return nil, err
	}
	var wrapKey [KeySize]byte
	blake3.DeriveKey(ctxWrap, kek.Data[:], wrapKey[:])
	aead, err := chacha20poly1305.NewX(wrapKey[:])
	if err != nil {
		return nil, err
	}
	if len(wrapped) < chacha20poly1305.NonceSizeX {
		return nil, ErrMalformed
	}
	nonce, ct := wrapped[:chacha20poly1305.NonceSizeX], wrapped[chacha20poly1305.NonceSizeX:]
	raw, err := aead.Open(nil, nonce, ct, []byte(ctxWrap))
	if err != nil {
		return nil, errors.New("codec: wrong passphrase")
	}
	return KeyFromBytes(raw)
}

// Equal compares two keys in constant time.
func (k *Key) Equal(other *Key) bool {
	if k == nil || other == nil {
		return k == other
	}
	return subtle.ConstantTimeCompare(k.Data[:], other.Data[:]) == 1
}
