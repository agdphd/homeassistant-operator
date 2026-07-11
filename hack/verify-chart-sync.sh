#!/usr/bin/env bash
# verify-chart-sync.sh — fail if the committed chart drifted from config/ (FR-002).
#
# Regenerates the chart's static parts and fails if that produces any change,
# proving the chart was not hand-edited out of sync with config/.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
# shellcheck source=hack/lib-helm.sh
source "$ROOT/hack/lib-helm.sh"

echo "==> Regenerating chart from config/ to detect drift"
KUSTOMIZE="${KUSTOMIZE:-}" YQ="${YQ:-}" HELM_CHART_DIR="$HELM_CHART_DIR" "$ROOT/hack/sync-chart-from-config.sh" >/dev/null

paths=("$HELM_CHART_DIR/crds" "$HELM_CHART_DIR/templates/clusterrole.yaml" "$HELM_CHART_DIR/templates/role.yaml")
if ! git diff --quiet -- "${paths[@]}"; then
  echo "❌ Chart is out of sync with config/. Diff:" >&2
  git --no-pager diff -- "${paths[@]}" >&2
  echo "" >&2
  echo "👉 Run 'make helm-sync' and commit the result." >&2
  exit 1
fi

echo "✅ Chart is in sync with config/"
