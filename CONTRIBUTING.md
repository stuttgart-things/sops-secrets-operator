# Contributing

Thanks for sending a fix or feature. This file is the dev-setup quick start —
how to go from a fresh clone to a green local test run that matches CI. For
the security boundary the operator promises, see [SECURITY.md](./SECURITY.md);
for what the operator does and why, see [README.md](./README.md).

## Prerequisites

| Tool | Version | Why |
|---|---|---|
| Go | matches `go.mod` (currently 1.26+) | builds + tests |
| Docker | any recent | `make test-e2e` builds the controller image and runs Kind in containers |
| Kind | `v0.24.0` | pinned by the e2e workflow; older versions usually work locally |
| `kubectl` | `v1.31.0` | pinned by the e2e workflow |
| `golangci-lint` | downloaded by `make lint` on demand | linting |

CI installs `kind` and `kubectl` on demand, so you don't need to match the
pinned versions exactly to reproduce a CI failure locally — but matching makes
the easiest debugging path.

The dedicated `kind-testing-runner-1` self-hosted runner already has all of
the above; this section is for local repro.

## Day-to-day

Three commands cover ~everything.

### `make test` — unit + envtest under `-race`

```bash
make test
```

Fast (~1 min once envtest binaries are cached). Runs every package except
`./test/e2e/`. Uses [`controller-runtime`'s `envtest`][envtest] — a real
`kube-apiserver` + `etcd` started in-process with no kubelet — so reconciler
behaviour against real CRDs is exercised without standing up a cluster. The
envtest k8s binaries are auto-downloaded into `bin/` on first run and
controlled via `ENVTEST_K8S_VERSION` (auto-detected from `k8s.io/api`).

### `make lint` — `golangci-lint`

```bash
make lint        # check
make lint-fix    # auto-fix where possible
make lint-config # validate config without running
```

The repo builds a custom `golangci-lint` with the `controller-runtime`
[`logcheck`][logcheck] plugin (see commit `12729b2` for why). `make lint`
handles that; running `golangci-lint` directly will skip the plugin and miss
log-format violations.

### `make test-e2e` — full real-cluster suite on Kind

```bash
make test-e2e
```

Cold runs are slow (~5 min for the first `docker build` + ~2 min for the run);
warm runs are ~2-3 min. Each invocation:

1. Builds the controller image as `example.com/sops-secrets-operator:v0.0.1`
2. Tears down any existing Kind cluster of the same name and creates a fresh one
3. Loads the image into the Kind nodes
4. Installs cert-manager (skipped if already present; controlled by
   `CERT_MANAGER_INSTALL_SKIP=true`)
5. Applies the CRDs + controller via `make install` + `make deploy`
6. Runs the Ginkgo specs under `./test/e2e/`
7. Tears the cluster back down via `make cleanup-test-e2e` (wrapped in a
   shell `trap` so cleanup runs even on test failure or `Ctrl-C`)

If a leftover Kind cluster ever survives a crash:

```bash
make cleanup-test-e2e
```

The setup target always deletes-then-creates anyway, so a stale cluster won't
poison the next attempt — but explicit cleanup is cheaper than waiting for the
next `make test-e2e` to do it for you.

## CI mapping

When a PR check fails, here's where to look and what to run locally:

| Failed check | Workflow | Reproduce locally |
|---|---|---|
| `build-and-test` | [`build-test.yaml`](./.github/workflows/build-test.yaml) | `make test` |
| `Build, Push to ttl.sh & Scan` | [`build-scan-image.yaml`](./.github/workflows/build-scan-image.yaml) | `make docker-build` (skip the push/scan steps; failure is usually a Trivy CVE finding visible in the job log) |
| `e2e (kind + cert-manager)` | [`e2e.yaml`](./.github/workflows/e2e.yaml) | `make test-e2e` |

The e2e job runs on the dedicated `kind-testing-runner-1` self-hosted runner
(see #43). If e2e is red but `make test-e2e` is green locally, suspect runner
state — the workflow's pre-run diagnostics step (`docker ps -a`, `free -h`,
`df -h`) is at the top of the failed job log and is usually the fastest way
to triage.

## Conventional commits

semantic-release reads commit subjects to compute the next version and
generate the changelog. Stick to [Conventional Commits][cc]:

```
feat(crd): InlineSopsSecret — accept SOPS-encrypted payload directly in the CR
fix(e2e): clean up metrics ClusterRoleBinding in AfterAll, make create idempotent
docs(readme): add adoption, drift detection, and managed-Secret labels
ci(e2e): switch to dedicated kind-test runner + harden Makefile
test: e2e adoption + drift-revert for SopsSecret
chore(dev): add Taskfile with kcl:sync-crds and kcl:render targets
```

`feat:` bumps the minor version; `fix:` bumps the patch version; everything
else (`docs:`, `ci:`, `test:`, `chore:`, `refactor:`) is changelog noise but
no version bump. A trailing `BREAKING CHANGE:` footer (or `!` after the type)
bumps the major. Reference issues with `closes #N` in the PR body, not the
commit subject — keeps the changelog readable.

## Security boundary — please don't relax these

The operator's [security model](./SECURITY.md) makes a small set of deliberate
choices that are load-bearing for the threat model. Changes that loosen any
of these need an explicit "why" in the PR description and likely a discussion
issue first:

- **age-only decryption.** Other SOPS providers (PGP, KMS, vault) are not
  wired in. Adding one is a feature decision (see #11), not a bug fix.
- **Namespace override is authoritative** (`SopsSecretManifest`). Decrypted
  manifests' `metadata.namespace` is intentionally ignored — this prevents
  git-write access from escalating to arbitrary-namespace cluster-write.
- **Strict field whitelist** for decrypted manifests — only `apiVersion`,
  `kind`, `metadata` (name/namespace/labels/annotations), `type`, `data`,
  `stringData`. Anything else is a parse error.
- **Adoption is opt-in.** The operator refuses to modify pre-existing
  un-managed Secrets without `spec.target.adopt: true`. Cross-CR ownership
  conflicts are always refused — there is no escape hatch.
- **Strict SSH `knownHosts`.** No insecure-skip option for `GitAuth.Type=ssh`.
- **SOPS MAC verification stays on.** Don't disable it.

If your change touches any of these, flag it in the PR.

[envtest]: https://book.kubebuilder.io/reference/envtest.html
[logcheck]: https://github.com/kubernetes-sigs/logtools/tree/main/logcheck
[cc]: https://www.conventionalcommits.org/
