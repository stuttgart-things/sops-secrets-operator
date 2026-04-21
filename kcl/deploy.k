"""
Deployment for sops-secrets-operator.

Includes an emptyDir volume mounted at /home/nonroot/.cache so
internal/git's UserCacheDir-backed clones have a writable home
(distroless runtime uses a read-only root).
"""

import labels

_config = labels.config

deployment = {
    apiVersion: "apps/v1"
    kind: "Deployment"
    metadata: {
        name: _config.name
        namespace: _config.namespace
        labels: labels.commonLabels
    }
    spec: {
        replicas: _config.replicas
        selector: {
            matchLabels: labels.selectorLabels
        }
        strategy: {
            type: "RollingUpdate"
            rollingUpdate: {
                maxSurge: 1
                maxUnavailable: 0
            }
        }
        template: {
            metadata: {
                labels: labels.commonLabels
                annotations: _config.annotations
            }
            spec: {
                serviceAccountName: _config.name
                containers: [
                    {
                        name: "manager"
                        image: _config.image
                        imagePullPolicy: _config.imagePullPolicy
                        args: [
                            "--metrics-bind-address=:{}".format(_config.metricsPort)
                            "--metrics-secure=false"
                            "--health-probe-bind-address=:{}".format(_config.healthPort)
                            "--leader-elect=true"
                        ]
                        env: [
                            {
                                # Force UserCacheDir() to resolve under /home/nonroot,
                                # which is the mount point of the writable emptyDir
                                # volume in this pod (root FS is read-only).
                                name: "XDG_CACHE_HOME"
                                value: "/home/nonroot/.cache"
                            }
                        ]
                        ports: [
                            {
                                name: "metrics"
                                containerPort: _config.metricsPort
                                protocol: "TCP"
                            }
                            {
                                name: "health"
                                containerPort: _config.healthPort
                                protocol: "TCP"
                            }
                        ]
                        resources: {
                            requests: {
                                cpu: _config.cpuRequest
                                memory: _config.memoryRequest
                            }
                            limits: {
                                cpu: _config.cpuLimit
                                memory: _config.memoryLimit
                            }
                        }
                        livenessProbe: {
                            httpGet: {
                                path: "/healthz"
                                port: "health"
                            }
                            initialDelaySeconds: 15
                            periodSeconds: 20
                        }
                        readinessProbe: {
                            httpGet: {
                                path: "/readyz"
                                port: "health"
                            }
                            initialDelaySeconds: 5
                            periodSeconds: 10
                        }
                        securityContext: {
                            readOnlyRootFilesystem: True
                            runAsNonRoot: True
                            allowPrivilegeEscalation: False
                            capabilities: {
                                drop: ["ALL"]
                            }
                        }
                        volumeMounts: [
                            {
                                name: "cache"
                                mountPath: "/home/nonroot/.cache"
                            }
                            {
                                name: "tmp"
                                mountPath: "/tmp"
                            }
                        ]
                    }
                ]
                securityContext: {
                    runAsNonRoot: True
                    runAsUser: 65532
                    runAsGroup: 65532
                    fsGroup: 65532
                    seccompProfile: {
                        type: "RuntimeDefault"
                    }
                }
                volumes: [
                    {
                        name: "cache"
                        emptyDir: {
                            sizeLimit: _config.cacheSizeLimit
                        }
                    }
                    {
                        name: "tmp"
                        emptyDir: {}
                    }
                ]
                affinity: {
                    podAntiAffinity: {
                        preferredDuringSchedulingIgnoredDuringExecution: [
                            {
                                weight: 100
                                podAffinityTerm: {
                                    labelSelector: {
                                        matchLabels: labels.selectorLabels
                                    }
                                    topologyKey: "kubernetes.io/hostname"
                                }
                            }
                        ]
                    }
                }
            }
        }
    }
}
