#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$repo_root"

archive_dir="$repo_root/benchmarks/data/archive"
mkdir -p "$archive_dir"

baseline="${1:-}"
current="${2:-$repo_root/benchmarks/data/current.txt}"

mkdir -p "$(dirname "$current")"

./scripts/bench-baseline.sh "$current"

if [[ -z "$baseline" ]]; then
  if [[ -f "$repo_root/benchmarks/data/baseline.txt" ]]; then
    baseline="$repo_root/benchmarks/data/baseline.txt"
  else
    baseline="$(ls -1t "$archive_dir"/*.txt 2>/dev/null | head -n 1 || true)"
  fi
fi

if [[ -z "$baseline" || ! -f "$baseline" ]]; then
  echo "baseline not found; generate one with: ./scripts/bench-baseline.sh" >&2
  exit 1
fi

if command -v benchstat >/dev/null 2>&1; then
  benchstat "$baseline" "$current"
else
  echo "benchstat is not installed. Compare the files manually:"
  echo "  diff -u '$baseline' '$current'"
  exit 2
fi
