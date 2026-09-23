#!/usr/bin/env bash
set -euo pipefail

# bench-baseline.sh — capture benchmark output and (deliberately) refresh the
# canonical reference dump.
#
# Usage: ./scripts/bench-baseline.sh [out-file] [--capture-only]
#
#   out-file        Where the raw capture is written. Default:
#                   benchmarks/data/archive/baseline-<UTC-stamp>.txt
#   --capture-only  Capture only: write out-file and stop. The committed
#                   benchmarks/data/baseline.txt is left untouched. This is the
#                   mode bench-compare.sh calls, so comparing never destroys the
#                   reference it compares against.
#
# This script is the ONLY writer of benchmarks/data/baseline.txt. A refresh
# archives the previous canonical dump under benchmarks/data/archive/ first, so
# the committed reference is never lost to an accident.

usage() {
  cat <<'USAGE'
Usage: ./scripts/bench-baseline.sh [out-file] [--capture-only]

  out-file        Where the raw capture is written. Default:
                  benchmarks/data/archive/baseline-<UTC-stamp>.txt
  --capture-only  Capture only: write out-file and stop; the committed
                  benchmarks/data/baseline.txt is left untouched.

This script is the only writer of benchmarks/data/baseline.txt. Refreshing it
archives the previous canonical dump under benchmarks/data/archive/ first.
USAGE
}

capture_only=false
out=""
for arg in "$@"; do
  case "$arg" in
    --capture-only) capture_only=true ;;
    -h|--help)
      usage
      exit 0
      ;;
    -*)
      echo "unknown option: $arg" >&2
      usage >&2
      exit 2
      ;;
    *)
      if [[ -n "$out" ]]; then
        echo "unexpected extra argument: $arg" >&2
        usage >&2
        exit 2
      fi
      out="$arg"
      ;;
  esac
done

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$repo_root"

archive_dir="$repo_root/benchmarks/data/archive"
canonical="$repo_root/benchmarks/data/baseline.txt"
mkdir -p "$archive_dir"

stamp="$(date -u +%Y%m%d-%H%M%SZ)"
if [[ -z "$out" ]]; then
  out="$archive_dir/baseline-$stamp.txt"
fi
mkdir -p "$(dirname "$out")"

echo "capturing benchmark output -> $out"
go test ./benchmarks -run=^$ -bench=. -benchmem -count=1 2>&1 | tee "$out"

if [[ "$capture_only" == "true" ]]; then
  echo "capture-only: left $canonical untouched"
  exit 0
fi

# Archive the previous canonical dump before replacing it. Never overwrite the
# capture we just wrote, and never overwrite an existing archived dump.
if [[ -f "$canonical" ]] && [[ ! "$canonical" -ef "$out" ]]; then
  archive="$archive_dir/baseline-$stamp.txt"
  n=1
  while [[ -e "$archive" ]]; do
    archive="$archive_dir/baseline-$stamp-$n.txt"
    n=$((n + 1))
  done
  cp "$canonical" "$archive"
  echo "previous canonical baseline archived at $archive"
fi

if [[ "$canonical" -ef "$out" ]]; then
  echo "baseline saved to $out"
  echo "canonical baseline already at $canonical"
  exit 0
fi

mkdir -p "$(dirname "$canonical")"
cp "$out" "$canonical"

echo "baseline saved to $out"
echo "latest baseline updated at $canonical"
