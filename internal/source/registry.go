// Package source maintains per-GitRepository caches and exposes file reads
// to consumer controllers (SopsSecret, SopsSecretManifest).
//
// The Registry is owned by main and shared across reconcilers. The
// GitRepository controller calls EnsureCached on each reconcile to keep
// the cache warm; consumer controllers call Read to fetch file bytes
// plus the commit SHA at read time. Each entry holds an RWMutex so
// updates and reads interleave safely.
package source

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/stuttgart-things/sops-secrets-operator/internal/git"
	"github.com/stuttgart-things/sops-secrets-operator/internal/metrics"
)

// Registry coordinates source caches across reconcilers. It holds both
// git caches (keyed by GitRepository key) and object caches (keyed by
// ObjectSource key), guarded by a single top-level mutex.
type Registry struct {
	mu      sync.Mutex
	entries map[client.ObjectKey]*entry
	objects map[client.ObjectKey]*objectEntry
}

type entry struct {
	rw        sync.RWMutex
	repo      *git.Repo
	cacheDir  string
	commitSHA string
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		entries: make(map[client.ObjectKey]*entry),
		objects: make(map[client.ObjectKey]*objectEntry),
	}
}

// EnsureCached updates the cache for the GitRepository identified by key
// and returns the resolved commit SHA. If the effective cache directory
// has changed (e.g. URL/Branch/Revision edited), the old entry is evicted.
func (r *Registry) EnsureCached(ctx context.Context, key client.ObjectKey, cfg git.Config) (_ string, retErr error) {
	start := time.Now()
	defer func() {
		result := metrics.ResultSuccess
		if retErr != nil {
			result = metrics.ResultError
		}
		metrics.GitFetchDurationSeconds.WithLabelValues(result).Observe(time.Since(start).Seconds())
	}()

	repo, err := git.New(cfg)
	if err != nil {
		return "", err
	}

	r.mu.Lock()
	e, exists := r.entries[key]
	if !exists || e.cacheDir != repo.CacheDir() {
		if exists {
			_ = os.RemoveAll(e.cacheDir)
		}
		e = &entry{repo: repo, cacheDir: repo.CacheDir()}
		r.entries[key] = e
	}
	r.mu.Unlock()

	e.rw.Lock()
	defer e.rw.Unlock()

	sha, err := e.repo.EnsureCloned(ctx)
	if err != nil {
		return "", err
	}
	e.commitSHA = sha
	return sha, nil
}

// Read returns the file bytes at path within the repository identified
// by key, plus the commit SHA observed at read time.
func (r *Registry) Read(key client.ObjectKey, path string) ([]byte, string, error) {
	r.mu.Lock()
	e, ok := r.entries[key]
	r.mu.Unlock()
	if !ok {
		return nil, "", fmt.Errorf("source: no cache for GitRepository %s", key)
	}

	e.rw.RLock()
	defer e.rw.RUnlock()

	content, err := e.repo.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	return content, e.commitSHA, nil
}

// Forget drops the cache entry for key and removes its cache directory.
// Called when the GitRepository is deleted.
func (r *Registry) Forget(key client.ObjectKey) {
	r.mu.Lock()
	e, ok := r.entries[key]
	delete(r.entries, key)
	r.mu.Unlock()

	if ok {
		_ = os.RemoveAll(e.cacheDir)
	}
}
