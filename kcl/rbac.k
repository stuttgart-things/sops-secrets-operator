"""
ClusterRole + ClusterRoleBinding for sops-secrets-operator.

The operator needs to:
- watch/update GitRepository, SopsSecret, SopsSecretManifest, InlineSopsSecret
  and ObjectSource CRs (namespaced)
- read + write Secrets across namespaces (auth tokens, age keys,
  target Secrets — potentially cross-namespace via target.namespace)
- emit Events
- hold leader-election Leases
"""

import labels

clusterRole = {
    apiVersion: "rbac.authorization.k8s.io/v1"
    kind: "ClusterRole"
    metadata: {
        name: labels.config.name
        labels: labels.commonLabels
    }
    rules: [
        {
            apiGroups: ["sops.stuttgart-things.com"]
            # EVERY CRD THIS ARTIFACT INSTALLS. objectsources was the one that
            # was missing, and controller-runtime blocks in WaitForCacheSync
            # until EVERY informer syncs -- so one un-listable kind starts no
            # controller at all, not just its own.
            #
            # It fails in the worst possible way: the pod is 1/1 Running (the
            # health probe is a separate bind address), every CR applies
            # cleanly, and none of them ever gets a status. Found on
            # machinery-test1, 2026-09-01, after twenty minutes of a
            # GitRepository and two SopsSecretManifests that looked applied and
            # reconciled nothing.
            #
            # config/rbac/role.yaml already had it: kubebuilder generates that
            # from the +kubebuilder:rbac markers and followed ObjectSource in
            # automatically. This file is hand-written and did not, so a
            # kustomize deploy was fine and the OCI artifact was not.
            # hack/check-kcl-rbac-parity.py now compares the two.
            resources: [
                "gitrepositories"
                "sopssecrets"
                "sopssecretmanifests"
                "inlinesopssecrets"
                "objectsources"
            ]
            verbs: ["get", "list", "watch", "update", "patch"]
        }
        {
            apiGroups: ["sops.stuttgart-things.com"]
            resources: [
                "gitrepositories/status"
                "sopssecrets/status"
                "sopssecretmanifests/status"
                "inlinesopssecrets/status"
                "objectsources/status"
            ]
            verbs: ["get", "update", "patch"]
        }
        {
            apiGroups: ["sops.stuttgart-things.com"]
            resources: [
                "gitrepositories/finalizers"
                "sopssecrets/finalizers"
                "sopssecretmanifests/finalizers"
                "inlinesopssecrets/finalizers"
                "objectsources/finalizers"
            ]
            verbs: ["update"]
        }
        {
            apiGroups: [""]
            resources: ["secrets"]
            verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
        }
        {
            apiGroups: [""]
            resources: ["events"]
            verbs: ["create", "patch"]
        }
        {
            apiGroups: ["coordination.k8s.io"]
            resources: ["leases"]
            verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
        }
    ]
}

clusterRoleBinding = {
    apiVersion: "rbac.authorization.k8s.io/v1"
    kind: "ClusterRoleBinding"
    metadata: {
        name: labels.config.name
        labels: labels.commonLabels
    }
    roleRef: {
        apiGroup: "rbac.authorization.k8s.io"
        kind: "ClusterRole"
        name: labels.config.name
    }
    subjects: [
        {
            kind: "ServiceAccount"
            name: labels.config.name
            namespace: labels.config.namespace
        }
    ]
}
