package testutil

import (
	"path/filepath"
	"testing"
)

func TestInitGitRepo(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "repo")
	repo, sha := InitGitRepo(t, dir, map[string][]byte{
		"a.yaml":       []byte("hello: world\n"),
		"sub/b.yaml":   []byte("sub: value\n"),
		"sub/c/d.yaml": []byte("deep: value\n"),
	})
	if repo.Path != dir {
		t.Fatalf("repo.Path = %s, want %s", repo.Path, dir)
	}
	if repo.URL != "file://"+dir {
		t.Fatalf("repo.URL = %s", repo.URL)
	}
	if len(sha) != 40 {
		t.Fatalf("sha is not a full hex SHA: %s", sha)
	}
	if br := DetectDefaultBranch(t, dir); br != "master" && br != "main" {
		t.Fatalf("unexpected default branch %q", br)
	}
}
