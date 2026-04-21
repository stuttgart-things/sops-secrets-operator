"""
Common config + labels for sops-secrets-operator resources.
Command-line overrides via `-D config.<field>=<value>`.
"""

import schema

_image = option("config.image")
_namespace = option("config.namespace")
_replicas = option("config.replicas")
_imagePullPolicy = option("config.imagePullPolicy")
_cacheSizeLimit = option("config.cacheSizeLimit")

config: schema.SopsSecretsOperator = schema.SopsSecretsOperator {
    if _image:
        image = _image
    if _namespace:
        namespace = _namespace
    if _replicas:
        replicas = int(_replicas)
    if _imagePullPolicy:
        imagePullPolicy = _imagePullPolicy
    if _cacheSizeLimit:
        cacheSizeLimit = _cacheSizeLimit
}

commonLabels = {
    "app.kubernetes.io/name": config.name
    "app.kubernetes.io/instance": config.name
    "app.kubernetes.io/component": "operator"
    "app.kubernetes.io/part-of": "sops-secrets-operator"
    **config.labels
}

selectorLabels = {
    "app.kubernetes.io/name": config.name
    "app.kubernetes.io/instance": config.name
}
