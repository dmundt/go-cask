#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$repo_root"

out="${1:-benchmarks/baseline.txt}"
mkdir -p "$(dirname "$out")"

go test ./benchmarks -run=^$ -bench=. -benchmem -count=1 2>&1 | tee "$out"
