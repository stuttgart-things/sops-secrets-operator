# KCL bundle — sops-secrets-operator

This directory defines the operator's deployment manifests in [KCL](https://kcl-lang.io/). It renders a single multi-document YAML containing CRDs, Namespace, ServiceAccount, ClusterRole, ClusterRoleBinding, and Deployment — suitable for `kubectl apply -f -` or publishing as a kustomize OCI bundle via the release pipeline.

## Render locally

```sh
kcl run kcl/
# with overrides:
kcl run kcl/ \
  -D config.image=ghcr.io/stuttgart-things/sops-secrets-operator:v0.1.0 \
  -D config.namespace=my-ns \
  -D config.replicas=2
```

## Files

| File | Purpose |
|---|---|
| `kcl.mod` | Module manifest + k8s API dependency |
| `schema.k` | `SopsSecretsOperator` config schema + defaults |
| `labels.k` | Config resolution (CLI `-D` overrides) + common labels |
| `namespace.k` | Namespace resource |
| `serviceaccount.k` | Operator ServiceAccount |
| `rbac.k` | ClusterRole + ClusterRoleBinding |
| `deploy.k` | Operator Deployment (distroless, nonroot, writable cache) |
| `main.k` | Entrypoint — concatenates CRDs + all above |
| `crds/` | CRDs copied from `config/crd/bases/` at release time |

## Configurable fields

Pass via `-D config.<field>=<value>` or via a `--profile` file.

| Field | Default | Notes |
|---|---|---|
| `name` | `sops-secrets-operator` | resource + SA name |
| `namespace` | `sops-secrets-operator-system` | target namespace |
| `image` | `ghcr.io/stuttgart-things/sops-secrets-operator:latest` | pinned at release |
| `imagePullPolicy` | `IfNotPresent` | |
| `replicas` | `1` | |
| `cpuRequest` / `cpuLimit` | `50m` / `500m` | |
| `memoryRequest` / `memoryLimit` | `64Mi` / `256Mi` | |
| `metricsPort` | `8080` | `/metrics` endpoint |
| `healthPort` | `8081` | `/healthz`, `/readyz` |
| `cacheSizeLimit` | `1Gi` | emptyDir for git clones |
| `labels` / `annotations` | `{}` | extra metadata merged in |

## Cache volume

The operator clones git repositories to a local cache directory resolved via `os.UserCacheDir()`. Under the distroless nonroot runtime (UID 65532, read-only root FS), we:

- mount an `emptyDir` at `/home/nonroot/.cache` (sized by `cacheSizeLimit`)
- set `XDG_CACHE_HOME=/home/nonroot/.cache` so `UserCacheDir()` picks it up
- mount a separate `emptyDir` at `/tmp` for SSH known-hosts tempfiles

The cache is recreated on pod restart — repos re-clone on the next reconcile.
