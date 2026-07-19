#!/usr/bin/env bash
# verify-signatures.sh — local reproduction of the release pipeline's signature
# checks (docs/user-guide/signed-releases.md, CI's smoke-oci job).
#
# Verifies, for one published version tag: the container image signature, the
# Helm chart OCI artifact signature, and the signed checksums.txt bundle
# attached to the GitHub Release — all keyless (Sigstore/cosign), pinned to
# this repository's own release workflow identity.
#
# Requires: cosign, gh, crane (or skopeo) to resolve the image manifest digest.
set -euo pipefail

REPO="${REPO:-przemekhys/homeassistant-operator}"
REGISTRY="${REGISTRY:-ghcr.io}"
IMAGE="${REGISTRY}/${REPO}"
CHART_REF="oci://${REGISTRY}/$(echo "${REPO%%/*}" | tr '[:upper:]' '[:lower:]')/charts/homeassistant-operator"
IDENTITY_REGEXP="https://github.com/${REPO}/\\.github/workflows/release\\.yml@refs/tags/.*"
ISSUER="https://token.actions.githubusercontent.com"

usage() {
  echo "Usage: $0 <version-tag>" >&2
  echo "Example: $0 v0.7.0" >&2
  exit 2
}

[ $# -eq 1 ] || usage
VERSION="$1"

for t in cosign gh crane; do
  command -v "$t" >/dev/null 2>&1 || { echo "❌ required tool not found: $t" >&2; exit 2; }
done

echo "==> Verifying container image ${IMAGE}:${VERSION}"
IMAGE_DIGEST="$(crane digest "${IMAGE}:${VERSION}")"
cosign verify \
  --certificate-identity-regexp "$IDENTITY_REGEXP" \
  --certificate-oidc-issuer "$ISSUER" \
  "${IMAGE}@${IMAGE_DIGEST}"

echo "==> Verifying Helm chart ${CHART_REF}:${VERSION#v}"
CHART_DIGEST="$(crane digest "${CHART_REF#oci://}:${VERSION#v}")"
cosign verify \
  --certificate-identity-regexp "$IDENTITY_REGEXP" \
  --certificate-oidc-issuer "$ISSUER" \
  "${CHART_REF#oci://}@${CHART_DIGEST}"

echo "==> Verifying checksums.txt bundle from the ${VERSION} GitHub Release"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT
gh release download "$VERSION" --repo "$REPO" -p 'checksums.txt*' --dir "$WORKDIR"
cosign verify-blob \
  --bundle "$WORKDIR/checksums.txt.bundle" \
  --certificate-identity-regexp "$IDENTITY_REGEXP" \
  --certificate-oidc-issuer "$ISSUER" \
  "$WORKDIR/checksums.txt"

echo "✅ all signatures verified for ${VERSION} (image, chart, checksums bundle)."
