# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-01-06

### Added

- Initial release of Home Assistant Operator
- `HomeAssistant` Custom Resource Definition (CRD) with support for:
  - Version/image configuration (`spec.version`, `spec.image`)
  - Storage configuration with PVC (`spec.storage`)
  - Service configuration (ClusterIP, NodePort, LoadBalancer) (`spec.service`)
  - Ingress configuration with TLS support (`spec.ingress`)
  - Resource limits and requests (`spec.resources`)
  - Timezone configuration (`spec.timezone`)
  - External ConfigMap for `configuration.yaml` (`spec.configurationFrom`)
  - External Secret for `secrets.yaml` (`spec.secretsFrom`)
- Reconciliation controller that manages:
  - StatefulSet for Home Assistant deployment
  - PersistentVolumeClaim for data storage
  - Service for network access
  - Ingress for external access (optional)
- Health checks (liveness and readiness probes)
- Status reporting with phase, conditions, and ready state
- Multi-architecture Docker images (amd64, arm64)
- CI/CD with GitHub Actions (lint, test, e2e tests)
- k3d support for local testing

### Target Environments

- Primary: k3s on Raspberry Pi 4/5 (ARM64)
- Also supported: Any Kubernetes cluster (AMD64/ARM64)

[0.1.0]: https://github.com/przemekhys/homeassistant-operator/releases/tag/v0.1.0
