"""
Namespace for sops-secrets-operator.
"""

import labels

ns = {
    apiVersion: "v1"
    kind: "Namespace"
    metadata: {
        name: labels.config.namespace
        labels: labels.commonLabels
    }
}
