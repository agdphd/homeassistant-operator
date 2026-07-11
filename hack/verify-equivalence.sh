#!/usr/bin/env bash
# verify-equivalence.sh — fail if the Kustomize and Helm renders diverge on the
# security-critical operator fields (FR-002, SC-005):
#   - manager ClusterRole .rules
#   - Deployment pod + container securityContext
#   - operator Namespace Pod Security Admission labels
#
# Naming (release prefixes) and the image reference legitimately differ between
# paths, so only the fields above are compared. Intended, documented differences
# can be recorded in hack/equivalence-allowlist.txt (one field name per line).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
# shellcheck source=hack/lib-helm.sh
source "$ROOT/hack/lib-helm.sh"

YQ="$(hh_yq)"
hh_require helm
ALLOWLIST="$ROOT/hack/equivalence-allowlist.txt"

k_render="$(mktemp)"; h_render="$(mktemp)"
trap 'rm -f "$k_render" "$h_render"' EXIT

hh_render_kustomize config/default > "$k_render"
hh_render_helm --set namespace.create=true > "$h_render"

# extract <render-file> <field>
extract() {
  local f="$1" field="$2"
  case "$field" in
    rules)
      "$YQ" ea 'select(.kind=="ClusterRole" and (.metadata.name | test("manager-role$"))) | .rules | (... comments="") | sort_keys(..)' "$f" ;;
    containerSecurityContext)
      "$YQ" ea 'select(.kind=="Deployment") | .spec.template.spec.containers[0].securityContext | (... comments="") | sort_keys(..)' "$f" ;;
    podSecurityContext)
      "$YQ" ea 'select(.kind=="Deployment") | .spec.template.spec.securityContext | (... comments="") | sort_keys(..)' "$f" ;;
    psaLabels)
      "$YQ" ea 'select(.kind=="Namespace") | .metadata.labels | with_entries(select(.key | test("pod-security.kubernetes.io"))) | (... comments="") | sort_keys(..)' "$f" ;;
  esac
}

rc=0
for field in rules containerSecurityContext podSecurityContext psaLabels; do
  if [ -f "$ALLOWLIST" ] && grep -qxF "$field" "$ALLOWLIST"; then
    echo "⏭️  $field — intended difference (allowlisted), skipping"
    continue
  fi
  if diff <(extract "$k_render" "$field") <(extract "$h_render" "$field") >/tmp/eq.diff 2>&1; then
    echo "✅ $field — Kustomize and Helm equivalent"
  else
    echo "❌ $field — Kustomize and Helm diverge:" >&2
    echo "   (< Kustomize   > Helm)" >&2
    sed 's/^/   /' /tmp/eq.diff >&2
    rc=1
  fi
done

[ "$rc" -eq 0 ] && echo "✅ operator renders are equivalent across install paths"
exit $rc
