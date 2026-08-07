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
# selects the operator pod, and allows the given port — and ONLY that port,
# via ONLY one ingress rule with ONLY one peer — from namespaces carrying
# <want-label-key>: enabled (checking both the label key and its value, and
# rejecting any additional/overly-permissive peers or rules that would slip
# past a check that only inspected the first entry).
assert_netpol() {
  local f="$1" suffix="$2" want_port="$3" want_label_key="$4"
  local sel="select(.kind==\"NetworkPolicy\" and (.metadata.name | test(\"${suffix}\$\")))"
  local count selector rule_count from_count port_count port protocol label_value

  count="$("$YQ" eval-all "[${sel}] | length" "$f")"
  if [ "$count" != "1" ]; then
    echo "❌ expected exactly 1 NetworkPolicy named *${suffix}, found ${count}" >&2
    return 1
  fi

  selector="$("$YQ" eval-all "${sel} | .spec.podSelector.matchLabels[\"control-plane\"]" "$f")"
  if [ "$selector" != "controller-manager" ]; then
    echo "❌ ${suffix}: podSelector.matchLabels.control-plane = '${selector}', want 'controller-manager'" >&2
    return 1
  fi

  # control-plane: controller-manager alone would also match any other
  # operator's controller-manager pod in the same namespace — the name
  # label is what actually scopes this policy to this operator's pod.
  # (Not asserted as the *only* two keys: the Helm render legitimately
  # adds app.kubernetes.io/instance here too, which kustomize doesn't.)
  local name_label
  name_label="$("$YQ" eval-all "${sel} | .spec.podSelector.matchLabels[\"app.kubernetes.io/name\"]" "$f")"
  if [ "$name_label" != "homeassistant-operator" ]; then
    echo "❌ ${suffix}: podSelector.matchLabels['app.kubernetes.io/name'] = '${name_label}', want 'homeassistant-operator'" >&2
    return 1
  fi

  rule_count="$("$YQ" eval-all "${sel} | .spec.ingress | length" "$f")"
  if [ "$rule_count" != "1" ]; then
    echo "❌ ${suffix}: expected exactly 1 ingress rule, found ${rule_count}" >&2
    return 1
  fi

  from_count="$("$YQ" eval-all "${sel} | .spec.ingress[0].from | length" "$f")"
  if [ "$from_count" != "1" ]; then
    echo "❌ ${suffix}: expected exactly 1 ingress peer (from), found ${from_count}" >&2
    return 1
  fi

  port_count="$("$YQ" eval-all "${sel} | .spec.ingress[0].ports | length" "$f")"
  if [ "$port_count" != "1" ]; then
    echo "❌ ${suffix}: expected exactly 1 ingress port, found ${port_count}" >&2
    return 1
  fi

  port="$("$YQ" eval-all "${sel} | .spec.ingress[0].ports[0].port" "$f")"
  if [ "$port" != "$want_port" ]; then
    echo "❌ ${suffix}: ingress port = '${port}', want '${want_port}'" >&2
    return 1
  fi

  protocol="$("$YQ" eval-all "${sel} | .spec.ingress[0].ports[0].protocol" "$f")"
  if [ "$protocol" != "TCP" ]; then
    echo "❌ ${suffix}: ingress protocol = '${protocol}', want 'TCP'" >&2
    return 1
  fi

  # The sole peer must be exactly a namespaceSelector on <want-label-key>: enabled
  # — no podSelector/ipBlock alongside it, no extra label keys, no other value.
  local peer_keys
  peer_keys="$("$YQ" eval-all "${sel} | .spec.ingress[0].from[0] | keys | .[]" "$f")"
  if [ "$peer_keys" != "namespaceSelector" ]; then
    echo "❌ ${suffix}: ingress peer has fields [${peer_keys}], want exactly [namespaceSelector]" >&2
    return 1
  fi

  local label_key_count
  label_key_count="$("$YQ" eval-all "${sel} | .spec.ingress[0].from[0].namespaceSelector.matchLabels | keys | length" "$f")"
  if [ "$label_key_count" != "1" ]; then
    echo "❌ ${suffix}: namespaceSelector.matchLabels has ${label_key_count} keys, want exactly 1" >&2
    return 1
  fi

  local label_key
  label_key="$("$YQ" eval-all "${sel} | .spec.ingress[0].from[0].namespaceSelector.matchLabels | keys | .[0]" "$f")"
  if [ "$label_key" != "$want_label_key" ]; then
    echo "❌ ${suffix}: namespaceSelector label key = '${label_key}', want '${want_label_key}'" >&2
    return 1
  fi

  label_value="$("$YQ" eval-all "${sel} | .spec.ingress[0].from[0].namespaceSelector.matchLabels[\"${want_label_key}\"]" "$f")"
  if [ "$label_value" != "enabled" ]; then
    echo "❌ ${suffix}: namespaceSelector.matchLabels.${want_label_key} = '${label_value}', want 'enabled'" >&2
    return 1
  fi

  echo "✅ ${suffix}: exactly 1 rule, 1 peer (${want_label_key}=enabled), 1 port (${want_port}/TCP), podSelector correct"
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
