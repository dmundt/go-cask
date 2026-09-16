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

notes="$(./scripts/release-notes.sh "$new_tag" "$from_tag")"
printf '%s\n' "$notes"
