# Installation

## Prerequisites

- Kubernetes cluster v1.24+
- `kubectl` configured to access your cluster

## Install via manifest

```sh
kubectl apply -f https://raw.githubusercontent.com/przemekhys/homeassistant-operator/v0.9.0/dist/install.yaml
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

!!! warning
    Delete all custom resources **before** removing the operator. The operator must be running to process finalizers (automation/scene/script/integration/floor/label/area cleanup). Deleting the operator first causes CRs with finalizers to hang indefinitely.

```sh
# 1. Delete all custom resources (keep operator running to process finalizers)
kubectl delete homeassistants --all -A
kubectl delete homeassistantconfigurations --all -A
kubectl delete homeassistantsecrets --all -A
kubectl delete homeassistantautomations --all -A
kubectl delete homeassistantscenes --all -A
kubectl delete homeassistantscripts --all -A
kubectl delete homeassistantintegrations --all -A
kubectl delete homeassistantfloors --all -A
kubectl delete homeassistantlabels --all -A
kubectl delete homeassistantareas --all -A

# 2. Remove the operator and CRDs
kubectl delete -f https://raw.githubusercontent.com/przemekhys/homeassistant-operator/v0.9.0/dist/install.yaml
```
