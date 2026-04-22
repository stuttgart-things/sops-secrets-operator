package testutil

import (
	"testing"

	"github.com/stuttgart-things/sops-secrets-operator/internal/decrypt"
)

// TestEncryptRoundTrip verifies EncryptYAML produces ciphertext that our
// own decrypt.DecryptAge round-trips — catches version/format drift
// between getsops/sops and our wrapper.
func TestEncryptYAMLRoundTrip(t *testing.T) {
	key := GenerateAge(t)
	plain := []byte("username: alice\npassword: s3cret\n")

	ct := EncryptYAML(t, key.PublicKey, plain)
	if len(ct) == 0 {
		t.Fatal("empty ciphertext")
	}
	got, err := decrypt.DecryptAge(ct, "x.yaml", []byte(key.PrivateKey))
	if err != nil {
		t.Fatalf("round-trip decrypt: %v", err)
	}
	if string(got) != string(plain) {
		t.Fatalf("round-trip mismatch:\nwant: %q\ngot:  %q", plain, got)
	}
}
