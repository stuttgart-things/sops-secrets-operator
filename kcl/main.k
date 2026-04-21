"""
Main entrypoint — renders all sops-secrets-operator deployment manifests.

Includes the generated CRD manifests from kcl/crds/ so the kustomize OCI
bundle produced at release time is self-contained: a single
`kubectl apply -k` installs CRDs, Deployment, RBAC — everything.
"""

import file
import yaml

import namespace
import serviceaccount
import rbac
import deploy

_mod = file.modpath()
_crdFiles = [
    _mod + "/crds/sops.stuttgart-things.com_gitrepositories.yaml"
    _mod + "/crds/sops.stuttgart-things.com_sopssecrets.yaml"
    _mod + "/crds/sops.stuttgart-things.com_sopssecretmanifests.yaml"
]
_crds = [yaml.decode(file.read(f)) for f in _crdFiles]

manifests = _crds + [
    m for m in [
        namespace.ns
        serviceaccount.serviceAccount
        rbac.clusterRole
        rbac.clusterRoleBinding
        deploy.deployment
    ] if m
]
