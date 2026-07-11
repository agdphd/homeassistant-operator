# Upgrade

This page documents how to install and upgrade the operator with Helm, including
the **explicit CRD update step** — so you never have to guess how to move between
versions.

The chart is published to the OCI registry
`oci://ghcr.io/przemekhys/homeassistant-operator/charts/homeassistant-operator`.

!!! info "Why CRDs need an explicit step"
    Helm installs everything under the chart's `crds/` directory on a **fresh
    install**, but by design it does **not** update those CRDs on `helm upgrade`.
    To pick up CRD schema changes between versions you must apply them yourself
    with `kubectl apply -f` (shown below). This is intentional and safe: it keeps
    Helm from ever deleting a CRD (and your Custom Resources with it).

## Fresh install

```bash
helm install ha-operator \
  oci://ghcr.io/przemekhys/homeassistant-operator/charts/homeassistant-operator \
  --version <VERSION> \
  --namespace homeassistant-operator-system --create-namespace
```

On a fresh install all CRDs are created **before** the operator Deployment, so no
extra step is needed. Verify:

```bash
kubectl -n homeassistant-operator-system rollout status \
  deploy -l control-plane=controller-manager
kubectl get crds | grep homeassistant.io   # expect 10 CRDs
```

## Upgrade from the previous version

The operator supports and tests upgrades from the **directly previous released
version (N-1)** to the latest. Perform the steps **in this order** — CRDs first,
then the Helm release:

```bash
# 1. Update the CRDs explicitly (Helm does NOT do this on upgrade).
#    Pull the exact CRDs for the target version and apply them:
helm pull oci://ghcr.io/przemekhys/homeassistant-operator/charts/homeassistant-operator \
  --version <VERSION> --untar --untardir /tmp/ha-operator-<VERSION>
kubectl apply -f /tmp/ha-operator-<VERSION>/homeassistant-operator/crds/

# 2. Upgrade the Helm release.
helm upgrade ha-operator \
  oci://ghcr.io/przemekhys/homeassistant-operator/charts/homeassistant-operator \
  --version <VERSION> \
  --namespace homeassistant-operator-system

# 3. Verify the upgrade.
kubectl -n homeassistant-operator-system rollout status \
  deploy -l control-plane=controller-manager
kubectl get crds | grep homeassistant.io
```

Your existing Custom Resources (`HomeAssistant`, `HomeAssistantConfiguration`,
automations, scenes, scripts, integrations, …) are preserved across the upgrade;
the CRD apply is additive and does not delete resources.

!!! warning "Do not skip intermediate versions"
    Only the **N-1 → latest** path is tested. If you are several versions behind
    and intermediate releases changed the CRD schema, upgrade through the
    intermediate versions one at a time rather than jumping straight to latest.

## RBAC on upgrade

`helm upgrade` reconciles the operator's RBAC (ClusterRole / Role / bindings) to
exactly what the target chart version declares. The project's CI enforces that a
chart upgrade never silently broadens the operator's permissions — any new
permission must be explicitly justified — so upgrading does not grant the
operator more access than the release notes describe.

## Verifying a successful upgrade

The upgrade succeeded when:

- `kubectl rollout status` reports the controller-manager Deployment is available;
- `kubectl get crds | grep homeassistant.io` lists all 10 CRDs;
- your existing Custom Resources are still present and reconciling
  (`kubectl get ha,haconfig,haauto,hasc,hascp,haint -A`).
