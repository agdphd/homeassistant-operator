# Home Assistant Operator

[![Lint](https://github.com/przemekhys/homeassistant-operator/actions/workflows/lint.yml/badge.svg)](https://github.com/przemekhys/homeassistant-operator/actions/workflows/lint.yml)
[![Tests](https://github.com/przemekhys/homeassistant-operator/actions/workflows/test.yml/badge.svg)](https://github.com/przemekhys/homeassistant-operator/actions/workflows/test.yml)
[![E2E Tests](https://github.com/przemekhys/homeassistant-operator/actions/workflows/test-e2e.yml/badge.svg)](https://github.com/przemekhys/homeassistant-operator/actions/workflows/test-e2e.yml)
[![Security Scan](https://github.com/przemekhys/homeassistant-operator/actions/workflows/security.yml/badge.svg)](https://github.com/przemekhys/homeassistant-operator/actions/workflows/security.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/przemekhys/homeassistant-operator)](https://goreportcard.com/report/github.com/przemekhys/homeassistant-operator)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

A Kubernetes operator that simplifies deploying and managing [Home Assistant](https://www.home-assistant.io/) instances on Kubernetes clusters, with a primary focus on lightweight environments like k3s on Raspberry Pi.

## Overview

The Home Assistant Operator automates the deployment and lifecycle management of Home Assistant instances in Kubernetes. Instead of manually creating Deployments, Services, PVCs, and Ingresses, you simply define a `HomeAssistant` custom resource and the operator handles the rest.


### Target Environment

- **Primary**: k3s on Raspberry Pi 4/5 (ARM64)
- **Also supported**: Any Kubernetes cluster (AMD64/ARM64)

## Project Status

**Alpha** - The project is in early development. CRDs and APIs may change.

| CRD | Status | Description |
|-----|--------|-------------|
| `HomeAssistant` | Alpha | Core Home Assistant deployment |
| `HomeAssistantSecrets` | Alpha | Declarative secrets management |
| `HomeAssistantConfiguration` | Alpha | Declarative configuration with hot-reload |
| `HomeAssistantAutomation` | Alpha | Declarative automation management |
| `HomeAssistantScene` | Alpha | Declarative scene management with hot-reload |

## Quick Start

### Prerequisites

- Kubernetes cluster (v1.24+)
- kubectl configured to access your cluster
- For development: Go 1.25+, Docker

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

See example configurations in [config/samples/](config/samples/):
- [ha_v1alpha1_homeassistant.yaml](config/samples/ha_v1alpha1_homeassistant.yaml) - Basic deployment
- [ha_v1alpha1_homeassistant_with_bootstrap.yaml](config/samples/ha_v1alpha1_homeassistant_with_bootstrap.yaml) - With automatic bootstrap
- [ha_v1alpha1_homeassistant_with_config.yaml](config/samples/ha_v1alpha1_homeassistant_with_config.yaml) - With configuration management
- [complete_example_with_secrets.yaml](config/samples/complete_example_with_secrets.yaml) - Complete example with secrets


### Uninstallation

```sh
# Delete Home Assistant instances
kubectl delete homeassistants --all

# Remove CRDs
kubectl delete -f https://raw.githubusercontent.com/przemekhys/homeassistant-operator/main/config/crd/bases/ha.homeassistant.io_homeassistants.yaml

# Remove operator
kubectl delete -f https://raw.githubusercontent.com/przemekhys/homeassistant-operator/main/dist/install.yaml
```

#### Example Configurations

See complete examples in [config/samples/](config/samples/):

- **[ha_v1alpha1_homeassistant_with_config.yaml](config/samples/ha_v1alpha1_homeassistant_with_config.yaml)** - Basic configuration with `auto` strategy (recommended)
- **[haconfig_hot_reload_strategy.yaml](config/samples/haconfig_hot_reload_strategy.yaml)** - Force hot-reload strategy
- **[haconfig_restart_strategy.yaml](config/samples/haconfig_restart_strategy.yaml)** - Force restart strategy
- **[complete_example_with_secrets.yaml](config/samples/complete_example_with_secrets.yaml)** - Complete example with secrets management


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

- **GitHub Issues**: For bugs, feature requests, questions, and community support

## Security

This project uses automated security scanning to ensure dependencies are free of known vulnerabilities:

- **[govulncheck](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck)**: Scans Go dependencies against the [Go Vulnerability Database](https://vuln.go.dev/)
  - Runs on every pull request (blocks merge if vulnerabilities found)
  - Local check: `make security-check`

- **[Dependabot](https://docs.github.com/en/code-security/dependabot)**: Automated dependency updates
  - Weekly scans (Fridays at 23:00 UTC)
  - Automatically creates PRs for security patches
  - Monitors Go modules, GitHub Actions, and Docker dependencies

## License

Copyright 2026.

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE) for the full license text.

## Acknowledgments

- [Home Assistant](https://www.home-assistant.io/) - The amazing home automation platform
- [Operator SDK](https://sdk.operatorframework.io/) - Framework for building Kubernetes operators
- [Kubebuilder](https://book.kubebuilder.io/) - SDK for building Kubernetes APIs
