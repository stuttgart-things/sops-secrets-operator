// Package decrypt wraps github.com/getsops/sops/v3 for age-based decryption.
//
// Design note on concurrency: SOPS v3's public decrypt API reads age keys
// from the SOPS_AGE_KEY (or SOPS_AGE_KEY_FILE) environment variable — it
// does not expose an in-memory keysource. To support concurrent reconciles
// with *different* age keys without races, every call serializes through
// ageKeyMu: the key is set, the decrypt is performed, the prior env value
// is restored. Decrypts are short and secret traffic is low, so the cost
// of serialization is negligible. If SOPS later exposes an in-memory
// identity API, switch to that and remove the mutex.
package decrypt

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	sopsdecrypt "github.com/getsops/sops/v3/decrypt"
)

var ageKeyMu sync.Mutex

// DecryptAge decrypts SOPS-encrypted data using the provided age private key.
// The file path is used only to infer the SOPS format (yaml/json/dotenv/binary).
func DecryptAge(content []byte, filePath string, ageKey []byte) ([]byte, error) {
	if len(content) == 0 {
		return nil, errors.New("decrypt: content is empty")
	}
	if len(ageKey) == 0 {
		return nil, errors.New("decrypt: age key is empty")
	}

	ageKeyMu.Lock()
	defer ageKeyMu.Unlock()

	prev, hadPrev := os.LookupEnv("SOPS_AGE_KEY")
	if err := os.Setenv("SOPS_AGE_KEY", string(ageKey)); err != nil {
		return nil, fmt.Errorf("decrypt: set SOPS_AGE_KEY: %w", err)
	}
	defer func() {
		if hadPrev {
			_ = os.Setenv("SOPS_AGE_KEY", prev)
		} else {
			_ = os.Unsetenv("SOPS_AGE_KEY")
		}
	}()

	plaintext, err := sopsdecrypt.Data(content, FormatFromPath(filePath))
	if err != nil {
		return nil, fmt.Errorf("decrypt: sops: %w", err)
	}
	return plaintext, nil
}

// FormatFromPath returns the SOPS format string for a given file path.
func FormatFromPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return "json"
	case ".env", ".ini":
		return "dotenv"
	case ".yml", ".yaml":
		return "yaml"
	default:
		return "binary"
	}
}
