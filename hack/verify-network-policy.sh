#!/usr/bin/env bash
# verify-network-policy.sh — fail if the operator's own self-protecting
# NetworkPolicy rules (metrics + admission webhook) are missing or malformed
# in either install path (Kustomize config/default, or the Helm chart with
# its opt-in metricsNetworkPolicy enabled).
#
# This is a static, no-cluster check: it only proves the rules are present
# and correctly shaped in the rendered manifests, not that a real CNI
# enforces them at runtime (that class of behavior — the underlying
# NetworkPolicy API itself — is Kubernetes' own well-tested contract, not
# something specific to this operator; see docs/development/testing.md's
# Coverage Gap Record for why this is intentionally not e2e-gated).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
# shellcheck source=hack/lib-helm.sh
source "$ROOT/hack/lib-helm.sh"

YQ="$(hh_yq)"
hh_require helm

rc=0

# assert_netpol <render-file> <name-suffix> <want-port> <want-label-key>
# Verifies exactly one NetworkPolicy whose name ends in <name-suffix> exists,
# selects the operator pod, and allows the given port from namespaces
# carrying <want-label-key>: enabled.
assert_netpol() {
  local f="$1" suffix="$2" want_port="$3" want_label_key="$4"
  local count selector port label_key

  count="$("$YQ" eval-all "[select(.kind==\"NetworkPolicy\" and (.metadata.name | test(\"${suffix}\$\")))] | length" "$f")"
  if [ "$count" != "1" ]; then
    echo "❌ expected exactly 1 NetworkPolicy named *${suffix}, found ${count}" >&2
    return 1
  fi

  selector="$("$YQ" eval-all "select(.kind==\"NetworkPolicy\" and (.metadata.name | test(\"${suffix}\$\"))) | .spec.podSelector.matchLabels[\"control-plane\"]" "$f")"
  if [ "$selector" != "controller-manager" ]; then
    echo "❌ ${suffix}: podSelector.matchLabels.control-plane = '${selector}', want 'controller-manager'" >&2
    return 1
  fi

  port="$("$YQ" eval-all "select(.kind==\"NetworkPolicy\" and (.metadata.name | test(\"${suffix}\$\"))) | .spec.ingress[0].ports[0].port" "$f")"
  if [ "$port" != "$want_port" ]; then
    echo "❌ ${suffix}: ingress port = '${port}', want '${want_port}'" >&2
    return 1
  fi

  label_key="$("$YQ" eval-all "select(.kind==\"NetworkPolicy\" and (.metadata.name | test(\"${suffix}\$\"))) | .spec.ingress[0].from[0].namespaceSelector.matchLabels | keys | .[0]" "$f")"
  if [ "$label_key" != "$want_label_key" ]; then
    echo "❌ ${suffix}: namespaceSelector label key = '${label_key}', want '${want_label_key}'" >&2
    return 1
  fi

  echo "✅ ${suffix}: podSelector/port/${want_label_key}-label all correct (port ${want_port})"
}

# assert_absent <render-file> <name-suffix> — fail if a matching NetworkPolicy exists.
assert_absent() {
  local f="$1" suffix="$2" count
  count="$("$YQ" eval-all "[select(.kind==\"NetworkPolicy\" and (.metadata.name | test(\"${suffix}\$\")))] | length" "$f")"
  if [ "$count" != "0" ]; then
    echo "❌ expected NO NetworkPolicy named *${suffix}, found ${count}" >&2
    return 1
  fi
  echo "✅ ${suffix}: correctly absent"
}

echo "==> Kustomize (config/default) — always-on install path"
k_render="$(mktemp)"; trap 'rm -f "$k_render"' EXIT
hh_render_kustomize config/default > "$k_render"
assert_netpol "$k_render" "allow-metrics-traffic" 8443 metrics || rc=1
assert_netpol "$k_render" "allow-webhook-traffic" 9443 webhook || rc=1

echo "==> Helm, metricsNetworkPolicy.enabled=true (webhook.enabled=true default)"
h_render="$(mktemp)"; trap 'rm -f "$k_render" "$h_render"' EXIT
hh_render_helm --set metricsNetworkPolicy.enabled=true > "$h_render"
assert_netpol "$h_render" "allow-metrics-traffic" 8443 metrics || rc=1
assert_netpol "$h_render" "allow-webhook-traffic" 9443 webhook || rc=1

echo "==> Helm, metricsNetworkPolicy.enabled=true + webhook.enabled=false — no dangling rule"
h_render_nowh="$(mktemp)"; trap 'rm -f "$k_render" "$h_render" "$h_render_nowh"' EXIT
hh_render_helm --set metricsNetworkPolicy.enabled=true --set webhook.enabled=false > "$h_render_nowh"
assert_netpol "$h_render_nowh" "allow-metrics-traffic" 8443 metrics || rc=1
assert_absent "$h_render_nowh" "allow-webhook-traffic" || rc=1

echo "==> Helm defaults (metricsNetworkPolicy.enabled=false) — neither rule renders"
h_render_default="$(mktemp)"; trap 'rm -f "$k_render" "$h_render" "$h_render_nowh" "$h_render_default"' EXIT
hh_render_helm > "$h_render_default"
assert_absent "$h_render_default" "allow-metrics-traffic" || rc=1
assert_absent "$h_render_default" "allow-webhook-traffic" || rc=1

[ "$rc" -eq 0 ] && echo "✅ operator NetworkPolicy rules present and correctly shaped in every install path"
exit $rc
