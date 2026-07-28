#!/usr/bin/env bash
# Builds one GitHub-codeload-style tarball per HACS category fixture in this directory
# (owner/repo/tar.gz/ref always wraps content in a single "owner-repo-<ref>/" directory —
# internal/communityrepo.FetchTarball strips exactly that one top-level directory, so
# fixtures must mirror the same shape).
#
# Usage: test/e2e/fixtures/community-repositories/generate.sh <output-dir>
#
# Tarballs are generated on demand (not checked into git) so the checked-in source trees
# under */src/ remain the single source of truth — nothing to keep in sync by hand.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUT_DIR="${1:?usage: generate.sh <output-dir>}"
mkdir -p "$OUT_DIR"

for category_dir in "$SCRIPT_DIR"/*/; do
  category="$(basename "$category_dir")"
  src="$category_dir/src"
  [ -d "$src" ] || continue

  work="$(mktemp -d)"
  trap 'rm -rf "$work"' EXIT
  wrapper="fixture-${category}-v1.0.0"
  cp -r "$src" "$work/$wrapper"
  tar -C "$work" -czf "$OUT_DIR/${category}.tar.gz" "$wrapper"
  rm -rf "$work"
  trap - EXIT
done

echo "Generated fixtures in $OUT_DIR:"
ls -la "$OUT_DIR"
