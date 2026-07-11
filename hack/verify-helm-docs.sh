#!/usr/bin/env bash
# verify-helm-docs.sh — fail if the committed chart README drifted from
# values.yaml (FR-009, SC-006).
#
# Regenerates the README into a temp copy and diffs it against the committed one,
# so values.yaml can never change without the docs table being updated.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

: "${HELM_CHART_DIR:=charts/homeassistant-operator}"
: "${HELM_DOCS:=$ROOT/bin/helm-docs}"
command -v "$HELM_DOCS" >/dev/null 2>&1 || [ -x "$HELM_DOCS" ] || {
  echo "❌ helm-docs not found (set HELM_DOCS or run 'make helm-docs-bin')" >&2; exit 2; }

readme="$HELM_CHART_DIR/README.md"
[ -f "$readme" ] || { echo "❌ missing $readme — run 'make helm-docs'" >&2; exit 1; }

backup="$(mktemp)"
cp "$readme" "$backup"
trap 'mv -f "$backup" "$readme"' EXIT # always restore the working-tree copy

"$HELM_DOCS" --chart-search-root="$HELM_CHART_DIR" --skip-version-footer >/dev/null

if ! diff -u "$backup" "$readme" >/tmp/helmdocs.diff 2>&1; then
  echo "❌ Chart README is out of date with values.yaml:" >&2
  sed 's/^/   /' /tmp/helmdocs.diff >&2
  echo "" >&2
  echo "👉 Run 'make helm-docs' and commit charts/homeassistant-operator/README.md." >&2
  exit 1
fi

echo "✅ Chart README is up to date with values.yaml"
