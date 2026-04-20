# Home Assistant Operator

[![Lint](https://github.com/przemekhys/homeassistant-operator/actions/workflows/lint.yml/badge.svg)](https://github.com/przemekhys/homeassistant-operator/actions/workflows/lint.yml)
[![Tests](https://github.com/przemekhys/homeassistant-operator/actions/workflows/test.yml/badge.svg)](https://github.com/przemekhys/homeassistant-operator/actions/workflows/test.yml)
[![E2E Tests](https://github.com/przemekhys/homeassistant-operator/actions/workflows/test-e2e-parallel.yml/badge.svg)](https://github.com/przemekhys/homeassistant-operator/actions/workflows/test-e2e-parallel.yml)
[![Security Scan](https://github.com/przemekhys/homeassistant-operator/actions/workflows/security.yml/badge.svg)](https://github.com/przemekhys/homeassistant-operator/actions/workflows/security.yml)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

A Kubernetes operator for deploying and managing [Home Assistant](https://www.home-assistant.io/) instances with declarative configuration, zero-touch bootstrap, and GitOps-ready automations.

**[Full documentation →](https://przemekhys.github.io/homeassistant-operator/)**

## Quick Start

```sh
kubectl apply -f https://raw.githubusercontent.com/przemekhys/homeassistant-operator/main/dist/install.yaml
```

See the [installation guide](https://przemekhys.github.io/homeassistant-operator/getting-started/installation/) for a full walkthrough.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) or the [contributing guide](https://przemekhys.github.io/homeassistant-operator/development/contributing/).

## Acknowledgments

- [Home Assistant](https://www.home-assistant.io/) - The amazing home automation platform
- [Operator SDK](https://sdk.operatorframework.io/) - Framework for building Kubernetes operators
- [Kubebuilder](https://book.kubebuilder.io/) - SDK for building Kubernetes APIs

## License

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE) for details.
