// Package idgen produces the identifiers and secrets used across OpenBackup.
package idgen

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"strings"
	"time"
)

var encoding = base32.NewEncoding("0123456789abcdefghjkmnpqrstvwxyz").WithPadding(base32.NoPadding)

// New returns a 26 character sortable identifier in the style of ULID: a
// 48-bit millisecond timestamp followed by 80 bits of randomness. Sortable ids
// keep SQLite's B-tree inserts sequential and make snapshot listings naturally
// ordered without an extra index.
func New() string {
	var buf [16]byte
	ms := uint64(time.Now().UTC().UnixMilli())
	buf[0] = byte(ms >> 40)
	buf[1] = byte(ms >> 32)
	buf[2] = byte(ms >> 24)
	buf[3] = byte(ms >> 16)
	buf[4] = byte(ms >> 8)
	buf[5] = byte(ms)
	if _, err := rand.Read(buf[6:]); err != nil {
		// crypto/rand cannot fail on any supported platform, but never emit a
		// predictable id if it somehow does.
		binary.BigEndian.PutUint64(buf[6:14], uint64(time.Now().UnixNano()))
	}
	return encoding.EncodeToString(buf[:])
}

// NewPrefixed returns an identifier tagged for readability in logs, for example
// "dev_01h2x...".
func NewPrefixed(prefix string) string {
	return prefix + "_" + New()
}

// Secret returns a URL-safe random secret with the requested entropy in bytes.
// Device tokens use 32 bytes, join tokens 16.
func Secret(n int) (string, error) {
	if n <= 0 {
		n = 32
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return encoding.EncodeToString(buf), nil
}

// JoinCode returns a short, human-typable enrolment code such as
// "K7QM-2F9X-B4TD". Ambiguous characters are already excluded by the alphabet,
// so a code read aloud over the phone still works.
func JoinCode() (string, error) {
	raw, err := Secret(10)
	if err != nil {
		return "", err
	}
	raw = strings.ToUpper(raw[:12])
	return raw[:4] + "-" + raw[4:8] + "-" + raw[8:12], nil
}

// NormalizeJoinCode strips the formatting a user may have typed.
func NormalizeJoinCode(code string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "", "\t", "").Replace(strings.TrimSpace(code)))
}
