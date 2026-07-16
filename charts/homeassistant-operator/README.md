# homeassistant-operator

![Version: 1.1.0](https://img.shields.io/badge/Version-1.1.0-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: 1.1.0](https://img.shields.io/badge/AppVersion-1.1.0-informational?style=flat-square)

A Kubernetes operator for deploying and managing Home Assistant instances with declarative configuration, zero-touch bootstrap, and GitOps-ready automations.

**Homepage:** <https://przemekhys.github.io/homeassistant-operator/>

## Maintainers

| Name | Email | Url |
| ---- | ------ | --- |
| przemekhys |  | <https://github.com/przemekhys> |

## Source Code

* <https://github.com/przemekhys/homeassistant-operator>

## Requirements

Kubernetes: `>=1.24-0`

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| affinity | object | `{}` |  |
| fullnameOverride | string | `""` |  |
| image.pullPolicy | string | `"IfNotPresent"` |  |
| image.repository | string | `"ghcr.io/przemekhys/homeassistant-operator"` |  |
| image.tag | string | `"1.1.0"` |  |
| imagePullSecrets | list | `[]` |  |
| metricsNetworkPolicy.enabled | bool | `false` |  |
| nameOverride | string | `""` |  |
| namespace.create | bool | `false` |  |
| nodeSelector | object | `{}` |  |
| podAnnotations | object | `{}` |  |
| podSecurityContext.runAsNonRoot | bool | `true` |  |
| podSecurityContext.seccompProfile.type | string | `"RuntimeDefault"` |  |
| priorityClassName | string | `""` |  |
| replicaCount | int | `1` |  |
| resources.limits.cpu | string | `"500m"` |  |
| resources.limits.memory | string | `"128Mi"` |  |
| resources.requests.cpu | string | `"10m"` |  |
| resources.requests.memory | string | `"64Mi"` |  |
| securityContext.allowPrivilegeEscalation | bool | `false` |  |
| securityContext.capabilities.drop[0] | string | `"ALL"` |  |
| securityContext.readOnlyRootFilesystem | bool | `true` |  |
| securityContext.runAsGroup | int | `65532` |  |
| securityContext.runAsNonRoot | bool | `true` |  |
| securityContext.runAsUser | int | `65532` |  |
| serviceAccount.annotations | object | `{}` |  |
| serviceAccount.create | bool | `true` |  |
| serviceAccount.name | string | `""` |  |
| tolerations | list | `[]` |  |
| topologySpreadConstraints | list | `[]` |  |
| watchNamespaces | list | `[]` |  |
| webhook.certManager.enabled | bool | `false` |  |
| webhook.enabled | bool | `true` |  |
| webhook.failurePolicy | string | `"Ignore"` |  |
| webhook.port | int | `9443` |  |
