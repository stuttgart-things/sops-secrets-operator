# sops-secrets-operator

A Kubernetes operator that syncs [SOPS](https://github.com/getsops/sops)-encrypted secrets from Git into Kubernetes `Secret` resources — without depending on Flux, Argo, or any other GitOps stack.

## Why another SOPS operator?

`isindir/sops-secrets-operator` is the reference implementation in this space, but it deliberately does not pull from Git — it relies on Flux or Argo (or `kubectl apply`) to deliver encrypted CRs into the cluster. That works if you already run one of those stacks.

This operator bundles the Git-sync into the operator itself, so the workflow is:

1. Encrypt a file with vanilla `sops` and push it to a git repo.
2. Apply a `GitRepository` + a `SopsSecret` / `SopsSecretManifest` CR.
3. The operator decrypts and produces a target `Secret`.

No delivery tool required.

## Architecture

Three CRDs, all in API group `sops.stuttgart-things.com/v1alpha1`:

```
┌──────────────────┐       ┌──────────────────────┐      ┌──────────────┐
│  GitRepository   │◄──────│  SopsSecret          │─────►│  Secret      │
│  (source + auth) │       │  (flat-map → Secret) │      │              │
└──────────────────┘       └──────────────────────┘      └──────────────┘
         ▲
         │                 ┌──────────────────────┐      ┌──────────────┐
         └─────────────────│  SopsSecretManifest  │─────►│  Secret      │
                           │  (passthrough)       │      │              │
                           └──────────────────────┘      └──────────────┘
```

- **`GitRepository`** — connection to a Git repo: URL, branch or pinned revision, poll interval, and either HTTP basic or SSH auth.
- **`SopsSecret`** — **mapping mode**: source file is a SOPS-encrypted flat key/value YAML. `spec.data[]` explicitly picks source keys and renames them into target Secret `data` keys. Unknown keys in the file are dropped; missing declared keys fail-closed.
- **`SopsSecretManifest`** — **pass-through mode**: source file *is* a SOPS-encrypted `kind: Secret` manifest. The decrypted manifest is applied directly, but namespace is overridden authoritatively by the CR.

Both secret CRDs share a single `GitRepository` cache entry, so one repo fed to many secrets is one clone on disk.

## Quick start

### 1. Install

```sh
# CRDs + RBAC + deployment
kubectl apply -k https://github.com/stuttgart-things/sops-secrets-operator/config/default?ref=main
```

The deployment runs in namespace `sops-secrets-operator-system` with a service account scoped to the CRDs plus `Secret` read/write.

### 2. Generate an age keypair

```sh
age-keygen -o age.agekey
# public key is printed; also stored as a comment inside age.agekey
```

### 3. Encrypt a secrets file

Create `prod/app/creds.enc.yaml` with plaintext:

```yaml
database_url: postgres://app@db:5432/app
database_password: s3cret
api_token: xyz
```

Encrypt in place:

```sh
sops --age age1yourPublicKeyHere --encrypt --in-place prod/app/creds.enc.yaml
```

Commit and push to your git repo.

### 4. Apply the CRs

```sh
kubectl create namespace apps

# Git auth token (PAT for HTTPS, or SSH key — see samples)
kubectl -n apps create secret generic git-auth \
  --from-literal=username=git \
  --from-literal=password=ghp_yourToken

# Age decryption key
kubectl -n apps create secret generic sops-age-key \
  --from-file=age.agekey=age.agekey

# GitRepository
cat <<EOF | kubectl apply -f -
apiVersion: sops.stuttgart-things.com/v1alpha1
kind: GitRepository
metadata:
  name: platform-secrets
  namespace: apps
spec:
  url: https://github.com/your-org/secrets.git
  branch: main
  interval: 5m
  auth:
    type: basic
    secretRef:
      name: git-auth
EOF

# SopsSecret — maps three keys out of the decrypted file
cat <<EOF | kubectl apply -f -
apiVersion: sops.stuttgart-things.com/v1alpha1
kind: SopsSecret
metadata:
  name: app-creds
  namespace: apps
spec:
  source:
    repositoryRef:
      name: platform-secrets
    path: prod/app/creds.enc.yaml
  decryption:
    keyRef:
      name: sops-age-key
      key: age.agekey
  data:
    - key: DATABASE_URL
      from: database_url
    - key: DATABASE_PASSWORD
      from: database_password
    - key: API_TOKEN
      from: api_token
EOF
```

### 5. Verify

```sh
kubectl -n apps get gitrepository,sopssecret
kubectl -n apps get secret app-creds -o yaml
```

Both CRs should show `Applied=True` in their status conditions, and the target `Secret` should carry the three declared keys plus operator-owned labels and annotations (`managed-by`, `owner`, `content-hash`, `source-commit`).

## Using both CRDs

### `SopsSecret` — flat key/value mapping

Use when your SOPS file is a flat key/value YAML and you want explicit control over what ends up in the target Secret (renaming, filtering).

- Source must be flat (no nested maps or lists).
- `spec.data[]` declares every target key; unknown source keys are dropped.
- Missing declared source keys fail the reconcile (fail-closed).
- Changing a `data[]` entry between reconciles removes the old key and adds the new one.

### `SopsSecretManifest` — pass-through k8s Secret

Use when your SOPS file *is* an encrypted `kind: Secret` manifest (typically with `--encrypted-regex '^(data|stringData)$'`).

- Strict whitelist on top-level and metadata fields (see [SECURITY.md](./SECURITY.md)).
- Namespace override is authoritative — the CR decides where the Secret lands, not the decrypted file.
- `target.nameOverride` optionally replaces `metadata.name` from the manifest.

## Operator observability

The manager exposes Prometheus metrics at `/metrics`:

| Metric | Type | Labels |
|---|---|---|
| `sops_reconcile_total` | counter | `kind`, `result` |
| `sops_reconcile_errors_total` | counter | `kind`, `stage` (auth / fetch / decrypt / apply) |
| `sops_reconcile_duration_seconds` | histogram | `kind`, `result` |
| `sops_git_fetch_duration_seconds` | histogram | `result` |
| `sops_decrypt_duration_seconds` | histogram | `result` |

Each CR has status conditions you can watch with `kubectl get -o jsonpath='{.status.conditions}'`:
- `GitRepository`: `AuthResolved`, `SourceReady`
- `SopsSecret` / `SopsSecretManifest`: `SourceReady`, `Decrypted`, `Applied`

## Samples

Runnable examples in [`config/samples/`](./config/samples):

- [`sops_v1alpha1_gitrepository.yaml`](./config/samples/sops_v1alpha1_gitrepository.yaml) — HTTP basic + SSH auth variants
- [`sops_v1alpha1_sopssecret.yaml`](./config/samples/sops_v1alpha1_sopssecret.yaml) — mapping mode
- [`sops_v1alpha1_sopssecretmanifest.yaml`](./config/samples/sops_v1alpha1_sopssecretmanifest.yaml) — pass-through mode

## Security model

See [SECURITY.md](./SECURITY.md) for the full threat model. Highlights:

- Namespace override on `SopsSecretManifest` is authoritative (no git-controlled namespace escape).
- SSH auth requires `knownHosts` — no insecure-skip option.
- Adoption of pre-existing un-owned Secrets is opt-in (`target.adopt: true`).
- SOPS MAC verification is preserved (integrity check on encrypted files is not disabled).

## Development

Requires Go 1.26+, `kubebuilder`, and `kubectl`.

```sh
make generate manifests   # regenerate CRD YAML + zz_generated
make build                # build the manager binary
make test                 # run unit + envtest (fetches envtest binaries)
make run                  # run the manager against the current kubecontext
```

The controllers are scaffolded with [kubebuilder v4](https://book.kubebuilder.io/). Key packages:

- `internal/git/` — go-git wrapper with revision pinning + safe cache directory
- `internal/decrypt/` — age-only SOPS decrypt with in-process serialization
- `internal/source/` — per-repo cache registry shared across reconcilers
- `internal/transform/` — pure helpers (flat-YAML parsing, manifest validation, content hashing)
- `internal/keyresolve/` — age key lookup from Secret refs
- `internal/controller/` — the three reconcilers
- `internal/metrics/` — Prometheus counters/histograms

## License

Apache-2.0 — see [LICENSE](./LICENSE).
