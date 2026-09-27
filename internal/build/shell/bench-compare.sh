#!/usr/bin/env bash
# ARCHIVED — superseded by `go run ./cmd/buildtool bench-compare`.
#
# The baseline choice (explicit, then the committed reference, then the newest archived
# capture) and the refusal to compare a capture with itself are internal/build/bench's
# and the command's; the command captures for itself rather than calling the other
# helper, so the two scripts' file ownership is one function's. Nothing runs this copy;
# see ../shell/README.md.
set -euo pipefail

# bench-compare.sh — capture a fresh benchmark run and diff it against a
# reference capture with benchstat.
#
# Usage: ./scripts/bench-compare.sh [baseline-file] [current-file]
#
#   baseline-file  Reference capture to compare against. Default: the committed
#                  benchmarks/data/baseline.txt, else the newest capture under
#                  benchmarks/data/archive/.
#   current-file   Where the fresh capture is written.
#                  Default: benchmarks/data/current.txt
#
# This script never writes benchmarks/data/baseline.txt: the fresh capture goes
# through `bench-baseline.sh --capture-only`, so comparing can never replace the
# reference it compares against. Only a deliberate `bench-baseline.sh` run
# refreshes the canonical dump.
#
# Exit codes: 0 compared, 1 missing/invalid baseline, 2 benchstat unavailable.

usage() {
  cat <<'USAGE'
Usage: ./scripts/bench-compare.sh [baseline-file] [current-file]

  baseline-file  Reference capture (default: benchmarks/data/baseline.txt,
                 else the newest file in benchmarks/data/archive/).
  current-file   Fresh capture path (default: benchmarks/data/current.txt).

Never writes benchmarks/data/baseline.txt; capture it with
./scripts/bench-baseline.sh when a new reference is wanted.
USAGE
}

case "${1:-}" in
  -h|--help)
    usage
    exit 0
    ;;
esac

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$repo_root"

archive_dir="$repo_root/benchmarks/data/archive"
canonical="$repo_root/benchmarks/data/baseline.txt"

baseline="${1:-}"
current="${2:-$repo_root/benchmarks/data/current.txt}"

# Choose the baseline before anything is captured: a capture written first
# would otherwise become its own comparison point.
if [[ -n "$baseline" ]]; then
  :
elif [[ -f "$canonical" ]]; then
  baseline="$canonical"
else
  baseline="$(ls -1t "$archive_dir"/*.txt 2>/dev/null | head -n 1 || true)"
fi

if [[ -z "$baseline" ]]; then
  echo "no baseline found: $canonical does not exist and $archive_dir holds no captures" >&2
  echo "capture a reference first with: ./scripts/bench-baseline.sh" >&2
  exit 1
fi

if [[ ! -f "$baseline" ]]; then
  echo "baseline not found: $baseline" >&2
  echo "pass an existing capture, or generate one with: ./scripts/bench-baseline.sh" >&2
  exit 1
fi

mkdir -p "$(dirname "$current")"

# Resolve both paths physically so `./a.txt` and `a.txt` are recognized as the
# same file: comparing a capture against itself always reports "no change".
resolve() {
  local dir
  dir="$(cd "$(dirname "$1")" && pwd -P)"
  printf '%s/%s' "$dir" "$(basename "$1")"
}

baseline_path="$(resolve "$baseline")"
current_path="$(resolve "$current")"
if [[ "$baseline_path" == "$current_path" ]]; then
  echo "baseline and current are the same file: $baseline_path" >&2
  echo "comparing a capture with itself always reports no change; pick a different baseline" >&2
  exit 1
fi

echo "comparing $current against $baseline"
./scripts/bench-baseline.sh "$current" --capture-only

if cmp -s "$baseline" "$current"; then
  echo "warning: $current is byte-identical to $baseline; the diff below compares no change" >&2
fi

if command -v benchstat >/dev/null 2>&1; then
  benchstat "$baseline" "$current"
else
  echo "benchstat is not installed. Compare the files manually:"
  echo "  diff -u '$baseline' '$current'"
  exit 2
fi
