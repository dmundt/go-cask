#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$repo_root"

archive_dir="$repo_root/benchmarks/data/archive"
mkdir -p "$archive_dir"

stamp="$(date -u +%Y%m%d-%H%M%SZ)"

out="${1:-$archive_dir/baseline-$stamp.txt}"
mkdir -p "$(dirname "$out")"

go test ./benchmarks -run=^$ -bench=. -benchmem -count=1 2>&1 | tee "$out"

canonical="$repo_root/benchmarks/data/baseline.txt"
mkdir -p "$(dirname "$canonical")"
cp "$out" "$canonical"

echo "baseline saved to $out"
echo "latest baseline updated at $canonical"
