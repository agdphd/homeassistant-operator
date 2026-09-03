# Installation

## Prerequisites

- Kubernetes cluster v1.24+
- `kubectl` configured to access your cluster

## Install via Helm (recommended)

```sh
helm install homeassistant-operator oci://ghcr.io/przemekhys/charts/homeassistant-operator \
  --version 0.10.0 \
  --namespace homeassistant-operator-system \
  --create-namespace
```

Verify the operator is running:

```sh
kubectl get pods -n homeassistant-operator-system
```

### Customise the installation

Download the default values and override what you need:

```sh
helm show values oci://ghcr.io/przemekhys/charts/homeassistant-operator --version 0.10.0 > values.yaml
# edit values.yaml, then:
helm install homeassistant-operator oci://ghcr.io/przemekhys/charts/homeassistant-operator \
  --version 0.10.0 \
  --namespace homeassistant-operator-system \
  --create-namespace \
  -f values.yaml
```

### Restrict watched namespaces (`watchNamespaces`)

By default the operator watches **all** namespaces in the cluster, which requires a cluster-wide `ClusterRoleBinding`. To follow the principle of least privilege, set `watchNamespaces` to the list of namespaces where Home Assistant actually runs:

```yaml
# values.yaml
watchNamespaces:
  - homeassistant
  - homeassistant-dev
```

| Mode | `watchNamespaces` | RBAC generated | Scope |
|------|-------------------|----------------|-------|
| Cluster-wide (default) | `[]` | `ClusterRoleBinding` | All namespaces |
| Namespace-scoped | non-empty list | one `RoleBinding` per listed namespace | Only listed namespaces |

When set, the operator receives per-namespace `RoleBinding` objects instead of the `ClusterRoleBinding`, and the `WATCH_NAMESPACES` environment variable is injected automatically.

!!! warning "The operator's own namespace is not auto-included"
    Add `homeassistant-operator-system` to the list explicitly if you deploy `HomeAssistant` resources into the same namespace as the operator itself.

!!! note "ClusterRoleBinding mode is deprecated"
    The default `watchNamespaces: []` (cluster-wide) mode is **deprecated since v1.1.0** and planned for removal in **v2.0.0**. See [DEPRECATIONS.md](https://github.com/przemekhys/homeassistant-operator/blob/main/DEPRECATIONS.md) for details.

**Migrating with kustomize** (non-Helm users):

1. Remove `config/rbac/role_binding.yaml` (the `ClusterRoleBinding`).
2. Apply `config/rbac/watched_namespace_role_binding.yaml` in each watched namespace.
3. Set the `WATCH_NAMESPACES` environment variable on the operator `Deployment`.

### Upgrade

```sh
helm upgrade homeassistant-operator oci://ghcr.io/przemekhys/charts/homeassistant-operator \
  --version <new-version> \
  --namespace homeassistant-operator-system
```

### Uninstall (Helm)

!!! warning
    Delete all custom resources **before** removing the operator. The operator must be running to process finalizers (automation/scene/script/integration cleanup). Deleting the operator first causes CRs with finalizers to hang indefinitely.

```sh
# 1. Delete all custom resources (keep operator running to process finalizers)
kubectl delete homeassistants --all -A
kubectl delete homeassistantconfigurations --all -A
kubectl delete homeassistantsecrets --all -A
kubectl delete homeassistantautomations --all -A
kubectl delete homeassistantscenes --all -A
kubectl delete homeassistantscripts --all -A
kubectl delete homeassistantintegrations --all -A

# 2. Uninstall the Helm release (removes the operator and CRDs)
helm uninstall homeassistant-operator -n homeassistant-operator-system
```

## Install via manifest

If you prefer a plain `kubectl apply` without Helm:

```sh
kubectl apply -f https://raw.githubusercontent.com/przemekhys/homeassistant-operator/v0.10.0/dist/install.yaml
```

This installs:

- All CRDs (`HomeAssistant`, `HomeAssistantSecrets`, `HomeAssistantConfiguration`, etc.)
- The operator `Deployment` in namespace `homeassistant-operator-system`
- RBAC (`ClusterRole`, `ClusterRoleBinding`, `ServiceAccount`)

### Uninstall (manifest)

!!! warning
    Delete all custom resources **before** removing the operator (same reason as above).

```sh
# 1. Delete all custom resources
kubectl delete homeassistants --all -A
kubectl delete homeassistantconfigurations --all -A
kubectl delete homeassistantsecrets --all -A
kubectl delete homeassistantautomations --all -A
kubectl delete homeassistantscenes --all -A
kubectl delete homeassistantscripts --all -A
kubectl delete homeassistantintegrations --all -A

# 2. Remove the operator and CRDs
kubectl delete -f https://raw.githubusercontent.com/przemekhys/homeassistant-operator/v0.10.0/dist/install.yaml
```
