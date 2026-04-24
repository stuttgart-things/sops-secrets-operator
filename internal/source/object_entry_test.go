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
	"strings"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/stuttgart-things/sops-secrets-operator/internal/object"
)

const (
	testETagV1     = `"v1"`
	testETagBucket = `"e"`
)

type fakeFetcher struct {
	fetchFn func(ctx context.Context, path, ifNoneMatch string) ([]byte, string, error)
	probeFn func(ctx context.Context) error
}

func (f *fakeFetcher) Fetch(ctx context.Context, path, ifNoneMatch string) ([]byte, string, error) {
	return f.fetchFn(ctx, path, ifNoneMatch)
}

func (f *fakeFetcher) Probe(ctx context.Context) error {
	return f.probeFn(ctx)
}

func TestEnsureObjectCached_URL_FetchAndNotModified(t *testing.T) {
	r := NewRegistry()
	key := client.ObjectKey{Namespace: "ns", Name: "os"}

	calls := 0
	f := &fakeFetcher{fetchFn: func(_ context.Context, path, ifNoneMatch string) ([]byte, string, error) {
		calls++
		if path != "" {
			t.Errorf("URL mode: expected empty path, got %q", path)
		}
		if ifNoneMatch == testETagV1 {
			return nil, testETagV1, object.ErrNotModified
		}
		return []byte("payload-v1"), testETagV1, nil
	}}

	etag, err := r.EnsureObjectCached(context.Background(), key, ObjectModeURL, f)
	if err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	if etag != testETagV1 {
		t.Fatalf("etag = %q, want v1", etag)
	}

	body, gotETag, err := r.ReadObject(context.Background(), key, "")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(body) != "payload-v1" || gotETag != testETagV1 {
		t.Fatalf("read returned body=%q etag=%q", body, gotETag)
	}

	etag2, err := r.EnsureObjectCached(context.Background(), key, ObjectModeURL, f)
	if err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	if etag2 != testETagV1 {
		t.Fatalf("second etag = %q", etag2)
	}
	if calls != 2 {
		t.Fatalf("fetch called %d times, want 2", calls)
	}

	body2, _, err := r.ReadObject(context.Background(), key, "")
	if err != nil || string(body2) != "payload-v1" {
		t.Fatalf("read after 304: body=%q err=%v", body2, err)
	}
}

func TestEnsureObjectCached_URL_FetchError(t *testing.T) {
	r := NewRegistry()
	key := client.ObjectKey{Namespace: "ns", Name: "os"}

	f := &fakeFetcher{fetchFn: func(context.Context, string, string) ([]byte, string, error) {
		return nil, "", errors.New("boom")
	}}

	_, err := r.EnsureObjectCached(context.Background(), key, ObjectModeURL, f)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want boom", err)
	}
}

func TestEnsureObjectCached_Bucket_ProbeOnly(t *testing.T) {
	r := NewRegistry()
	key := client.ObjectKey{Namespace: "ns", Name: "os"}

	probed := false
	f := &fakeFetcher{
		probeFn: func(context.Context) error { probed = true; return nil },
		fetchFn: func(_ context.Context, path, _ string) ([]byte, string, error) {
			if path == "" {
				return nil, "", errors.New("empty path")
			}
			return []byte("obj-at-" + path), testETagBucket, nil
		},
	}

	if _, err := r.EnsureObjectCached(context.Background(), key, ObjectModeBucket, f); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if !probed {
		t.Fatal("probe was not called")
	}

	body, etag, err := r.ReadObject(context.Background(), key, "secrets/db.enc.yaml")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(body) != "obj-at-secrets/db.enc.yaml" || etag != testETagBucket {
		t.Fatalf("read body=%q etag=%q", body, etag)
	}
}

func TestReadObject_MissingKey(t *testing.T) {
	r := NewRegistry()
	_, _, err := r.ReadObject(context.Background(), client.ObjectKey{Name: "x"}, "p")
	if err == nil {
		t.Fatal("expected error for unknown object source")
	}
}

func TestForgetObject(t *testing.T) {
	r := NewRegistry()
	key := client.ObjectKey{Namespace: "n", Name: "a"}
	f := &fakeFetcher{fetchFn: func(context.Context, string, string) ([]byte, string, error) {
		return []byte("x"), testETagBucket, nil
	}}
	if _, err := r.EnsureObjectCached(context.Background(), key, ObjectModeURL, f); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	r.ForgetObject(key)
	_, _, err := r.ReadObject(context.Background(), key, "")
	if err == nil || !strings.Contains(err.Error(), "no cache for") {
		t.Fatalf("err = %v, want no-cache-for", err)
	}
	// Idempotent.
	r.ForgetObject(key)
}

func TestEnsureObjectCached_ModeSwitchClearsURLCache(t *testing.T) {
	r := NewRegistry()
	key := client.ObjectKey{Namespace: "n", Name: "a"}

	urlFetcher := &fakeFetcher{fetchFn: func(context.Context, string, string) ([]byte, string, error) {
		return []byte("v1"), testETagV1, nil
	}}
	if _, err := r.EnsureObjectCached(context.Background(), key, ObjectModeURL, urlFetcher); err != nil {
		t.Fatalf("url ensure: %v", err)
	}

	// Switch to bucket mode — previously cached URL content must not be
	// returned, and a missing fetchFn would blow up if the reset had
	// forgotten to drop it.
	bucketFetcher := &fakeFetcher{
		probeFn: func(context.Context) error { return nil },
		fetchFn: func(_ context.Context, path, _ string) ([]byte, string, error) {
			return []byte("bucket-" + path), `"b"`, nil
		},
	}
	if _, err := r.EnsureObjectCached(context.Background(), key, ObjectModeBucket, bucketFetcher); err != nil {
		t.Fatalf("bucket ensure: %v", err)
	}

	body, etag, err := r.ReadObject(context.Background(), key, "key1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(body) != "bucket-key1" || etag != `"b"` {
		t.Fatalf("read body=%q etag=%q — URL cache was not reset", body, etag)
	}
}
