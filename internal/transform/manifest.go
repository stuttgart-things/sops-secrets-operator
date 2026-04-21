package transform

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"
)

// allowedTopLevelManifestKeys is the whitelist of top-level fields we accept
// in a decrypted Secret manifest. Anything else (spec, ownerReferences,
// finalizers, status, etc.) is rejected to prevent surprising behaviour.
var allowedTopLevelManifestKeys = map[string]bool{
	"apiVersion": true,
	"kind":       true,
	"metadata":   true,
	"type":       true,
	"data":       true,
	"stringData": true,
}

// allowedMetadataKeys is the whitelist of metadata sub-keys we accept in
// a decrypted Secret manifest.
var allowedMetadataKeys = map[string]bool{
	"name":        true,
	"namespace":   true,
	"labels":      true,
	"annotations": true,
}

// ParseManifest parses a decrypted YAML document as a Kubernetes Secret.
// It enforces the SopsSecretManifest v1alpha1 contract:
//   - apiVersion: v1, kind: Secret
//   - only whitelisted top-level and metadata fields are present
//
// Use the returned *corev1.Secret as a starting point; the caller is
// expected to override namespace authoritatively before applying.
func ParseManifest(plaintext []byte) (*corev1.Secret, error) {
	var raw map[string]any
	if err := yaml.Unmarshal(plaintext, &raw); err != nil {
		return nil, fmt.Errorf("parse decrypted manifest: %w", err)
	}

	for k := range raw {
		if !allowedTopLevelManifestKeys[k] {
			return nil, fmt.Errorf("manifest contains unsupported top-level field %q", k)
		}
	}

	apiVersion, _ := raw["apiVersion"].(string)
	kind, _ := raw["kind"].(string)
	if apiVersion != "v1" || kind != "Secret" {
		return nil, fmt.Errorf("manifest is not a core/v1 Secret (got apiVersion=%q kind=%q)", apiVersion, kind)
	}

	if meta, ok := raw["metadata"].(map[string]any); ok {
		for k := range meta {
			if !allowedMetadataKeys[k] {
				return nil, fmt.Errorf("manifest metadata contains unsupported field %q", k)
			}
		}
	}

	var sec corev1.Secret
	if err := yaml.Unmarshal(plaintext, &sec); err != nil {
		return nil, fmt.Errorf("decode manifest as Secret: %w", err)
	}
	return &sec, nil
}

// NormalizeSecretData merges stringData into data (byte-encoded) and clears
// stringData, producing a single authoritative data map. Called before
// hashing and before apply so drift detection is consistent.
func NormalizeSecretData(sec *corev1.Secret) {
	if sec.Data == nil && len(sec.StringData) > 0 {
		sec.Data = make(map[string][]byte, len(sec.StringData))
	}
	for k, v := range sec.StringData {
		sec.Data[k] = []byte(v)
	}
	sec.StringData = nil
}

// HashManifestSecret returns a deterministic SHA-256 over the Secret's
// type and data. stringData must have been merged into data first via
// NormalizeSecretData.
func HashManifestSecret(sec *corev1.Secret) string {
	keys := make([]string, 0, len(sec.Data))
	for k := range sec.Data {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	h := sha256.New()
	_, _ = h.Write([]byte("type="))
	_, _ = h.Write([]byte(sec.Type))
	_, _ = h.Write([]byte{0})
	for _, k := range keys {
		_, _ = h.Write([]byte(k))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(sec.Data[k])
		_, _ = h.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
