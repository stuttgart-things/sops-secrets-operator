// Package transform contains pure helpers that convert decrypted SOPS
// output into the data map of a Kubernetes Secret. Kept in its own
// package so unit tests do not drag in the controller package's envtest
// BeforeSuite.
package transform

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"

	"sigs.k8s.io/yaml"

	sopsv1alpha1 "github.com/stuttgart-things/sops-secrets-operator/api/v1alpha1"
)

// ParseFlatYAML unmarshals plaintext as a top-level map of scalars.
// Returns an error if any top-level value is not a scalar — this is the
// "flat-only" guard that enforces the v1alpha1 input contract and
// prevents silent partial mappings.
func ParseFlatYAML(plaintext []byte) (map[string]string, error) {
	var raw map[string]any
	if err := yaml.Unmarshal(plaintext, &raw); err != nil {
		return nil, fmt.Errorf("parse decrypted yaml: %w", err)
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		s, err := scalarToString(v)
		if err != nil {
			return nil, fmt.Errorf("key %q: %w", k, err)
		}
		out[k] = s
	}
	return out, nil
}

func scalarToString(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case bool:
		return strconv.FormatBool(x), nil
	case float64:
		// sigs.k8s.io/yaml decodes numbers via encoding/json, which yields
		// float64. Render integers without a decimal point so e.g.
		// DB_PORT=5432 stays "5432".
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10), nil
		}
		return strconv.FormatFloat(x, 'f', -1, 64), nil
	case int:
		return strconv.Itoa(x), nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case nil:
		return "", fmt.Errorf("value is null")
	case map[string]any, []any:
		return "", fmt.Errorf("value is non-scalar (map or list) — flat YAML only")
	default:
		return "", fmt.Errorf("unsupported value type %T", v)
	}
}

// ApplyMapping selects and renames keys according to the CR's Data slice.
// Fails closed when any mapped source key is missing.
func ApplyMapping(source map[string]string, mappings []sopsv1alpha1.DataMapping) (map[string][]byte, error) {
	out := make(map[string][]byte, len(mappings))
	for _, m := range mappings {
		val, ok := source[m.From]
		if !ok {
			return nil, fmt.Errorf("source file missing key %q referenced by data mapping (target key %q)", m.From, m.Key)
		}
		out[m.Key] = []byte(val)
	}
	return out, nil
}

// HashSecretData returns a deterministic SHA-256 over the Secret data map.
// Keys are sorted so the hash is stable across runs.
func HashSecretData(data map[string][]byte) string {
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		_, _ = h.Write([]byte(k))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(data[k])
		_, _ = h.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
