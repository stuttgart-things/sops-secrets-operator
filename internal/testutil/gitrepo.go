package testutil

import (
	"os"
	"path/filepath"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// GitRepo describes a local file-backed git repository for tests.
type GitRepo struct {
	// Path on disk of the working tree.
	Path string
	// URL is a file:// URL suitable for GitRepository.spec.url.
	URL string
}

// InitGitRepo creates a non-bare git repository in a fresh directory under
// t.TempDir (via the caller's dir param), writes each file in the `files`
// map, and commits them. Returns the repo's path + file:// URL plus the
// commit SHA of the initial commit.
//
// The internal/git package accepts file:// URLs directly via go-git, so
// this repo plugs into the operator's Registry without any extra daemon.
func InitGitRepo(tb TestingT, dir string, files map[string][]byte) (repo GitRepo, sha string) {
	tb.Helper()

	if err := os.MkdirAll(dir, 0o700); err != nil {
		tb.Fatalf("testutil: mkdir %s: %v", dir, err)
	}

	r, err := gogit.PlainInit(dir, false)
	if err != nil {
		tb.Fatalf("testutil: git init: %v", err)
	}

	for relPath, content := range files {
		full := filepath.Join(dir, relPath)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			tb.Fatalf("testutil: mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, content, 0o600); err != nil {
			tb.Fatalf("testutil: write %s: %v", full, err)
		}
	}

	wt, err := r.Worktree()
	if err != nil {
		tb.Fatalf("testutil: worktree: %v", err)
	}
	for relPath := range files {
		if _, err := wt.Add(relPath); err != nil {
			tb.Fatalf("testutil: add %s: %v", relPath, err)
		}
	}
	hash, err := wt.Commit("initial", &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "testutil",
			Email: "testutil@localhost",
			When:  time.Now(),
		},
	})
	if err != nil {
		tb.Fatalf("testutil: commit: %v", err)
	}

	head, err := r.Head()
	if err != nil {
		tb.Fatalf("testutil: head: %v", err)
	}
	// Make sure the repo's current branch exists as "main" (or whatever
	// go-git's default init branch is on this host). The operator's
	// GitRepository spec defaults to "main"; if PlainInit used "master"
	// on this go-git version, switch the HEAD ref name to "main" so
	// tests don't have to care.
	if head.Name().Short() != "main" {
		_ = wt // no-op: we can just tell the tests to set Spec.Branch
		// to the detected name. Return it via URL-fragment-like hints
		// is brittle; instead, tests should read head.Name().Short().
		// Keep simple: expose it on the returned repo if needed.
	}
	_ = hash

	return GitRepo{
		Path: dir,
		URL:  "file://" + dir,
	}, head.Hash().String()
}

// DetectDefaultBranch returns the current HEAD branch name of a
// previously-initialized repo. Useful since go-git's PlainInit default
// may be "master" or "main" depending on version.
func DetectDefaultBranch(tb TestingT, path string) string {
	tb.Helper()
	r, err := gogit.PlainOpen(path)
	if err != nil {
		tb.Fatalf("testutil: git open: %v", err)
	}
	head, err := r.Head()
	if err != nil {
		tb.Fatalf("testutil: head: %v", err)
	}
	return head.Name().Short()
}
