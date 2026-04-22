"""
ClusterRole + ClusterRoleBinding for sops-secrets-operator.

The operator needs to:
- watch/update GitRepository, SopsSecret, SopsSecretManifest, InlineSopsSecret
  CRs (namespaced)
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
            resources: ["gitrepositories", "sopssecrets", "sopssecretmanifests", "inlinesopssecrets"]
            verbs: ["get", "list", "watch", "update", "patch"]
        }
        {
            apiGroups: ["sops.stuttgart-things.com"]
            resources: [
                "gitrepositories/status"
                "sopssecrets/status"
                "sopssecretmanifests/status"
                "inlinesopssecrets/status"
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
