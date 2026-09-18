#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$repo_root"

examples=(api artifacts bloom files notes)

if [[ "${1:-}" == "--list" ]]; then
  printf '%s\n' "${examples[@]}"
  exit 0
fi

if [[ $# -gt 0 ]]; then
  selected=("$@")
else
  selected=("${examples[@]}")
fi

for name in "${selected[@]}"; do
  if [[ ! -d "./examples/$name" ]]; then
    echo "unknown example: $name" >&2
    exit 1
  fi

echo "== $name =="
  go run "./examples/$name"
done
