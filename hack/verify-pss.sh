#!/usr/bin/env bash
# verify-pss.sh — Static check that the operator manifests satisfy and enforce the
# "restricted" Pod Security Standard (version latest).
#
# Verifies BOTH shipped install paths:
#   1) Kustomize   — `kustomize build config/default` (dist/install.yaml, E2E).
#   2) Helm chart  — `helm template charts/... --set namespace.create=true` (primary
#      user-facing install path).
#
# For each render it checks that the operator namespace carries the 6 PSA labels and
# that the controller-manager pod/container securityContext satisfies restricted.
#
# Exit 0 = compliant; exit 1 = violation (with a readable message); exit 2 = tooling
# error. Scope: operator ONLY — Home Assistant pods are out of scope.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

CHECKER="$ROOT/hack/verify_pss.py"
HELM_CHART="charts/homeassistant-operator"
rc=0

# --- 1) Kustomize render ---------------------------------------------------------
if [ -n "${KUSTOMIZE:-}" ] && [ -x "${KUSTOMIZE}" ]; then
  KBIN="${KUSTOMIZE}"
elif [ -x "bin/kustomize" ]; then
  KBIN="bin/kustomize"
elif command -v kustomize >/dev/null 2>&1; then
  KBIN="kustomize"
else
  KBIN=""
fi

echo "==> Kustomize (config/default)"
if [ -n "$KBIN" ]; then
  "$KBIN" build config/default | python3 "$CHECKER" || rc=1
elif command -v kubectl >/dev/null 2>&1; then
  kubectl kustomize config/default | python3 "$CHECKER" || rc=1
else
  echo "❌ verify-pss: no kustomize/kubectl available to render config/default" >&2
  exit 2
fi

# --- 2) Helm render --------------------------------------------------------------
# namespace.create=true so the chart renders the labeled Namespace to verify.
echo "==> Helm (${HELM_CHART}, namespace.create=true)"
if command -v helm >/dev/null 2>&1; then
  helm template pss-verify "$HELM_CHART" --set namespace.create=true | python3 "$CHECKER" || rc=1
else
  echo "⚠️  verify-pss: helm not found — skipping Helm chart verification" >&2
fi

exit $rc
