package source

import (
	"context"
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
