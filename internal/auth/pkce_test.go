package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
)

func TestGenerateVerifier(t *testing.T) {
	v, err := GenerateVerifier()
	if err != nil {
		t.Fatal(err)
	}
	// RFC 7636: 43–128 URL-safe characters
	if l := len(v); l < 43 || l > 128 {
		t.Fatalf("verifier length %d out of range [43,128]", l)
	}
	const allowed = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	for _, c := range v {
		if !strings.ContainsRune(allowed, c) {
			t.Fatalf("verifier contains invalid char %q", c)
		}
	}
}

func TestChallenge(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	got := Challenge(verifier)

	h := sha256.Sum256([]byte(verifier))
	want := base64.RawURLEncoding.EncodeToString(h[:])

	if got != want {
		t.Fatalf("challenge mismatch: got %q, want %q", got, want)
	}
}

func TestVerifierUniqueness(t *testing.T) {
	v1, _ := GenerateVerifier()
	v2, _ := GenerateVerifier()
	if v1 == v2 {
		t.Fatal("two verifiers must not be equal")
	}
}
