[package]
name = "deploy-sops-secrets-operator"
version = "0.1.0"
description = "KCL module for deploying sops-secrets-operator on Kubernetes"

[dependencies]
k8s = "1.31"

[profile]
entries = [
    "main.k"
]
