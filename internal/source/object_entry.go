/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package source

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/stuttgart-things/sops-secrets-operator/internal/metrics"
	"github.com/stuttgart-things/sops-secrets-operator/internal/object"
)

// ObjectMode distinguishes URL-mode ObjectSources (single cached file)
// from bucket-mode ObjectSources (per-key fetch on read).
type ObjectMode int

const (
	// ObjectModeURL is a single HTTPS URL. The controller caches the
	// full content keyed by ETag; consumer Read returns the cached bytes.
	ObjectModeURL ObjectMode = iota
	// ObjectModeBucket is an S3-compatible bucket. The controller only
	// validates auth (Probe). Consumer Read performs a GetObject by key.
	ObjectModeBucket
)

// objectEntry is the per-ObjectSource state held by the Registry.
type objectEntry struct {
	rw         sync.RWMutex
	mode       ObjectMode
	fetcher    object.Fetcher
	etag       string
	content    []byte
	lastSynced time.Time
}

// EnsureObjectCached refreshes the cache entry for the ObjectSource
// identified by key. For URL mode it performs a conditional GET
// (If-None-Match) and stores the content. For bucket mode it runs
// Probe() to validate auth without transferring content. Returns the
// current ETag (may be empty for bucket mode).
func (r *Registry) EnsureObjectCached(ctx context.Context, key client.ObjectKey, mode ObjectMode, fetcher object.Fetcher) (_ string, retErr error) {
	start := time.Now()
	defer func() {
		result := metrics.ResultSuccess
		if retErr != nil {
			result = metrics.ResultError
		}
		metrics.ObjectFetchDurationSeconds.WithLabelValues(result).Observe(time.Since(start).Seconds())
	}()

	r.mu.Lock()
	e, ok := r.objects[key]
	if !ok || e.mode != mode {
		e = &objectEntry{mode: mode}
		r.objects[key] = e
	}
	r.mu.Unlock()

	e.rw.Lock()
	defer e.rw.Unlock()
	e.fetcher = fetcher

	switch mode {
	case ObjectModeBucket:
		if err := fetcher.Probe(ctx); err != nil {
			return "", err
		}
		e.lastSynced = time.Now()
		return "", nil

	case ObjectModeURL:
		body, etag, err := fetcher.Fetch(ctx, "", e.etag)
		if errors.Is(err, object.ErrNotModified) {
			e.lastSynced = time.Now()
			return e.etag, nil
		}
		if err != nil {
			return "", err
		}
		e.etag = etag
		e.content = body
		e.lastSynced = time.Now()
		return etag, nil

	default:
		return "", fmt.Errorf("unknown object mode %d", mode)
	}
}

// ReadObject returns the content and ETag observed for the ObjectSource
// identified by key. For URL mode the path argument is ignored and the
// cached content is returned. For bucket mode path is the object key and
// a fresh GetObject is performed against the cached fetcher.
func (r *Registry) ReadObject(ctx context.Context, key client.ObjectKey, path string) ([]byte, string, error) {
	r.mu.Lock()
	e, ok := r.objects[key]
	r.mu.Unlock()
	if !ok {
		return nil, "", fmt.Errorf("source: no cache for ObjectSource %s", key)
	}

	e.rw.RLock()
	defer e.rw.RUnlock()

	switch e.mode {
	case ObjectModeURL:
		if len(e.content) == 0 {
			return nil, "", fmt.Errorf("source: ObjectSource %s not yet fetched", key)
		}
		return append([]byte(nil), e.content...), e.etag, nil

	case ObjectModeBucket:
		if e.fetcher == nil {
			return nil, "", fmt.Errorf("source: ObjectSource %s has no fetcher", key)
		}
		body, etag, err := e.fetcher.Fetch(ctx, path, "")
		if err != nil {
			return nil, "", err
		}
		return body, etag, nil

	default:
		return nil, "", fmt.Errorf("unknown object mode %d", e.mode)
	}
}

// ForgetObject drops the cache entry for the ObjectSource identified by key.
func (r *Registry) ForgetObject(key client.ObjectKey) {
	r.mu.Lock()
	delete(r.objects, key)
	r.mu.Unlock()
}
