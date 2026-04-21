# Security model

This document lists the security-relevant guarantees the operator makes and the assumptions it relies on. Read this before deploying to a multi-tenant cluster.

## Threat model

The operator assumes:
- The decryption keys (age secret keys) live in Kubernetes `Secret`s that only the operator and the intended owners can read.
- Kubernetes RBAC is the authoritative boundary for who can create `GitRepository`, `SopsSecret`, and `SopsSecretManifest` CRs in a given namespace.
- Git repositories referenced by `GitRepository` are trusted to the extent that their writers can influence what ends up decrypted — file content, path, and (for `SopsSecretManifest`) some metadata fields.

## Guarantees

### Namespace override is authoritative (`SopsSecretManifest`)

For `SopsSecretManifest`, the target Secret's namespace is **always** determined by `spec.target.namespace` (or the CR's own namespace if unset). The decrypted manifest's `metadata.namespace` is ignored.

Reason: without this rule, any git-repo writer could target any namespace in the cluster by setting `metadata.namespace: kube-system` in the encrypted file. That would turn git-write access into arbitrary-namespace cluster-write access via the operator. The override closes that escalation.

### Strict field whitelist (`SopsSecretManifest`)

Decrypted manifests must contain **only** these top-level fields:
- `apiVersion` (must be `v1`)
- `kind` (must be `Secret`)
- `metadata` (only `name`, `namespace`, `labels`, `annotations` allowed)
- `type`
- `data`, `stringData`

Anything else (`spec`, `status`, `ownerReferences`, `finalizers`, arbitrary metadata subfields) fails parse with a clear error condition. This prevents a decrypted manifest from, e.g., planting an `ownerReference` that causes surprising garbage-collection relationships.

### Adoption is opt-in

When the target Secret already exists and is **not** labelled `sops.stuttgart-things.com/managed-by=sops-secrets-operator`, the reconciler refuses to modify it. The user must explicitly set `spec.target.adopt: true` to take over an un-owned Secret.

When the target Secret **is** managed by this operator but its `sops.stuttgart-things.com/owner` annotation names a different CR, the reconciler always refuses — no escape hatch. This prevents two CRs silently racing for ownership of the same target.

### Strict SSH host-key checking

`GitAuth.Type=ssh` requires the referenced Secret to provide a non-empty `knownHosts` entry. There is no insecure-skip option; strict host-key checking is mandatory.

### SOPS MAC is preserved

Unlike some other SOPS-based Kubernetes integrations, this operator does **not** disable SOPS's MAC verification. Encrypted files on disk are subject to the normal SOPS integrity check, so plaintext tampering of encrypted fields is detected.

## Caveats and sharp edges

### Cache directory

The operator maintains per-repo git clones under `$XDG_CACHE_HOME/sops-secrets-operator` (or `/var/cache/sops-secrets-operator` if `UserCacheDir()` fails). The directory is created with `0700` permissions. Inside a container, protect this directory from sidecars and `exec`-ed shells with the usual pod-spec controls (no shared mounts, restricted `kubectl exec`).

### Decryption key scope

The age key referenced by `SopsSecret.spec.decryption.keyRef` / `SopsSecretManifest.spec.decryption.keyRef` must live in the same namespace as the CR. The operator reads it with namespace-scoped RBAC only.

If you distribute a single age key across namespaces by copying the Secret, any namespace that holds a copy can decrypt anything in the repository — RBAC on CR creation becomes the only limit. For strong tenant isolation, use one age key per namespace.

### Git-repo writers are trusted

A `GitRepository` CR references a URL whose contents are fetched and decrypted. Anyone with write access to that repo can:
- Add new encrypted files that decrypt with the configured key.
- Change the contents of files referenced by existing `SopsSecret` / `SopsSecretManifest` CRs.

Treat write access to the referenced repository as equivalent to namespace-write on the `Secret`s it produces.

### Git revision is optional but recommended

Without `spec.revision`, `GitRepository` follows branch HEAD — which means new commits to the branch are picked up on the next poll. For stable pinning, set `spec.revision` to a commit SHA or tag. This also gives you a clean audit trail via `status.lastSyncedCommit`.

## Reporting a vulnerability

Please email [phermann1988@gmail.com](mailto:phermann1988@gmail.com) with details. Do not open a public issue for undisclosed vulnerabilities.
