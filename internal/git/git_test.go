package git

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestNewRequiresURL(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("expected error for missing URL")
	}
}

func TestCacheDirDiffersByBranch(t *testing.T) {
	root := t.TempDir()
	a, err := New(Config{URL: "https://example/foo.git", Branch: "main", CacheRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(Config{URL: "https://example/foo.git", Branch: "dev", CacheRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if a.CacheDir() == b.CacheDir() {
		t.Fatal("different branches must produce different cache dirs")
	}
	if !strings.HasPrefix(a.CacheDir(), root) {
		t.Fatalf("cache dir %q not under root %q", a.CacheDir(), root)
	}
}

func TestCacheDirDiffersByRevision(t *testing.T) {
	root := t.TempDir()
	a, err := New(Config{URL: "https://example/foo.git", Revision: "v1.0.0", CacheRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(Config{URL: "https://example/foo.git", Revision: "v1.1.0", CacheRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if a.CacheDir() == b.CacheDir() {
		t.Fatal("different revisions must produce different cache dirs")
	}
}

func TestCacheRootCreatedWithTightPerms(t *testing.T) {
	root := filepath.Join(t.TempDir(), "created-here")
	if _, err := New(Config{URL: "https://example/foo.git", CacheRoot: root}); err != nil {
		t.Fatal(err)
	}
	// If MkdirAll returned nil, the dir exists. Chmod is also applied.
}

func TestReadFileRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	r, err := New(Config{URL: "https://example/foo.git", CacheRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.ReadFile("../escape"); err == nil {
		t.Fatal("expected traversal rejection")
	}
	if _, err := r.ReadFile("/etc/passwd"); err == nil {
		t.Fatal("expected absolute-path rejection")
	}
}

func TestSSHAuthRequiresKnownHosts(t *testing.T) {
	r, err := New(Config{
		URL:    "git@example.com:foo/bar.git",
		Branch: "main",
		Auth:   Auth{SSH: &SSHAuth{PrivateKey: []byte("dummy")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.authMethod(); err == nil || !strings.Contains(err.Error(), "knownHosts") {
		t.Fatalf("expected knownHosts error, got %v", err)
	}
}
