#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$repo_root"

baseline="${1:-benchmarks/data/baseline.txt}"
current="${2:-benchmarks/data/current.txt}"

mkdir -p "$(dirname "$baseline")" "$(dirname "$current")"

./scripts/bench-baseline.sh "$current"

if [[ ! -f "$baseline" ]]; then
  echo "baseline not found: $baseline" >&2
  echo "generate it with: ./scripts/bench-baseline.sh $baseline" >&2
  exit 1
fi

if command -v benchstat >/dev/null 2>&1; then
  benchstat "$baseline" "$current"
else
  echo "benchstat is not installed. Compare the files manually:"
  echo "  diff -u '$baseline' '$current'"
  exit 2
fi
