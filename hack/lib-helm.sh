#!/usr/bin/env bash
# lib-helm.sh — shared helpers for the Helm chart lifecycle gates.
#
# Sourced by hack/verify-*.sh and hack/helm-*.sh. Provides deterministic render
# and normalization helpers plus a SINGLE source for "previous released version"
# (N-1) discovery, so every gate agrees on what N-1 means.
#
# Not executable on its own; `source` it.

# Repo root (callers may override ROOT before sourcing).
: "${ROOT:="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"}"
: "${HELM_CHART_DIR:=charts/homeassistant-operator}"

# hh_kustomize_bin — echo a usable kustomize binary (env KUSTOMIZE, bin/, PATH).
hh_kustomize_bin() {
  if [ -n "${KUSTOMIZE:-}" ] && [ -x "${KUSTOMIZE}" ]; then
    echo "${KUSTOMIZE}"
  elif [ -x "${ROOT}/bin/kustomize" ]; then
    echo "${ROOT}/bin/kustomize"
  elif command -v kustomize >/dev/null 2>&1; then
    echo "kustomize"
  else
    echo ""
  fi
}

# hh_require <cmd> — abort (rc 2) if a required tool is missing.
hh_require() {
  command -v "$1" >/dev/null 2>&1 || { echo "❌ required tool not found: $1" >&2; exit 2; }
}

# hh_yq — echo a mikefarah yq binary. The gates require mikefarah yq (block-YAML
# output, `del`/`sort_keys` filters); python-yq (jq wrapper) is NOT compatible.
hh_yq() {
  if [ -n "${YQ:-}" ] && [ -x "${YQ}" ]; then
    echo "${YQ}"; return 0
  elif [ -x "${ROOT}/bin/yq" ]; then
    echo "${ROOT}/bin/yq"; return 0
  elif command -v yq >/dev/null 2>&1 && yq --version 2>&1 | grep -qi mikefarah; then
    echo "yq"; return 0
  fi
  echo "❌ mikefarah yq not found — run 'make helm-tools' (installs bin/yq)" >&2
  exit 2
}

# hh_render_kustomize <overlay> — render a kustomize overlay (default config/default).
hh_render_kustomize() {
  local overlay="${1:-config/default}"
  local kbin
  kbin="$(hh_kustomize_bin)"
  if [ -n "$kbin" ]; then
    "$kbin" build "${ROOT}/${overlay}"
  elif command -v kubectl >/dev/null 2>&1; then
    kubectl kustomize "${ROOT}/${overlay}"
  else
    echo "❌ no kustomize/kubectl available to render ${overlay}" >&2
    exit 2
  fi
}

# hh_render_helm [extra helm template args...] — render the chart with default values.
hh_render_helm() {
  hh_require helm
  helm template equivalence-check "${ROOT}/${HELM_CHART_DIR}" "$@"
}

# hh_normalize — read a multi-doc manifest stream on stdin and emit a normalized
# form (sorted keys, Helm/release-managed noise removed) suitable for diffing.
hh_normalize() {
  local yq
  yq="$(hh_yq)"
  "$yq" eval-all '
    del(.metadata.labels["helm.sh/chart"]) |
    del(.metadata.labels["app.kubernetes.io/managed-by"]) |
    del(.metadata.labels["app.kubernetes.io/version"]) |
    del(.metadata.annotations["meta.helm.sh/release-name"]) |
    del(.metadata.annotations["meta.helm.sh/release-namespace"]) |
    (.. | select(tag == "!!map") | keys) as $k | sort_keys(..)
  ' -
}

# hh_previous_version — echo the N-1 released chart version (semver, no leading v).
# Order of resolution:
#   1) HELM_N1_VERSION env override (explicit)
#   2) newest git tag v* strictly older than the current ref (release/PR context)
# Emits empty string if none can be determined (callers decide how to handle).
hh_previous_version() {
  if [ -n "${HELM_N1_VERSION:-}" ]; then
    echo "${HELM_N1_VERSION#v}"
    return 0
  fi
  # "current" is only defined in release context (a tag push, GITHUB_REF_NAME).
  # In PR/local (unreleased) context current is empty, so N-1 = newest release tag.
  local current tags t
  current="${GITHUB_REF_NAME:-}"
  current="${current#v}"
  tags="$(git -C "$ROOT" tag --list 'v*' --sort=-v:refname 2>/dev/null)"
  for t in $tags; do
    t="${t#v}"
    # skip pre-releases and the current version; first remaining is N-1
    case "$t" in *-*) continue;; esac
    if [ -z "$current" ] || [ "$t" != "$current" ]; then
      echo "$t"
      return 0
    fi
  done
  echo ""
}
