package codec

import (
	"bytes"
	"testing"

	"github.com/foisalislambd/openbackup/internal/hash"
)

func TestEncodeReuseMatchesEncode(t *testing.T) {
	plain := bytes.Repeat([]byte("reusable encoder "), 3000)
	digest := hash.Sum(plain)
	for _, key := range []*Key{nil, mustKey(t)} {
		c := mustCodec(t, key)
		want, err := c.Encode(plain, digest)
		if err != nil {
			t.Fatal(err)
		}
		slot := c.Acquire()
		got, err := c.EncodeReuse(slot, plain, digest)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("reused encoder produced a different blob (encrypted=%v)", key != nil)
		}
		decoded, err := c.Decode(append([]byte(nil), got...))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(decoded, plain) {
			t.Fatal("decode mismatch")
		}
		c.Release(slot)
	}
}

func mustKey(t *testing.T) *Key {
	t.Helper()
	key, err := NewRandomKey()
	if err != nil {
		t.Fatal(err)
	}
	return key
}
