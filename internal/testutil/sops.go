// Package testutil contains helpers shared across test binaries.
//
// SOPS/age encryption at test time: GenerateAge returns a fresh
// X25519 age identity and EncryptYAML emits a SOPS-encrypted YAML
// document that can be round-tripped by internal/decrypt.DecryptAge.
// These helpers should never be compiled into production binaries.
package testutil

import (
	"filippo.io/age"
	sops "github.com/getsops/sops/v3"
	"github.com/getsops/sops/v3/aes"
	sopsage "github.com/getsops/sops/v3/age"
	"github.com/getsops/sops/v3/cmd/sops/common"
	"github.com/getsops/sops/v3/stores/json"
	"github.com/getsops/sops/v3/stores/yaml"
	"github.com/getsops/sops/v3/version"
)

// TestingT is the minimal subset of TestingT / Ginkgo's GinkgoT() that
// these helpers need. Both *testing.T and GinkgoT() satisfy it, so the
// helpers work in plain Go tests and Ginkgo specs.
type TestingT interface {
	Helper()
	Fatalf(format string, args ...any)
}

// AgeKey is the result of GenerateAge.
type AgeKey struct {
	// PrivateKey is the Bech32-encoded age identity (AGE-SECRET-KEY-...).
	PrivateKey string
	// PublicKey is the Bech32-encoded age recipient (age1...).
	PublicKey string
}

// GenerateAge returns a fresh X25519 age identity. Fatals the test on error.
func GenerateAge(tb TestingT) AgeKey {
	tb.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		tb.Fatalf("testutil: generate age identity: %v", err)
	}
	return AgeKey{
		PrivateKey: id.String(),
		PublicKey:  id.Recipient().String(),
	}
}

// EncryptYAML SOPS-encrypts a plain YAML document for the given age
// recipient. The output is a full SOPS-encrypted YAML blob suitable
// for passing to decrypt.DecryptAge with a yaml-format hint.
func EncryptYAML(tb TestingT, agePublicKey string, plaintext []byte) []byte {
	tb.Helper()
	store := &yaml.Store{}
	return encryptWithStore(tb, store, store, agePublicKey, plaintext)
}

// EncryptJSON SOPS-encrypts a plain JSON document for the given age
// recipient.
func EncryptJSON(tb TestingT, agePublicKey string, plaintext []byte) []byte {
	tb.Helper()
	store := &json.Store{}
	return encryptWithStore(tb, store, store, agePublicKey, plaintext)
}

func encryptWithStore(tb TestingT, inStore, outStore sops.Store, agePublicKey string, plaintext []byte) []byte {
	tb.Helper()
	branches, err := inStore.LoadPlainFile(plaintext)
	if err != nil {
		tb.Fatalf("testutil: parse plaintext: %v", err)
	}
	mk, err := sopsage.MasterKeyFromRecipient(agePublicKey)
	if err != nil {
		tb.Fatalf("testutil: age master key: %v", err)
	}
	tree := sops.Tree{
		Branches: branches,
		Metadata: sops.Metadata{
			KeyGroups: []sops.KeyGroup{{mk}},
			Version:   version.Version,
		},
	}
	dataKey, errs := tree.GenerateDataKey()
	if len(errs) > 0 {
		tb.Fatalf("testutil: generate data key: %v", errs)
	}
	if err := common.EncryptTree(common.EncryptTreeOpts{
		DataKey: dataKey,
		Tree:    &tree,
		Cipher:  aes.NewCipher(),
	}); err != nil {
		tb.Fatalf("testutil: encrypt tree: %v", err)
	}
	out, err := outStore.EmitEncryptedFile(tree)
	if err != nil {
		tb.Fatalf("testutil: emit encrypted file: %v", err)
	}
	return out
}
