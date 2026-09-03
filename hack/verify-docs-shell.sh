#!/usr/bin/env bash
# verify-docs-shell.sh — fail if any shell snippet in the docs cannot be pasted
# into a shell.
#
# The usual culprit is a `<placeholder>`: to a shell, `<` and `>` are
# redirections, so a line ending in `...@<chart-digest>` dies with
# "parse error near '\n'" and two adjacent placeholders die with
# "parse error near '<'". Readers copy these blocks verbatim — that is what they
# are for — so a snippet that cannot even be parsed is a broken instruction.
#
# Use a variable the reader can edit instead:
#     VERSION=1.4.0
#     helm install ... --version "$VERSION"
#
# This only parses (`-n`); nothing is executed.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

command -v python3 >/dev/null 2>&1 || { echo "❌ python3 not found" >&2; exit 2; }

SHELL_BIN="${SHELL_BIN:-}"
if [ -z "$SHELL_BIN" ]; then
  if command -v zsh >/dev/null 2>&1; then SHELL_BIN=zsh; else SHELL_BIN=bash; fi
fi

python3 - "$SHELL_BIN" <<'PY'
import pathlib, re, subprocess, sys, tempfile, os

shell = sys.argv[1]
failures, checked = [], 0
for f in sorted(pathlib.Path("docs").rglob("*.md")):
    if f.name == "api.md":          # generated; contains no shell snippets
        continue
    text = f.read_text()
    for m in re.finditer(r"```(?:bash|sh|shell|console)\n(.*?)```", text, re.S):
        checked += 1
        with tempfile.NamedTemporaryFile("w", suffix=".sh", delete=False) as t:
            t.write(m.group(1))
            path = t.name
        r = subprocess.run([shell, "-n", path], capture_output=True, text=True)
        os.unlink(path)
        if r.returncode != 0:
            line = text[:m.start()].count("\n") + 2
            err = (r.stderr.strip().split("\n") or [""])[0]
            err = err.replace(path, "<snippet>")
            failures.append((f"{f}:{line}", err, m.group(1).strip().split("\n")[0][:70]))

if failures:
    print(f"❌ {len(failures)} of {checked} shell snippets cannot be parsed by {shell}:")
    for loc, err, first in failures:
        print(f"\n  {loc}\n    first line: {first}\n    {err}")
    print("\nReplace <placeholders> with a variable the reader can edit.")
    sys.exit(1)

print(f"✅ all {checked} shell snippets parse ({shell})")
PY
