#!/usr/bin/env bash
set -euo pipefail

scan() {
  pkgx gitleaks@8.30.1 "$@" --config .gitleaks.toml --redact --no-banner --ignore-gitleaks-allow
}

# Scan every locally available branch/tag, including deleted historical files.
scan git --log-opts='--all --full-history --diff-merges=first-parent' .

# Include staged and unstaged edits before a commit is made.
git diff --cached --no-ext-diff --no-textconv -- . | scan stdin
git diff --no-ext-diff --no-textconv -- . | scan stdin

# New files are not in git diff yet. Ignored profiles/build outputs stay local.
git ls-files --others --exclude-standard -z | while IFS= read -r -d '' file; do
  if [[ -L "$file" ]]; then
    readlink "$file" | scan stdin
  elif [[ -f "$file" ]]; then
    scan dir "$file"
  fi
done
