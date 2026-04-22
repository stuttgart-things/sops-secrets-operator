package source

import (
	"context"
	"strings"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/stuttgart-things/sops-secrets-operator/internal/git"
)

func TestReadMissingKey(t *testing.T) {
	r := NewRegistry()
	if _, _, err := r.Read(client.ObjectKey{Name: "nope", Namespace: "x"}, "a.yaml"); err == nil {
		t.Fatal("expected error for unknown key")
	}
}

func TestForgetIsIdempotent(t *testing.T) {
	r := NewRegistry()
	// Forget on an empty registry must not panic.
	r.Forget(client.ObjectKey{Name: "nope", Namespace: "x"})
}

func TestEnsureCachedRejectsInvalidConfig(t *testing.T) {
	r := NewRegistry()
	_, err := r.EnsureCached(context.Background(), client.ObjectKey{Name: "x"}, git.Config{})
	if err == nil {
		t.Fatal("expected error for missing URL")
	}
}

// TestForgetEvictsEntry verifies Forget removes the cached entry created
// by EnsureCached so a subsequent Read fails with "no cache for".
// Uses a bogus URL: git.New succeeds (it only validates URL non-empty),
// then EnsureCloned fails on the clone, but the registry has already
// recorded the entry before that point.
func TestForgetEvictsEntry(t *testing.T) {
	r := NewRegistry()
	root := t.TempDir()
	key := client.ObjectKey{Namespace: "x", Name: "y"}

	_, err := r.EnsureCached(context.Background(), key, git.Config{
		URL:       "file:///nonexistent/path-" + t.Name(),
		Branch:    "main",
		CacheRoot: root,
	})
	if err == nil {
		t.Fatal("expected clone failure for bogus URL")
	}

	// Entry should be readable (well, the Read will fail on the underlying
	// repo, but it confirms the registry knows about the key).
	if _, _, err := r.Read(key, "whatever"); err == nil || !strings.Contains(err.Error(), "open") && !strings.Contains(err.Error(), "no such") {
		// The repo dir was never populated, so Read hits os.ReadFile which
		// returns an ENOENT — anything except "no cache for" proves the
		// entry exists in the registry.
		if err != nil && strings.Contains(err.Error(), "no cache for") {
			t.Fatalf("entry was never recorded: %v", err)
		}
	}

	r.Forget(key)

	_, _, err = r.Read(key, "whatever")
	if err == nil || !strings.Contains(err.Error(), "no cache for") {
		t.Fatalf("expected 'no cache for' after Forget, got %v", err)
	}
}
