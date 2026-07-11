#!/usr/bin/env bash
# helm-smoke-oci.sh — post-publish smoke test (FR-008/SC-004).
#
# Pulls and installs the EXACT published chart artifact from the OCI registry on
# an ephemeral k3d cluster and asserts the operator becomes Ready. Catches
# packaging/metadata errors that only surface after publication (missing files in
# the package, wrong OCI metadata) before a user hits them.
#
# On failure the release workflow (not this script) raises the maintainer alert
# and marks the release "needs verification"; this script performs no yank.
#
# Requires: k3d, helm, kubectl. VERSION and HELM_REGISTRY come from the environment.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
# shellcheck source=hack/lib-helm.sh
source "$ROOT/hack/lib-helm.sh"

VERSION="${VERSION:-${GITHUB_REF_NAME#v}}"
[ -n "$VERSION" ] || { echo "❌ VERSION not set (published chart version)" >&2; exit 2; }
CLUSTER="${SMOKE_CLUSTER:-helm-smoke}"
NS="${SMOKE_NAMESPACE:-homeassistant-operator-system}"
RELEASE="${SMOKE_RELEASE:-ha-operator-smoke}"
# renovate: datasource=docker depName=rancher/k3s
K3S_VERSION="${K3S_VERSION:-v1.36.2-k3s1}"
OCI="oci://${HELM_REGISTRY}/charts/homeassistant-operator"

for t in k3d helm kubectl; do hh_require "$t"; done

cleanup() { k3d cluster delete "$CLUSTER" >/dev/null 2>&1 || true; }
trap cleanup EXIT

echo "==> Creating ephemeral k3d cluster '$CLUSTER'"
k3d cluster delete "$CLUSTER" >/dev/null 2>&1 || true
k3d cluster create "$CLUSTER" --image "rancher/k3s:$K3S_VERSION" --agents 0 --servers-memory 4g --wait

echo "==> Installing published chart $OCI --version $VERSION"
helm install "$RELEASE" "$OCI" --version "$VERSION" \
  --namespace "$NS" --create-namespace --wait --timeout 180s

echo "==> Verifying operator readiness"
kubectl -n "$NS" rollout status deploy -l control-plane=controller-manager --timeout=180s

n="$(kubectl get crds -o name | grep -c 'homeassistant.io' || true)"
[ "$n" -ge 10 ] || { echo "❌ expected >=10 homeassistant.io CRDs, found $n" >&2; exit 1; }

echo "✅ smoke test passed: published $VERSION installs cleanly from OCI ($n CRDs, operator Ready)."
