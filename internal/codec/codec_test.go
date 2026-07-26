package codec

import (
	"bytes"
	"crypto/rand"
	"strings"
	"testing"

	"github.com/foisalislambd/openbackup/internal/hash"
)

func mustCodec(t *testing.T, key *Key) *Codec {
	t.Helper()
	c, err := New(Options{Key: key})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(c.Close)
	return c
}

func TestRoundTripPlain(t *testing.T) {
	c := mustCodec(t, nil)
	plain := bytes.Repeat([]byte("openbackup compresses well "), 4096)
	blob, err := c.Encode(plain, hash.Sum(plain))
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(blob) >= len(plain) {
		t.Fatalf("expected compression, blob %d >= plain %d", len(blob), len(plain))
	}
	if IsEncrypted(blob) {
		t.Fatal("blob should not be marked encrypted")
	}
	got, err := c.Decode(blob)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatal("round trip mismatch")
	}
}

func TestRoundTripEncrypted(t *testing.T) {
	key, err := NewRandomKey()
	if err != nil {
		t.Fatal(err)
	}
	c := mustCodec(t, key)
	plain := []byte("private family photos")
	digest := hash.Sum(plain)

	blob, err := c.Encode(plain, digest)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !IsEncrypted(blob) {
		t.Fatal("blob should be marked encrypted")
	}
	if bytes.Contains(blob, plain) {
		t.Fatal("plaintext leaked into the blob")
	}
	got, err := c.Decode(blob)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatal("round trip mismatch")
	}
}

// Deterministic ciphertext is what keeps deduplication working under
// encryption, so it is a documented guarantee rather than an accident.
func TestEncryptionIsDeterministic(t *testing.T) {
	key, err := NewRandomKey()
	if err != nil {
		t.Fatal(err)
	}
	c := mustCodec(t, key)
	plain := bytes.Repeat([]byte("dedup me"), 1000)
	digest := hash.Sum(plain)

	first, err := c.Encode(plain, digest)
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.Encode(plain, digest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("identical plaintext must produce identical blobs")
	}
}

func TestDecodeRejectsTampering(t *testing.T) {
	key, err := NewRandomKey()
	if err != nil {
		t.Fatal(err)
	}
	c := mustCodec(t, key)
	plain := bytes.Repeat([]byte("integrity matters"), 100)
	blob, err := c.Encode(plain, hash.Sum(plain))
	if err != nil {
		t.Fatal(err)
	}

	tampered := bytes.Clone(blob)
	tampered[len(tampered)-1] ^= 0xff
	if _, err := c.Decode(tampered); err == nil {
		t.Fatal("expected authentication failure for flipped ciphertext bit")
	}

	headerTampered := bytes.Clone(blob)
	headerTampered[1] ^= flagCompressed
	if _, err := c.Decode(headerTampered); err == nil {
		t.Fatal("expected authentication failure for flipped header flag")
	}
}

func TestDecodeEncryptedWithoutKey(t *testing.T) {
	key, err := NewRandomKey()
	if err != nil {
		t.Fatal(err)
	}
	sealed := mustCodec(t, key)
	plain := []byte("secret")
	blob, err := sealed.Encode(plain, hash.Sum(plain))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mustCodec(t, nil).Decode(blob); err != ErrNeedKey {
		t.Fatalf("expected ErrNeedKey, got %v", err)
	}
}

func TestWrongKeyCannotDecode(t *testing.T) {
	k1, _ := NewRandomKey()
	k2, _ := NewRandomKey()
	plain := []byte("mine only")
	blob, err := mustCodec(t, k1).Encode(plain, hash.Sum(plain))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mustCodec(t, k2).Decode(blob); err == nil {
		t.Fatal("a different key must not decode the blob")
	}
}

func TestIncompressibleDataIsStoredVerbatim(t *testing.T) {
	c := mustCodec(t, nil)
	plain := make([]byte, 1<<16)
	if _, err := rand.Read(plain); err != nil {
		t.Fatal(err)
	}
	blob, err := c.Encode(plain, hash.Sum(plain))
	if err != nil {
		t.Fatal(err)
	}
	// Header overhead only; no Zstd expansion.
	if len(blob) > len(plain)+16 {
		t.Fatalf("random data expanded from %d to %d bytes", len(plain), len(blob))
	}
	got, err := c.Decode(blob)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatal("round trip mismatch")
	}
}

func TestPassphraseDerivationIsStable(t *testing.T) {
	k1, salt, err := DeriveKeyFromPassphrase("correct horse battery", nil)
	if err != nil {
		t.Fatal(err)
	}
	k2, _, err := DeriveKeyFromPassphrase("correct horse battery", salt)
	if err != nil {
		t.Fatal(err)
	}
	if !k1.Equal(k2) {
		t.Fatal("same passphrase and salt must derive the same key")
	}
	if k1.ID() != k2.ID() {
		t.Fatal("key IDs must match")
	}
	k3, _, err := DeriveKeyFromPassphrase("wrong horse battery", salt)
	if err != nil {
		t.Fatal(err)
	}
	if k1.Equal(k3) {
		t.Fatal("different passphrases must derive different keys")
	}
}

func TestShortPassphraseRejected(t *testing.T) {
	if _, _, err := DeriveKeyFromPassphrase("short", nil); err == nil {
		t.Fatal("expected rejection of a short passphrase")
	}
}

func TestRecoveryCodeRoundTrip(t *testing.T) {
	key, err := NewRandomKey()
	if err != nil {
		t.Fatal(err)
	}
	code := key.RecoveryCode()
	if !strings.Contains(code, "-") {
		t.Fatalf("expected grouped recovery code, got %q", code)
	}
	restored, err := KeyFromRecoveryCode(strings.ToLower(code))
	if err != nil {
		t.Fatalf("KeyFromRecoveryCode: %v", err)
	}
	if !key.Equal(restored) {
		t.Fatal("recovery code did not restore the key")
	}
}

func TestKeyWrapRoundTrip(t *testing.T) {
	key, err := NewRandomKey()
	if err != nil {
		t.Fatal(err)
	}
	wrapped, salt, err := key.Wrap("a good long passphrase")
	if err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	if bytes.Contains(wrapped, key.Data[:]) {
		t.Fatal("wrapped key exposes the master key")
	}
	got, err := UnwrapKey(wrapped, salt, "a good long passphrase")
	if err != nil {
		t.Fatalf("UnwrapKey: %v", err)
	}
	if !key.Equal(got) {
		t.Fatal("unwrap mismatch")
	}
	if _, err := UnwrapKey(wrapped, salt, "a bad long passphrase"); err == nil {
		t.Fatal("expected unwrap failure with the wrong passphrase")
	}
}
