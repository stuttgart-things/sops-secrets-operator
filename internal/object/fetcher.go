// Package object implements HTTPS and S3-compatible object fetchers for
// the ObjectSource CRD. A Fetcher is created per ObjectSource by the
// controller and handed to the source Registry so that the reconcile
// loop can refresh cached content with conditional-GET (ETag / If-None-Match)
// semantics.
//
// Two modes are supported:
//
//   - HTTPS: spec.url references a single file. Fetch(path="") GETs the
//     URL with If-None-Match; Probe() issues a HEAD.
//   - S3:    spec.bucket references an S3-compatible bucket. Fetch(path)
//     GetObject's the given key; Probe() calls BucketExists.
//
// The package never logs or returns credentials.
package object

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// ErrNotModified is returned by Fetch when the remote ETag matched the
// caller-supplied If-None-Match value and no content was transferred.
var ErrNotModified = errors.New("object: not modified")

// Fetcher is the abstraction over HTTPS and S3-compatible backends.
type Fetcher interface {
	// Fetch returns the object content and the server-reported ETag.
	// If ifNoneMatch is non-empty and the remote reports it unchanged,
	// Fetch returns (nil, ifNoneMatch, ErrNotModified).
	//
	// For HTTPS fetchers the path argument is ignored (URL is the object).
	// For S3 fetchers path is the object key.
	Fetch(ctx context.Context, path, ifNoneMatch string) (content []byte, etag string, err error)

	// Probe validates connectivity + auth without transferring content.
	// HTTPS issues a HEAD; S3 calls BucketExists.
	Probe(ctx context.Context) error
}

// HTTPSAuth carries resolved HTTPS authentication material.
type HTTPSAuth struct {
	// BearerToken, when non-empty, is sent as `Authorization: Bearer ...`.
	BearerToken string
	// Username/Password, when both non-empty, are sent as HTTP Basic Auth.
	Username string
	Password string
}

// HTTPSFetcher fetches a single file from an HTTPS URL.
type HTTPSFetcher struct {
	URL    string
	Auth   HTTPSAuth
	Client *http.Client
}

// NewHTTPSFetcher returns an HTTPSFetcher with a default 30s timeout.
func NewHTTPSFetcher(url string, auth HTTPSAuth) *HTTPSFetcher {
	return &HTTPSFetcher{
		URL:    url,
		Auth:   auth,
		Client: &http.Client{Timeout: 30 * time.Second},
	}
}

// Fetch GETs the configured URL, respecting If-None-Match.
func (f *HTTPSFetcher) Fetch(ctx context.Context, _, ifNoneMatch string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.URL, nil)
	if err != nil {
		return nil, "", err
	}
	f.applyAuth(req)
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}

	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusNotModified:
		return nil, ifNoneMatch, ErrNotModified
	case http.StatusOK:
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, "", fmt.Errorf("read body: %w", err)
		}
		return body, resp.Header.Get("ETag"), nil
	default:
		return nil, "", fmt.Errorf("https: unexpected status %d", resp.StatusCode)
	}
}

// Probe issues a HEAD against the configured URL.
func (f *HTTPSFetcher) Probe(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, f.URL, nil)
	if err != nil {
		return err
	}
	f.applyAuth(req)
	resp, err := f.Client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return nil
	}
	return fmt.Errorf("https: probe returned %d", resp.StatusCode)
}

func (f *HTTPSFetcher) applyAuth(req *http.Request) {
	if f.Auth.BearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+f.Auth.BearerToken)
		return
	}
	if f.Auth.Username != "" && f.Auth.Password != "" {
		req.SetBasicAuth(f.Auth.Username, f.Auth.Password)
	}
}

// S3Config configures an S3Fetcher.
type S3Config struct {
	Endpoint  string
	Bucket    string
	Region    string
	Insecure  bool
	AccessKey string
	SecretKey string
}

// S3Fetcher fetches objects from an S3-compatible bucket.
type S3Fetcher struct {
	cfg    S3Config
	client *minio.Client
}

// NewS3Fetcher constructs an S3Fetcher using minio-go. When AccessKey is
// empty the client is unauthenticated (suitable for public buckets).
func NewS3Fetcher(cfg S3Config) (*S3Fetcher, error) {
	opts := &minio.Options{
		Secure: !cfg.Insecure,
		Region: cfg.Region,
	}
	if cfg.AccessKey != "" {
		opts.Creds = credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, "")
	}
	cli, err := minio.New(cfg.Endpoint, opts)
	if err != nil {
		return nil, fmt.Errorf("new minio client: %w", err)
	}
	return &S3Fetcher{cfg: cfg, client: cli}, nil
}

// Fetch GetObject's the given key. If ifNoneMatch is non-empty and the
// object's current ETag matches, ErrNotModified is returned and no
// content is transferred.
func (f *S3Fetcher) Fetch(ctx context.Context, path, ifNoneMatch string) ([]byte, string, error) {
	key := strings.TrimPrefix(path, "/")
	if key == "" {
		return nil, "", fmt.Errorf("s3: empty object key")
	}

	stat, err := f.client.StatObject(ctx, f.cfg.Bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return nil, "", fmt.Errorf("stat %s/%s: %w", f.cfg.Bucket, key, err)
	}
	etag := stat.ETag
	if ifNoneMatch != "" && etag != "" && ifNoneMatch == etag {
		return nil, etag, ErrNotModified
	}

	obj, err := f.client.GetObject(ctx, f.cfg.Bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, "", fmt.Errorf("get %s/%s: %w", f.cfg.Bucket, key, err)
	}
	defer func() { _ = obj.Close() }()
	body, err := io.ReadAll(obj)
	if err != nil {
		return nil, "", fmt.Errorf("read %s/%s: %w", f.cfg.Bucket, key, err)
	}
	return body, etag, nil
}

// Probe validates auth + bucket reachability via BucketExists.
func (f *S3Fetcher) Probe(ctx context.Context) error {
	ok, err := f.client.BucketExists(ctx, f.cfg.Bucket)
	if err != nil {
		return fmt.Errorf("bucket exists: %w", err)
	}
	if !ok {
		return fmt.Errorf("bucket %q not found or not accessible", f.cfg.Bucket)
	}
	return nil
}
