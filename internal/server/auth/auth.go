// Package auth implements password hashing and the token handling shared by the
// dashboard and the agent API.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/zeebo/blake3"
	"golang.org/x/crypto/argon2"
)

// Argon2id parameters for account passwords. 64 MiB and three passes take about
// 50 ms on a small VPS core, which is a sensible ceiling for a login endpoint
// while making offline cracking expensive.
const (
	pwTime    uint32 = 3
	pwMemory  uint32 = 64 * 1024
	pwThreads uint8  = 4
	pwKeyLen  uint32 = 32
	pwSaltLen        = 16
)

// ErrInvalidHash means a stored hash is not in the expected format.
var ErrInvalidHash = errors.New("auth: invalid password hash")

// MinPasswordLength is enforced on registration and password changes.
const MinPasswordLength = 10

// HashPassword returns a self-describing Argon2id hash. Encoding the parameters
// means the cost can be raised later without invalidating existing passwords.
func HashPassword(password string) (string, error) {
	if len(password) < MinPasswordLength {
		return "", fmt.Errorf("auth: password must be at least %d characters", MinPasswordLength)
	}
	salt := make([]byte, pwSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, pwTime, pwMemory, pwThreads, pwKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, pwMemory, pwTime, pwThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword checks a password against an encoded hash in constant time.
func VerifyPassword(password, encoded string) error {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return ErrInvalidHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return ErrInvalidHash
	}
	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return ErrInvalidHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return ErrInvalidHash
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return ErrInvalidHash
	}
	got := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(want)))
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return errors.New("auth: password does not match")
	}
	return nil
}

// HashToken derives the stored form of a bearer token.
//
// Device tokens, session cookies and join codes are high-entropy random strings,
// so a fast keyed hash is the right primitive: there is nothing to brute force,
// and hashing keeps a database dump from yielding usable credentials. Argon2
// here would only add latency to every single API request.
func HashToken(token string) string {
	sum := blake3.Sum256([]byte("openbackup token v1|" + token))
	return hex.EncodeToString(sum[:])
}

// TokensEqual compares two token hashes in constant time.
func TokensEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// BearerToken extracts a token from an Authorization header value.
func BearerToken(header string) string {
	const prefix = "Bearer "
	if len(header) > len(prefix) && strings.EqualFold(header[:len(prefix)], prefix) {
		return strings.TrimSpace(header[len(prefix):])
	}
	return ""
}
