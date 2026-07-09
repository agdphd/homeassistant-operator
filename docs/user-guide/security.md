# Security: Pod Security Standards

The operator ships hardened to run under the **`restricted`** [Pod Security
Standard](https://kubernetes.io/docs/concepts/security/pod-security-standards/#restricted)
— the strictest built-in profile — and its namespace is labeled to **enforce** that
profile.

## What is enforced

The operator's own namespace (`homeassistant-operator-system` by default) carries the
Pod Security Admission labels:

```yaml
pod-security.kubernetes.io/enforce: restricted
pod-security.kubernetes.io/enforce-version: latest
pod-security.kubernetes.io/audit: restricted
pod-security.kubernetes.io/audit-version: latest
pod-security.kubernetes.io/warn: restricted
pod-security.kubernetes.io/warn-version: latest
```

The controller-manager pod already satisfies `restricted`:

- runs as a non-root user (`runAsNonRoot: true`),
- `seccompProfile: RuntimeDefault`,
- `allowPrivilegeEscalation: false`,
- all Linux capabilities dropped (`capabilities.drop: ["ALL"]`),
- no host namespaces, `hostPath` volumes, or host ports.

Version `latest` means the namespace always applies the newest `restricted` rules and
automatically tightens on cluster upgrades.

## Scope: operator only

!!! warning "Home Assistant pods are out of scope"
    This enforcement applies **only to the operator's own workloads**. Home Assistant
    pods run in their own namespaces and are deliberately **not** placed under
    `restricted`. Many Home Assistant setups need elevated privileges (for example
    `hostNetwork`, or access to USB/Zigbee devices), which `restricted` would block.

## Enforcement with Helm

The Helm chart's operator pod is restricted-compliant out of the box (see
`securityContext`/`podSecurityContext` in `values.yaml`). Namespace-level enforcement
is **opt-in**, because Helm does not label the namespace it is installed into.

**GitOps / `helm template` (recommended for enforcement).** Set `namespace.create=true`
so the chart renders a `Namespace` carrying the six `restricted` PSA labels. This works
when the rendered manifests are applied directly — e.g. Argo CD, Flux, or a plain
`helm template ... | kubectl apply -f -`:

```bash
helm template ha-operator oci://ghcr.io/przemekhys/charts/homeassistant-operator \
  --namespace homeassistant-operator-system \
  --set namespace.create=true | kubectl apply -f -
```

!!! note "Direct `helm install` cannot create its own release namespace"
    With `helm install`, Helm must store release state in the target namespace *before*
    it applies the chart, so the namespace has to exist first. Do **not** combine
    `namespace.create=true` with `--create-namespace` (the auto-created namespace
    collides with the chart's `Namespace` object). Instead, pre-create and label the
    namespace, then install with `namespace.create=false` (the default):

    ```bash
    kubectl create namespace homeassistant-operator-system
    kubectl label namespace homeassistant-operator-system \
      pod-security.kubernetes.io/enforce=restricted \
      pod-security.kubernetes.io/enforce-version=latest \
      pod-security.kubernetes.io/audit=restricted \
      pod-security.kubernetes.io/audit-version=latest \
      pod-security.kubernetes.io/warn=restricted \
      pod-security.kubernetes.io/warn-version=latest
    helm install ha-operator oci://ghcr.io/przemekhys/charts/homeassistant-operator \
      --namespace homeassistant-operator-system
    ```

The operator pod stays restricted-compliant in every case; only the namespace-level
enforcement differs.

## Behavior without Pod Security Admission

Pod Security Admission is a cluster feature. On clusters where it is disabled (or that
predate it), the labels are **inert** — they never block installation. The pod's
`securityContext` remains compliant, so enforcement takes effect immediately once PSA
is enabled.

## Verifying compliance

A static check validates that the rendered manifests keep satisfying `restricted`:

```bash
make verify-pss
```

It renders **both** shipped install paths — `kustomize build config/default` and
`helm template ... --set namespace.create=true` — and fails (non-zero exit) if any
required PSA namespace label or `securityContext` field is missing or non-compliant.
The same check runs in CI (the `Security Scan` workflow), so a regression on either
path is caught before release.
