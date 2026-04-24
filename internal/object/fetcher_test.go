/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package object

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPSFetcher_ETagSemantics(t *testing.T) {
	t.Parallel()

	const etag = `"v1"`
	var gets, heads int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodHead:
			heads++
			w.Header().Set("ETag", etag)
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			gets++
			if inm := r.Header.Get("If-None-Match"); inm == etag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", etag)
			_, _ = w.Write([]byte("encrypted-payload"))
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(srv.Close)

	f := NewHTTPSFetcher(srv.URL, HTTPSAuth{})
	f.Client.Timeout = 5 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	if err := f.Probe(ctx); err != nil {
		t.Fatalf("probe: %v", err)
	}
	if heads != 1 {
		t.Fatalf("expected 1 HEAD, got %d", heads)
	}

	body, got, err := f.Fetch(ctx, "", "")
	if err != nil {
		t.Fatalf("initial fetch: %v", err)
	}
	if got != etag {
		t.Fatalf("etag = %q, want %q", got, etag)
	}
	if string(body) != "encrypted-payload" {
		t.Fatalf("body = %q", body)
	}

	body2, got2, err := f.Fetch(ctx, "", etag)
	if !errors.Is(err, ErrNotModified) {
		t.Fatalf("second fetch err = %v, want ErrNotModified", err)
	}
	if got2 != etag {
		t.Fatalf("etag on 304 = %q", got2)
	}
	if body2 != nil {
		t.Fatalf("expected nil body on 304, got %q", body2)
	}
	if gets != 2 {
		t.Fatalf("expected 2 GETs, got %d", gets)
	}
}

func TestHTTPSFetcher_Auth(t *testing.T) {
	t.Parallel()

	t.Run("bearer", func(t *testing.T) {
		t.Parallel()
		var gotAuth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			w.Header().Set("ETag", `"a"`)
			_, _ = w.Write([]byte("x"))
		}))
		t.Cleanup(srv.Close)

		f := NewHTTPSFetcher(srv.URL, HTTPSAuth{BearerToken: "s3cret"})
		if _, _, err := f.Fetch(context.Background(), "", ""); err != nil {
			t.Fatalf("fetch: %v", err)
		}
		if gotAuth != "Bearer s3cret" {
			t.Fatalf("auth header = %q", gotAuth)
		}
	})

	t.Run("basic", func(t *testing.T) {
		t.Parallel()
		var gotAuth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			w.Header().Set("ETag", `"a"`)
			_, _ = w.Write([]byte("x"))
		}))
		t.Cleanup(srv.Close)

		f := NewHTTPSFetcher(srv.URL, HTTPSAuth{Username: "u", Password: "p"})
		if _, _, err := f.Fetch(context.Background(), "", ""); err != nil {
			t.Fatalf("fetch: %v", err)
		}
		if !strings.HasPrefix(gotAuth, "Basic ") {
			t.Fatalf("auth header = %q, want Basic ...", gotAuth)
		}
	})
}

func TestHTTPSFetcher_ErrorStatus(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "denied", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)

	f := NewHTTPSFetcher(srv.URL, HTTPSAuth{})
	_, _, err := f.Fetch(context.Background(), "", "")
	if err == nil {
		t.Fatal("expected error on 403")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Fatalf("err = %v, want 403 mention", err)
	}
}
