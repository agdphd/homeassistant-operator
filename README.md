# Home Assistant Operator

[![Lint](https://github.com/przemekhys/homeassistant-operator/actions/workflows/lint.yml/badge.svg)](https://github.com/przemekhys/homeassistant-operator/actions/workflows/lint.yml)
[![Tests](https://github.com/przemekhys/homeassistant-operator/actions/workflows/test.yml/badge.svg)](https://github.com/przemekhys/homeassistant-operator/actions/workflows/test.yml)
[![E2E Tests](https://github.com/przemekhys/homeassistant-operator/actions/workflows/test-e2e.yml/badge.svg)](https://github.com/przemekhys/homeassistant-operator/actions/workflows/test-e2e.yml)
[![Coverage Status](https://img.shields.io/badge/coverage-17.2%25-red)](https://github.com/przemekhys/homeassistant-operator/actions/workflows/test.yml)
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

## Advanced Configuration

### Managing Secrets with HomeAssistantSecrets

The operator provides a `HomeAssistantSecrets` CRD for declarative secret management. Instead of manually creating ConfigMaps or Secrets for `secrets.yaml`, you can reference existing Kubernetes Secrets and the operator will automatically generate and mount the `secrets.yaml` file.

#### Benefits

- **Declarative**: Define secrets in Kubernetes-native way
- **Automatic Updates**: Secrets are automatically regenerated when source Secrets change
- **Integration Ready**: Works seamlessly with Sealed Secrets, External Secrets Operator, or Vault
- **No Manual Mounting**: The operator handles all the volume mounting automatically

#### Example Usage

1. **Create Kubernetes Secrets with your sensitive data:**

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: mqtt-credentials
type: Opaque
stringData:
  mqtt_user: "homeassistant"
  mqtt_password: "changeme"
---
apiVersion: v1
kind: Secret
metadata:
  name: database-credentials
type: Opaque
stringData:
  db_url: "postgresql://ha:password@postgres:5432/homeassistant"
```

2. **Create HomeAssistant instance:**

```yaml
apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistant
metadata:
  name: my-home
spec:
  version: "2024.1"
  storage:
    size: 10Gi
```

3. **Create HomeAssistantSecrets to auto-generate secrets.yaml:**

```yaml
apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantSecrets
metadata:
  name: my-home-secrets
spec:
  homeAssistantRef:
    name: my-home  # Must match HomeAssistant name

  secretRefs:
    # Include specific keys from a Secret
    - name: mqtt-credentials
      keys:
        - mqtt_user
        - mqtt_password

    # Include all keys from a Secret (omit keys field)
    - name: database-credentials
```

The operator will:
1. Collect secrets from referenced Kubernetes Secrets
2. Generate a `secrets.yaml` file with these entries
3. Create a Secret named `my-home-generated-secrets`
4. Mount it into the Home Assistant pod at `/config/secrets.yaml`
5. Automatically restart the pod when secrets change

#### Using Secrets in Home Assistant Configuration

Once configured, you can reference secrets in your `configuration.yaml`:

```yaml
http:
  api_password: !secret http_password

mqtt:
  broker: mqtt.example.com
  username: !secret mqtt_user
  password: !secret mqtt_password

recorder:
  db_url: !secret db_url
```

#### kubectl Commands

```sh
# List HomeAssistantSecrets
kubectl get homeassistantsecrets
# or use short name:
kubectl get hasecrets

# View details
kubectl describe homeassistantsecrets my-home-secrets

# Check status
kubectl get hasecrets my-home-secrets -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}'
```

For complete examples, see [config/samples/complete_example_with_secrets.yaml](config/samples/complete_example_with_secrets.yaml).

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
