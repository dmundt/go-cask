#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$repo_root"

new_tag="${1:-}"
from_tag="${2:-}"

if [[ -z "$new_tag" ]]; then
  echo "usage: $0 <new-tag> [from-tag]" >&2
  exit 1
fi

if [[ -z "$from_tag" ]]; then
  from_tag="$(git tag --sort=-version:refname | grep -v "^${new_tag}$" | head -n 1 || true)"
fi

if [[ -z "$from_tag" ]]; then
  echo "no previous tag found; pass a from-tag explicitly" >&2
  exit 1
fi

section="$(awk -v tag="$new_tag" '
  BEGIN { in_section = 0 }
  /^## \[/ {
    if (in_section) exit
    if ($0 ~ "\\[" tag "\\]") {
      in_section = 1
      print
      next
    }
  }
  in_section { print }
' CHANGELOG.md)"

if [[ -z "$section" ]]; then
  echo "no changelog section found for $new_tag" >&2
  exit 1
fi

printf '%s\n\n**Full Changelog**: https://github.com/dmundt/go-cask/compare/%s...%s\n' "$section" "$from_tag" "$new_tag"
