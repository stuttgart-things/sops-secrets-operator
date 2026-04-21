// Package git provides a thin wrapper around go-git that fetches a Git
// repository into a local cache directory and exposes file reads from it.
// It supports HTTP basic auth and SSH (public-key) auth, branch HEAD or
// pinned-revision (SHA or tag) checkouts, and a per-cache-dir mutex so
// concurrent reconciles of the same repo don't race on the working tree.
package git

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	cryptossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// Auth selects the authentication method. At most one field should be set;
// a zero-value Auth performs an unauthenticated clone (for public repos).
type Auth struct {
	Basic *BasicAuth
	SSH   *SSHAuth
}

// BasicAuth carries HTTP(S) basic-auth credentials. Password may be a PAT.
type BasicAuth struct {
	Username string
	Password string
}

// SSHAuth carries SSH public-key credentials. KnownHosts is required
// (strict host-key checking is non-negotiable for MVP).
type SSHAuth struct {
	User       string // defaults to "git"
	PrivateKey []byte
	Passphrase []byte
	KnownHosts []byte
}

// Config configures a Repo.
type Config struct {
	URL    string
	Branch string // used when Revision is empty; defaults to "main"
	// Revision pins to a commit SHA or tag. When non-empty it overrides Branch.
	Revision string
	Auth     Auth
	// CacheRoot overrides the cache parent directory. Defaults to
	// $XDG_CACHE_HOME/sops-secrets-operator, then /var/cache/sops-secrets-operator.
	CacheRoot string
}

// Repo manages a local clone of a remote git repository.
type Repo struct {
	cfg      Config
	cacheDir string
}

// New constructs a Repo. The cache directory is derived deterministically
// from (URL, Branch, Revision) so different pins do not collide.
func New(cfg Config) (*Repo, error) {
	if cfg.URL == "" {
		return nil, errors.New("git: URL is required")
	}
	if cfg.Branch == "" && cfg.Revision == "" {
		cfg.Branch = "main"
	}

	root, err := resolveCacheRoot(cfg.CacheRoot)
	if err != nil {
		return nil, err
	}

	key := fmt.Sprintf("%s@%s#%s", cfg.URL, cfg.Branch, cfg.Revision)
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(key)))[:16]
	return &Repo{cfg: cfg, cacheDir: filepath.Join(root, hash)}, nil
}

// CacheDir returns the directory where this Repo is cloned.
func (r *Repo) CacheDir() string { return r.cacheDir }

func resolveCacheRoot(override string) (string, error) {
	candidate := override
	if candidate == "" {
		if ucd, err := os.UserCacheDir(); err == nil {
			candidate = filepath.Join(ucd, "sops-secrets-operator")
		} else {
			candidate = "/var/cache/sops-secrets-operator"
		}
	}
	if err := os.MkdirAll(candidate, 0o700); err != nil {
		return "", fmt.Errorf("git: create cache root %q: %w", candidate, err)
	}
	// Tighten perms even if MkdirAll succeeded against a pre-existing dir.
	if err := os.Chmod(candidate, 0o700); err != nil {
		return "", fmt.Errorf("git: chmod cache root %q: %w", candidate, err)
	}
	return candidate, nil
}

var (
	muMap   = make(map[string]*sync.Mutex)
	muMapMu sync.Mutex
)

func repoMutex(key string) *sync.Mutex {
	muMapMu.Lock()
	defer muMapMu.Unlock()
	if m, ok := muMap[key]; ok {
		return m
	}
	m := &sync.Mutex{}
	muMap[key] = m
	return m
}

// EnsureCloned ensures the cache is populated with the configured branch or
// revision and returns the resolved commit SHA.
func (r *Repo) EnsureCloned(ctx context.Context) (string, error) {
	mu := repoMutex(r.cacheDir)
	mu.Lock()
	defer mu.Unlock()

	authMethod, err := r.authMethod()
	if err != nil {
		return "", err
	}

	if _, err := os.Stat(filepath.Join(r.cacheDir, ".git")); err == nil {
		if sha, err := r.updateAndResolve(ctx, authMethod); err == nil {
			_ = os.Chtimes(r.cacheDir, time.Now(), time.Now())
			return sha, nil
		}
		// Stale cache — wipe and reclone.
		_ = os.RemoveAll(r.cacheDir)
	}

	if err := os.MkdirAll(filepath.Dir(r.cacheDir), 0o700); err != nil {
		return "", fmt.Errorf("git: create parent of cache: %w", err)
	}

	cloneOpts := &gogit.CloneOptions{URL: r.cfg.URL, Auth: authMethod}
	if r.cfg.Revision == "" {
		cloneOpts.ReferenceName = plumbing.NewBranchReferenceName(r.cfg.Branch)
		cloneOpts.SingleBranch = true
		cloneOpts.Depth = 1
	}
	// Full history is required to resolve arbitrary SHAs or tags.

	repo, err := gogit.PlainCloneContext(ctx, r.cacheDir, false, cloneOpts)
	if err != nil {
		_ = os.RemoveAll(r.cacheDir)
		return "", fmt.Errorf("git: clone %q: %w", r.cfg.URL, err)
	}

	if r.cfg.Revision != "" {
		if err := checkoutRevision(repo, r.cfg.Revision); err != nil {
			_ = os.RemoveAll(r.cacheDir)
			return "", err
		}
	}

	return resolveHead(repo)
}

func (r *Repo) updateAndResolve(ctx context.Context, authMethod transport.AuthMethod) (string, error) {
	repo, err := gogit.PlainOpen(r.cacheDir)
	if err != nil {
		return "", fmt.Errorf("git: open cache: %w", err)
	}

	if err := repo.FetchContext(ctx, &gogit.FetchOptions{
		RemoteName: "origin",
		Auth:       authMethod,
		Force:      true,
	}); err != nil && !errors.Is(err, gogit.NoErrAlreadyUpToDate) {
		return "", fmt.Errorf("git: fetch: %w", err)
	}

	if r.cfg.Revision != "" {
		if err := checkoutRevision(repo, r.cfg.Revision); err != nil {
			return "", err
		}
		return resolveHead(repo)
	}

	wt, err := repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("git: worktree: %w", err)
	}
	if err := wt.PullContext(ctx, &gogit.PullOptions{
		RemoteName:    "origin",
		ReferenceName: plumbing.NewBranchReferenceName(r.cfg.Branch),
		SingleBranch:  true,
		Auth:          authMethod,
		Force:         true,
	}); err != nil && !errors.Is(err, gogit.NoErrAlreadyUpToDate) {
		return "", fmt.Errorf("git: pull: %w", err)
	}
	return resolveHead(repo)
}

func checkoutRevision(repo *gogit.Repository, revision string) error {
	hash, err := repo.ResolveRevision(plumbing.Revision(revision))
	if err != nil {
		return fmt.Errorf("git: resolve revision %q: %w", revision, err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("git: worktree: %w", err)
	}
	return wt.Checkout(&gogit.CheckoutOptions{Hash: *hash, Force: true})
}

func resolveHead(repo *gogit.Repository) (string, error) {
	head, err := repo.Head()
	if err != nil {
		return "", fmt.Errorf("git: head: %w", err)
	}
	return head.Hash().String(), nil
}

func (r *Repo) authMethod() (transport.AuthMethod, error) {
	switch {
	case r.cfg.Auth.Basic != nil:
		return &http.BasicAuth{
			Username: r.cfg.Auth.Basic.Username,
			Password: r.cfg.Auth.Basic.Password,
		}, nil
	case r.cfg.Auth.SSH != nil:
		return sshAuth(r.cfg.Auth.SSH)
	default:
		return nil, nil
	}
}

func sshAuth(a *SSHAuth) (transport.AuthMethod, error) {
	if len(a.KnownHosts) == 0 {
		return nil, errors.New("git: SSH auth requires knownHosts (strict host-key checking)")
	}
	user := a.User
	if user == "" {
		user = "git"
	}
	auth, err := gitssh.NewPublicKeys(user, a.PrivateKey, string(a.Passphrase))
	if err != nil {
		return nil, fmt.Errorf("git: parse SSH private key: %w", err)
	}
	cb, err := knownHostsCallback(a.KnownHosts)
	if err != nil {
		return nil, err
	}
	auth.HostKeyCallback = cb
	return auth, nil
}

// knownHostsCallback parses OpenSSH known_hosts content into a HostKeyCallback.
// knownhosts.New only accepts file paths, so we write to a short-lived temp file.
func knownHostsCallback(data []byte) (cryptossh.HostKeyCallback, error) {
	f, err := os.CreateTemp("", "ssh-known-hosts-*")
	if err != nil {
		return nil, fmt.Errorf("git: temp known_hosts: %w", err)
	}
	defer func() {
		_ = f.Close()
		_ = os.Remove(f.Name())
	}()
	if _, err := f.Write(data); err != nil {
		return nil, fmt.Errorf("git: write known_hosts: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("git: close known_hosts: %w", err)
	}
	return knownhosts.New(f.Name())
}

// ReadFile reads a file from the repository cache. Rejects paths that would
// escape the cache directory via "..".
func (r *Repo) ReadFile(relativePath string) ([]byte, error) {
	clean := filepath.Clean(relativePath)
	if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return nil, fmt.Errorf("git: invalid path %q", relativePath)
	}
	full := filepath.Join(r.cacheDir, clean)
	return os.ReadFile(full)
}
