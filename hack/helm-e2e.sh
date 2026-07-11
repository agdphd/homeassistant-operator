#!/usr/bin/env bash
# helm-e2e.sh — Helm install/upgrade e2e on k3d (FR-003/FR-004/FR-011).
#
# Part 1 (fresh install): install the HEAD chart on a clean cluster and assert
#   the operator becomes Ready and all CRDs exist.
# Part 2 (upgrade):        install the previous release (N-1) from the OCI
#   registry, then follow the DOCUMENTED upgrade path — `kubectl apply -f crds/`
#   followed by `helm upgrade` to HEAD — and assert the operator is Ready and
#   pre-existing CRs survive.
#
# The upgrade steps mirror docs/user-guide/upgrade.md exactly, so this test also
# guards the documentation. Requires: k3d, docker, helm, kubectl.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
# shellcheck source=hack/lib-helm.sh
source "$ROOT/hack/lib-helm.sh"

CLUSTER="${HELM_E2E_CLUSTER:-helm-e2e}"
NS="${HELM_E2E_NAMESPACE:-homeassistant-operator-system}"
RELEASE="${HELM_E2E_RELEASE:-ha-operator}"
IMG="${IMG:-ghcr.io/przemekhys/homeassistant-operator:v0.9.0}"
IMG_REPO="${IMG%:*}"
IMG_TAG="${IMG##*:}"
K3D_MEMORY="${HELM_E2E_MEMORY:-4g}"
# renovate: datasource=docker depName=rancher/k3s
K3S_VERSION="${K3S_VERSION:-v1.36.2-k3s1}"

for t in k3d docker helm kubectl; do hh_require "$t"; done

cleanup() { k3d cluster delete "$CLUSTER" >/dev/null 2>&1 || true; }
trap cleanup EXIT

echo "==> Creating fresh k3d cluster '$CLUSTER'"
k3d cluster delete "$CLUSTER" >/dev/null 2>&1 || true
k3d cluster create "$CLUSTER" --image "rancher/k3s:$K3S_VERSION" --agents 0 --servers-memory "$K3D_MEMORY" --wait

echo "==> Building and importing the HEAD operator image ($IMG)"
make docker-build IMG="$IMG" >/dev/null
k3d image import "$IMG" -c "$CLUSTER"

wait_ready() {
  echo "    waiting for operator rollout in $NS ..."
  kubectl -n "$NS" rollout status deploy -l control-plane=controller-manager --timeout=180s
}

assert_crds() {
  local n
  n="$(kubectl get crds -o name | grep -c 'homeassistant.io' || true)"
  [ "$n" -ge 10 ] || { echo "❌ expected >=10 homeassistant.io CRDs, found $n" >&2; exit 1; }
  echo "    ✅ $n homeassistant.io CRDs present"
}

# ---- Part 1: fresh install of HEAD ---------------------------------------------
echo ""
echo "==> PART 1: fresh install of HEAD chart"
helm install "$RELEASE" "$HELM_CHART_DIR" \
  --namespace "$NS" --create-namespace \
  --set image.repository="$IMG_REPO" --set image.tag="$IMG_TAG" --set image.pullPolicy=IfNotPresent \
  --wait --timeout 180s
wait_ready
assert_crds
echo "    ✅ fresh install OK"

echo "==> Tearing down fresh install to prepare the upgrade scenario"
helm uninstall "$RELEASE" --namespace "$NS" --wait || true

# ---- Part 2: upgrade from N-1 --------------------------------------------------
N1="$(hh_previous_version)"
if [ -z "$N1" ]; then
  echo "⏭️  PART 2 skipped: no previous release (N-1) available."
  echo "✅ helm-e2e (fresh install) passed."
  exit 0
fi

echo ""
echo "==> PART 2: upgrade from N-1 ($N1) to HEAD"
OCI="oci://${HELM_REGISTRY}/charts/homeassistant-operator"
echo "    installing N-1 chart from $OCI --version $N1"
helm install "$RELEASE" "$OCI" --version "$N1" \
  --namespace "$NS" --create-namespace --wait --timeout 180s
wait_ready

echo "    creating a sample CR to verify it survives the upgrade"
kubectl -n "$NS" apply -f - >/dev/null <<'EOF'
apiVersion: ha.homeassistant.io/v1
kind: HomeAssistantConfiguration
metadata:
  name: e2e-upgrade-probe
spec:
  homeAssistantRef:
    name: e2e-upgrade-probe
EOF

echo "    [documented step] kubectl apply -f $HELM_CHART_DIR/crds/"
kubectl apply -f "$HELM_CHART_DIR/crds/"

echo "    [documented step] helm upgrade to HEAD"
helm upgrade "$RELEASE" "$HELM_CHART_DIR" \
  --namespace "$NS" \
  --set image.repository="$IMG_REPO" --set image.tag="$IMG_TAG" --set image.pullPolicy=IfNotPresent \
  --wait --timeout 180s
wait_ready
assert_crds

kubectl -n "$NS" get homeassistantconfiguration e2e-upgrade-probe >/dev/null \
  && echo "    ✅ pre-existing CR survived the upgrade" \
  || { echo "❌ pre-existing CR was lost during upgrade" >&2; exit 1; }

echo ""
echo "✅ helm-e2e passed: fresh install + upgrade N-1 ($N1) -> HEAD."
