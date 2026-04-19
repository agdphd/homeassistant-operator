# Installation

## Prerequisites

- Kubernetes cluster v1.24+
- `kubectl` configured to access your cluster

## Install via manifest

```sh
kubectl apply -f https://raw.githubusercontent.com/przemekhys/homeassistant-operator/main/dist/install.yaml
```

This installs:

- All CRDs (`HomeAssistant`, `HomeAssistantSecrets`, `HomeAssistantConfiguration`, etc.)
- The operator `Deployment` in namespace `homeassistant-operator-system`
- RBAC (`ClusterRole`, `ClusterRoleBinding`, `ServiceAccount`)

Verify the operator is running:

```sh
kubectl get pods -n homeassistant-operator-system
```

## Uninstall

```sh
# Remove all Home Assistant instances first
kubectl delete homeassistants --all -A

# Remove the operator and CRDs
kubectl delete -f https://raw.githubusercontent.com/przemekhys/homeassistant-operator/main/dist/install.yaml
```

!!! warning
    Deleting CRDs removes all custom resources of that type. Back up any important CRs before uninstalling.
