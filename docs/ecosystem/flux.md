# Flux CD

Deploy the Home Assistant Operator and manage its custom resources declaratively with [Flux](https://fluxcd.io/), instead of running `helm install` / `kubectl apply` by hand. This is the GitOps pattern: the desired state (operator chart version, `HomeAssistant` spec, configuration) lives in Git, and Flux continuously reconciles the cluster to match it.

## Example layout

A typical setup keeps the operator's `HelmRelease` and the Home Assistant custom resources together in one directory, applied by a single Flux `Kustomization`:

```
clusters/my-cluster/homeassistant/
├── kustomization.yaml
├── namespace.yaml
├── helm-repository.yaml
├── helm-release.yaml
└── resources/
    ├── homeassistant.yaml
    └── configuration.yaml
```

**`kustomization.yaml`** (plain `kustomize`, not the Flux CRD — this just lists what to apply):

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - namespace.yaml
  - helm-repository.yaml
  - helm-release.yaml
  - resources/homeassistant.yaml
  - resources/configuration.yaml
```

**`helm-repository.yaml`** — the chart is published as an OCI artifact, so `HelmRepository` just needs `type: oci`:

```yaml
apiVersion: source.toolkit.fluxcd.io/v1
kind: HelmRepository
metadata:
  name: homeassistant-operator
  namespace: flux-system
spec:
  type: oci
  interval: 1h
  url: oci://ghcr.io/przemekhys/charts
```

**`helm-release.yaml`**:

```yaml
apiVersion: helm.toolkit.fluxcd.io/v2
kind: HelmRelease
metadata:
  name: homeassistant-operator
  namespace: flux-system
spec:
  interval: 10m
  chart:
    spec:
      chart: homeassistant-operator
      version: ">=1.0.0 <2.0.0"
      sourceRef:
        kind: HelmRepository
        name: homeassistant-operator
        namespace: flux-system
  targetNamespace: homeassistant-operator-system
  install:
    createNamespace: true
  values:
    watchNamespaces:
      - homeassistant
```

**`resources/homeassistant.yaml`** and **`resources/configuration.yaml`** are just the `HomeAssistant` and `HomeAssistantConfiguration` CRs, committed to Git like any other manifest — see [Home Assistant CR](../user-guide/homeassistant.md) for the full spec.

Finally, a Flux `Kustomization` (the CRD, not the plain-kustomize file above) points at this directory:

```yaml
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: homeassistant
  namespace: flux-system
spec:
  interval: 10m0s
  path: ./clusters/my-cluster/homeassistant
  prune: true
  sourceRef:
    kind: GitRepository
    name: flux-system
```

## Key decisions

**OCI `HelmRepository`, not a classic chart repo.** The chart is only published to `oci://ghcr.io/przemekhys/charts/homeassistant-operator` (see [Installation](../getting-started/installation.md)) — there's no `index.yaml`-based repo, so `spec.type: oci` is required on the `HelmRepository`.

**Version pinning: watch out for pre-releases.** Once a stable version exists, a semver range like `>=1.0.0 <2.0.0` lets Flux pick up minor/patch updates automatically. But Flux's `HelmRelease` semver matching **does not match pre-release versions** (tags like `1.1.0-rc.0`) even with a range written as `>=1.0.0-0 <2.0.0`. If you need to track a pre-release chart — e.g. testing a release candidate before it goes stable — pin the exact version instead:

```yaml
spec:
  chart:
    spec:
      version: "1.1.0-rc.0"   # exact pin; ranges won't match pre-releases
```

Switch back to a range once the corresponding stable tag is published.

**Custom resources live next to the `HelmRelease`.** Grouping the operator's `HelmRelease` and its `HomeAssistant`/`HomeAssistantConfiguration` CRs in the same directory (and the same Flux `Kustomization`) keeps "the operator" and "what it manages" as one reconciled unit in Git — one `flux diff` or `flux reconcile` covers the whole thing.

## Automatic image updates (security-sensitive)

[Flux Image Automation](https://fluxcd.io/flux/guides/image-update/) can bump `spec.version` in the `HomeAssistant` CR automatically whenever a new Home Assistant image is published, instead of you editing it by hand on every release. This is convenient, but it auto-commits to your Git repo — treat the policy that decides *which* tags qualify as a security control, not just a convenience setting.

**Restrict which tags are eligible — never allow pre-releases.** Home Assistant publishes `dev`/`beta` tags alongside stable calendar-versioned releases (`2026.7.1`). Without a strict filter, Flux could just as easily pick up a beta build. Combine a `filterTags` regex with a `semver` floor so only fully-qualified stable tags are ever considered:

```yaml
apiVersion: image.toolkit.fluxcd.io/v1
kind: ImageRepository
metadata:
  name: homeassistant
  namespace: flux-system
spec:
  image: ghcr.io/home-assistant/home-assistant
  interval: 1h
  provider: generic
---
apiVersion: image.toolkit.fluxcd.io/v1
kind: ImagePolicy
metadata:
  name: homeassistant
  namespace: flux-system
spec:
  imageRepositoryRef:
    name: homeassistant
  filterTags:
    # Stable calendar-versioned tags only (YYYY.MM.patch) — excludes dev/beta
    pattern: '^\d{4}\.\d+\.\d+$'
  policy:
    semver:
      range: ">=2025.0.0"
```

Wire the policy into the CR with the marker comment Flux looks for:

```yaml
apiVersion: ha.homeassistant.io/v1
kind: HomeAssistant
metadata:
  name: home
spec:
  version: "2026.7.1" # {"$imagepolicy": "flux-system:homeassistant:tag"}
```

Then an `ImageUpdateAutomation` periodically checks for a new match and commits the bump:

```yaml
apiVersion: image.toolkit.fluxcd.io/v1
kind: ImageUpdateAutomation
metadata:
  name: homeassistant
  namespace: flux-system
spec:
  interval: 30m
  sourceRef:
    kind: GitRepository
    name: flux-system
  git:
    checkout:
      ref:
        branch: main
    commit:
      author:
        email: flux-bot@users.noreply.github.com
        name: flux-bot
      messageTemplate: "chore: auto-update images"
    push:
      branch: main
  update:
    strategy: Setters
    path: ./clusters/my-cluster
```

**Direct push to `main` vs. a PR gate.** The example above pushes the version bump straight to `main` — no human reviews it before it's live. That's a deliberate trade-off: it's simpler and keeps Home Assistant current with zero manual effort, acceptable for a low-stakes homelab workload where a bad release is easy to roll back (`git revert`). If you'd rather review every auto-bump before it applies, push to a dedicated branch instead and let a PR (with your normal CI) gate the merge:

```yaml
spec:
  git:
    push:
      branch: flux-image-updates   # push here instead of main
      # then open/require a PR from flux-image-updates -> main
```

**Don't point Image Automation at the operator's own image.** The pattern above is for the *workload* the operator manages (Home Assistant itself) — not for the operator. The operator's container image is coupled to the Helm chart's `appVersion`, so there's no independent tag to auto-bump: upgrading the operator means deliberately bumping `HelmRelease.spec.chart.spec.version` (see [Version pinning](#key-decisions) above). The operator holds cluster-wide RBAC to reconcile CRDs across namespaces — that upgrade deserves a human looking at the changelog, not a silent auto-commit.

## See also

- [Home Assistant CR](../user-guide/homeassistant.md) and [Configuration Management](../user-guide/configuration.md) for the full CR specs used in `resources/`.
- [CRD API Reference](../reference/api.md) for every field on every CRD.
- [Installation](../getting-started/installation.md) for the full list of Helm chart values (e.g. `watchNamespaces`).
