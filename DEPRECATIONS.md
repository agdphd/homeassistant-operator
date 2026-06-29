# Deprecations

This file tracks all deprecated features, their deprecation version, planned removal, and migration path.

| Feature | Deprecated since | Planned removal | Reason | Migration |
|---------|-----------------|-----------------|--------|-----------|
| [ClusterRoleBinding mode](#clusterrolebinding-mode-watchnamespaces) | v1.1.0 | v2.0.0 | Violates least-privilege — operator gains access to all namespaces in the cluster | Set `watchNamespaces` in Helm values |

---

## ClusterRoleBinding mode (`watchNamespaces: []`)

**Deprecated since:** v1.1.0
**Planned removal:** v2.0.0
**Affects:** Helm chart users with default `values.yaml` (i.e. `watchNamespaces` not set)

### Why

When `watchNamespaces` is empty, the operator receives a `ClusterRoleBinding` granting it access to secrets, configmaps, and other resources **across all namespaces** in the cluster. This violates the principle of least privilege — in practice, the operator only needs access to the namespaces where Home Assistant is deployed.

### Migration

Set `watchNamespaces` to the list of namespaces where Home Assistant runs:

```yaml
# values.yaml
watchNamespaces:
  - homeassistant
  - homeassistant-dev
```

This switches from a `ClusterRoleBinding` to per-namespace `RoleBinding` objects, restricting the operator's access to only those namespaces.

If you are using kustomize instead of Helm:
1. Remove `config/rbac/role_binding.yaml` (the `ClusterRoleBinding`).
2. Apply `config/rbac/watched_namespace_role_binding.yaml` in each watched namespace.
3. Set the `WATCH_NAMESPACES` environment variable on the operator `Deployment`.
