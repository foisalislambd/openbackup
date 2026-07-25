package auth

import (
	"strings"
	"testing"
)

func TestPasswordRoundTrip(t *testing.T) {
	hash, err := HashPassword("a-perfectly-fine-password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if strings.Contains(hash, "a-perfectly-fine-password") {
		t.Fatal("the hash must not contain the password")
	}
	if err := VerifyPassword("a-perfectly-fine-password", hash); err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if err := VerifyPassword("a-perfectly-fine-passwerd", hash); err == nil {
		t.Fatal("expected verification to fail for the wrong password")
	}
}

func TestHashesAreSalted(t *testing.T) {
	a, err := HashPassword("the same password twice")
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashPassword("the same password twice")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("two hashes of the same password must differ")
	}
}

func TestShortPasswordRejected(t *testing.T) {
	if _, err := HashPassword("short"); err == nil {
		t.Fatal("expected short passwords to be rejected")
	}
}

func TestVerifyRejectsMalformedHash(t *testing.T) {
	for _, bad := range []string{"", "plaintext", "$argon2i$v=19$m=1,t=1,p=1$aaaa$bbbb", "$argon2id$x$y$z"} {
		if err := VerifyPassword("whatever", bad); err == nil {
			t.Errorf("expected %q to be rejected", bad)
		}
	}
}

func TestHashTokenIsStableAndOpaque(t *testing.T) {
	token := "abc123def456"
	h := HashToken(token)
	if h == token || strings.Contains(h, token) {
		t.Fatal("the token hash must not reveal the token")
	}
	if h != HashToken(token) {
		t.Fatal("hashing must be deterministic")
	}
	if h == HashToken(token+"x") {
		t.Fatal("different tokens must hash differently")
	}
	if len(h) != 64 {
		t.Fatalf("expected a 32 byte hex digest, got %d characters", len(h))
	}
}

func TestBearerToken(t *testing.T) {
	cases := map[string]string{
		"Bearer abc":  "abc",
		"bearer abc":  "abc",
		"Bearer  abc": "abc",
		"Basic abc":   "",
		"":            "",
		"Bearer":      "",
	}
	for header, want := range cases {
		if got := BearerToken(header); got != want {
			t.Errorf("BearerToken(%q) = %q, want %q", header, got, want)
		}
	}
}
