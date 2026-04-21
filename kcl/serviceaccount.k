"""
ServiceAccount for sops-secrets-operator.
"""

import labels

serviceAccount = {
    apiVersion: "v1"
    kind: "ServiceAccount"
    metadata: {
        name: labels.config.name
        namespace: labels.config.namespace
        labels: labels.commonLabels
    }
}
