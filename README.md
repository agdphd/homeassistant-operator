# Home Assistant Operator

[![Lint](https://github.com/przemekhys/homeassistant-operator/actions/workflows/lint.yml/badge.svg)](https://github.com/przemekhys/homeassistant-operator/actions/workflows/lint.yml)
[![Tests](https://github.com/przemekhys/homeassistant-operator/actions/workflows/test.yml/badge.svg)](https://github.com/przemekhys/homeassistant-operator/actions/workflows/test.yml)
[![E2E Tests](https://github.com/przemekhys/homeassistant-operator/actions/workflows/test-e2e.yml/badge.svg)](https://github.com/przemekhys/homeassistant-operator/actions/workflows/test-e2e.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/przemekhys/homeassistant-operator)](https://goreportcard.com/report/github.com/przemekhys/homeassistant-operator)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

A Kubernetes operator that simplifies deploying and managing [Home Assistant](https://www.home-assistant.io/) instances on Kubernetes clusters, with a primary focus on lightweight environments like k3s on Raspberry Pi.

## Overview

The Home Assistant Operator automates the deployment and lifecycle management of Home Assistant instances in Kubernetes. Instead of manually creating Deployments, Services, PVCs, and Ingresses, you simply define a `HomeAssistant` custom resource and the operator handles the rest.

### Key Features

- **Declarative Configuration** - Define your Home Assistant instance as a Kubernetes custom resource
- **Automatic Resource Management** - Operator creates and manages StatefulSets, Services, PVCs, and Ingresses
- **Storage Management** - Persistent storage for Home Assistant configuration and data
- **Flexible Networking** - Support for ClusterIP, NodePort, LoadBalancer, and Ingress
- **Resource Control** - Configure CPU and memory limits for your instance
- **Timezone Support** - Easy timezone configuration for your Home Assistant instance

### Target Environment

- **Primary**: k3s on Raspberry Pi 4/5 (ARM64)
- **Also supported**: Any Kubernetes cluster (AMD64/ARM64)

## Project Status

**Alpha** - The project is in early development. CRDs and APIs may change.

| CRD | Status | Description |
|-----|--------|-------------|
| `HomeAssistant` | Alpha | Core Home Assistant deployment |

## Custom Resource Definitions

### HomeAssistant

The `HomeAssistant` CRD defines a Home Assistant instance with the following configuration options:

| Field | Description | Default |
|-------|-------------|---------|
| `spec.version` | Home Assistant version/tag | `stable` |
| `spec.image` | Container image | `ghcr.io/home-assistant/home-assistant` |
| `spec.timezone` | Timezone (e.g., `Europe/Warsaw`) | `UTC` |
| `spec.storage.size` | PVC size | `5Gi` |
| `spec.storage.storageClassName` | Storage class | cluster default |
| `spec.service.type` | Service type | `ClusterIP` |
| `spec.service.port` | Service port | `8123` |
| `spec.ingress.enabled` | Enable Ingress | `false` |
| `spec.ingress.host` | Ingress hostname | - |
| `spec.resources` | CPU/Memory requests and limits | - |

## Quick Start

### Prerequisites

- Kubernetes cluster (v1.24+)
- kubectl configured to access your cluster
- For development: Go 1.24+, Docker

### Installation

1. **Install the CRDs:**

```sh
kubectl apply -f https://raw.githubusercontent.com/przemekhys/homeassistant-operator/main/config/crd/bases/ha.homeassistant.io_homeassistants.yaml
```

2. **Deploy the operator:**

```sh
kubectl apply -f https://raw.githubusercontent.com/przemekhys/homeassistant-operator/main/dist/install.yaml
```

3. **Create a Home Assistant instance:**

```yaml
apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistant
metadata:
  name: my-home
spec:
  version: "stable"
  timezone: "Europe/Warsaw"
  storage:
    size: "10Gi"
  service:
    type: NodePort
    port: 8123
  resources:
    requests:
      cpu: "100m"
      memory: "256Mi"
    limits:
      cpu: "1000m"
      memory: "1Gi"
```

```sh
kubectl apply -f homeassistant.yaml
```

4. **Access Home Assistant:**

```sh
# For NodePort
kubectl get svc -l app.kubernetes.io/instance=my-home

# For port-forward
kubectl port-forward svc/my-home-homeassistant 8123:8123
```

### Uninstallation

```sh
# Delete Home Assistant instances
kubectl delete homeassistants --all

# Remove CRDs
kubectl delete -f https://raw.githubusercontent.com/przemekhys/homeassistant-operator/main/config/crd/bases/ha.homeassistant.io_homeassistants.yaml

# Remove operator
kubectl delete -f https://raw.githubusercontent.com/przemekhys/homeassistant-operator/main/dist/install.yaml
```

## Development

### Building from Source

```sh
# Clone the repository
git clone https://github.com/przemekhys/homeassistant-operator.git
cd homeassistant-operator

# Build
make build

# Run tests
make test

# Run linter
make lint

# Build Docker image
make docker-build IMG=myregistry/homeassistant-operator:dev
```

### Local Development with k3d

```sh
# Create a test cluster
make k3d-create

# Build and load image into k3d
make k3d-load IMG=controller:latest

# Install CRDs and deploy operator
make install deploy IMG=controller:latest

# Apply sample
kubectl apply -f config/samples/ha_v1alpha1_homeassistant.yaml

# Cleanup
make k3d-delete
```

## Contributing

We welcome contributions! Please see our [Contributing Guide](CONTRIBUTING.md) for details.

### Getting Help

- **GitHub Issues**: For bugs and feature requests
- **Discussions**: For questions and community support

## Security

If you discover a security vulnerability, please report it via GitHub Security Advisories instead of opening a public issue.

## License

Copyright 2026.

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE) for the full license text.

## Acknowledgments

- [Home Assistant](https://www.home-assistant.io/) - The amazing home automation platform
- [Operator SDK](https://sdk.operatorframework.io/) - Framework for building Kubernetes operators
- [Kubebuilder](https://book.kubebuilder.io/) - SDK for building Kubernetes APIs
