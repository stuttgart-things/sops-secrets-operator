"""
Schema definition for sops-secrets-operator deployment configuration.
"""

schema SopsSecretsOperator:
    """Configuration for sops-secrets-operator Kubernetes deployment."""

    # Metadata
    name: str = "sops-secrets-operator"
    namespace: str = "sops-secrets-operator-system"

    # Image
    image: str = "ghcr.io/stuttgart-things/sops-secrets-operator:latest"
    imagePullPolicy: str = "IfNotPresent"

    # Replicas and resources
    replicas: int = 1
    cpuRequest: str = "50m"
    cpuLimit: str = "500m"
    memoryRequest: str = "64Mi"
    memoryLimit: str = "256Mi"

    # Ports
    metricsPort: int = 8080
    healthPort: int = 8081

    # Git cache — operator stores per-repo clones under /home/nonroot/.cache/
    # (nonroot UID 65532 has that as HOME). Size it to fit the expected
    # fleet of GitRepository CRs; the cache is recreated on pod restart.
    cacheSizeLimit: str = "1Gi"

    # Labels and annotations
    labels: {str:str} = {}
    annotations: {str:str} = {}

    check:
        replicas >= 0, "replicas must be >= 0"
        name != "", "name must not be empty"
        namespace != "", "namespace must not be empty"
