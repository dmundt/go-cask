#!/usr/bin/env bash
set -euo pipefail

# test-bench-scripts.sh — regression test for the benchmark helper scripts.
#
# Issue #211: ./scripts/bench-compare.sh used to capture a fresh run first and
# choose a baseline afterwards, so a no-argument run compared a capture against
# itself and, on the way, overwrote the committed benchmarks/data/baseline.txt.
#
# This test pins the ownership rule that fixes it:
#   * only bench-baseline.sh (run deliberately, without --capture-only) may
#     write benchmarks/data/baseline.txt;
#   * bench-compare.sh never writes it, and refuses to compare a file with
#     itself;
#   * the no-argument baseline choice fails loudly when no reference exists.
#
# It runs in throwaway git repositories with stub `go` and `benchstat`
# executables, so it is fast, offline, and never touches the real repo.

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

failed=0
pass() { echo "ok - $1"; }
fail() {
  echo "not ok - $1" >&2
  failed=1
}
expect_eq() { # expect_eq <desc> <expected> <actual>
  if [[ "$2" == "$3" ]]; then
    pass "$1"
  else
    fail "$1 (expected '$2', got '$3')"
  fi
}
expect_exit() { # expect_exit <desc> <expected-status> <actual-status>
  if [[ "$2" == "$3" ]]; then
    pass "$1"
  else
    fail "$1 (expected exit $2, got $3)"
  fi
}
expect_file() {
  if [[ -f "$2" ]]; then pass "$1"; else fail "$1 (missing file $2)"; fi
}
expect_absent() {
  if [[ ! -e "$2" ]]; then pass "$1"; else fail "$1 (unexpected file $2)"; fi
}
expect_grep() { # expect_grep <desc> <pattern> <file>
  if grep -q -- "$2" "$3"; then pass "$1"; else fail "$1 (no match for '$2' in $3)"; fi
}
expect_same() { # expect_same <desc> <file-a> <file-b>
  if cmp -s "$2" "$3"; then pass "$1"; else fail "$1 ($2 and $3 differ)"; fi
}

# --- stub toolchain --------------------------------------------------------

mkdir -p "$work/bin"
cat > "$work/bin/go" <<'STUB'
#!/usr/bin/env bash
# Stub `go`: emit deterministic benchmark-shaped output; never run a benchmark.
cat <<'OUT'
goos: linux
goarch: amd64
pkg: github.com/dmundt/go-cask/benchmarks
cpu: stub
BenchmarkStub-8   1000   1000 ns/op   10 B/op   1 allocs/op
ok  	github.com/dmundt/go-cask/benchmarks	0.5s
OUT
STUB
cat > "$work/bin/benchstat" <<'STUB'
#!/usr/bin/env bash
printf 'benchstat %s %s\n' "${1:-}" "${2:-}"
STUB
chmod +x "$work/bin/go" "$work/bin/benchstat"
export PATH="$work/bin:$PATH"
"$work/bin/go" test > "$work/stub-out.txt"

# A `go` stub in a directory that holds no benchstat.
mkdir -p "$work/bin-nobenchstat"
ln -s "$work/bin/go" "$work/bin-nobenchstat/go"

# A PATH without any usable `benchstat`, so the "not installed" branch is
# reachable even on a machine that has benchstat installed.
path_without_benchstat() {
  local out="" dir
  IFS=':' read -r -a dirs <<< "$PATH"
  for dir in "${dirs[@]}"; do
    if [[ -z "$dir" ]]; then continue; fi
    if [[ -x "$dir/benchstat" ]]; then continue; fi
    out="${out:+$out:}$dir"
  done
  printf '%s' "$out"
}

# --- fixture ---------------------------------------------------------------

fixture() { # fixture <name> -> prints the repository path
  local dir="$work/$1"
  mkdir -p "$dir/scripts" "$dir/benchmarks/data/archive"
  cp "$script_dir/bench-baseline.sh" "$script_dir/bench-compare.sh" "$dir/scripts/"
  chmod +x "$dir/scripts/"*.sh
  git -C "$dir" init -q -b main
  printf 'committed reference capture\n' > "$dir/benchmarks/data/baseline.txt"
  printf '%s' "$dir"
}

# Run a fixture script from the fixture root; usage: run_fixture <repo> <log> <script> [args...]
run_fixture() {
  local repo="$1" log="$2" script="$3"
  shift 3
  ( cd "$repo" && bash "./scripts/$script" "$@" ) > "$log" 2>&1
}

# --- 1. a no-argument compare must not touch the canonical baseline --------

repo="$(fixture case-default)"
cp "$repo/benchmarks/data/baseline.txt" "$work/case-default.before"
status=0
run_fixture "$repo" "$work/case-default.log" bench-compare.sh || status=$?
expect_exit "no-arg compare succeeds" 0 "$status"
expect_same "no-arg compare leaves the canonical baseline byte-identical" \
  "$work/case-default.before" "$repo/benchmarks/data/baseline.txt"
expect_file "no-arg compare writes the scratch capture" "$repo/benchmarks/data/current.txt"
expect_grep "no-arg compare fetches a baseline before capturing" \
  "comparing .*current.txt against .*baseline.txt" "$work/case-default.log"
expect_grep "no-arg compare diffs the committed baseline, not the capture" \
  "benchstat .*data/baseline.txt .*data/current.txt" "$work/case-default.log"

# --- 2. an explicit baseline must not touch the canonical baseline either ---

repo="$(fixture case-explicit)"
cp "$repo/benchmarks/data/baseline.txt" "$work/case-explicit.before"
printf 'older archived reference\n' > "$repo/benchmarks/data/archive/baseline-old.txt"
status=0
run_fixture "$repo" "$work/case-explicit.log" bench-compare.sh \
  "$repo/benchmarks/data/archive/baseline-old.txt" || status=$?
expect_exit "explicit-baseline compare succeeds" 0 "$status"
expect_same "explicit-baseline compare leaves the canonical baseline byte-identical" \
  "$work/case-explicit.before" "$repo/benchmarks/data/baseline.txt"
expect_grep "explicit-baseline compare diffs the requested baseline" \
  "benchstat .*baseline-old.txt .*current.txt" "$work/case-explicit.log"

# --- 3. comparing a file with itself is refused ----------------------------

repo="$(fixture case-same)"
same="$repo/benchmarks/data/baseline.txt"
status=0
run_fixture "$repo" "$work/case-same.log" bench-compare.sh "$same" "$same" || status=$?
expect_exit "same-file compare is refused" 1 "$status"
expect_grep "same-file compare explains why" "same file" "$work/case-same.log"
expect_absent "same-file compare captures nothing" "$repo/benchmarks/data/current.txt"

# --- 4. no baseline anywhere fails loudly ----------------------------------

repo="$(fixture case-missing)"
rm -f "$repo/benchmarks/data/baseline.txt"
status=0
run_fixture "$repo" "$work/case-missing.log" bench-compare.sh || status=$?
expect_exit "compare without any baseline fails" 1 "$status"
expect_grep "compare without a baseline points at bench-baseline.sh" \
  "bench-baseline.sh" "$work/case-missing.log"
expect_absent "compare without a baseline creates no canonical baseline" \
  "$repo/benchmarks/data/baseline.txt"

# --- 5. with no canonical baseline, the newest archive is the fallback -----

repo="$(fixture case-fallback)"
rm -f "$repo/benchmarks/data/baseline.txt"
printf 'archived reference\n' > "$repo/benchmarks/data/archive/baseline-20260101-000000Z.txt"
status=0
run_fixture "$repo" "$work/case-fallback.log" bench-compare.sh || status=$?
expect_exit "compare falls back to the archive when no canonical baseline exists" 0 "$status"
expect_grep "archive fallback diffs the newest archived capture" \
  "benchstat .*baseline-20260101-000000Z.txt .*current.txt" "$work/case-fallback.log"
expect_absent "archive fallback does not invent a canonical baseline" \
  "$repo/benchmarks/data/baseline.txt"

# --- 6. identical content is called out ------------------------------------

repo="$(fixture case-identical)"
cp "$work/stub-out.txt" "$repo/benchmarks/data/baseline.txt"
status=0
run_fixture "$repo" "$work/case-identical.log" bench-compare.sh || status=$?
expect_exit "identical-content compare still exits 0" 0 "$status"
expect_grep "identical-content compare warns instead of reporting a clean diff" \
  "byte-identical" "$work/case-identical.log"

# --- 7. a missing benchstat keeps the documented exit code 2 ---------------

repo="$(fixture case-nobenchstat)"
cp "$repo/benchmarks/data/baseline.txt" "$work/case-nobenchstat.before"
status=0
( cd "$repo" && PATH="$work/bin-nobenchstat:$(path_without_benchstat)" \
  bash ./scripts/bench-compare.sh ) > "$work/case-nobenchstat.log" 2>&1 || status=$?
expect_exit "missing benchstat exits 2" 2 "$status"
expect_grep "missing benchstat prints the manual diff hint" \
  "diff -u" "$work/case-nobenchstat.log"
expect_same "missing benchstat still leaves the canonical baseline alone" \
  "$work/case-nobenchstat.before" "$repo/benchmarks/data/baseline.txt"

# --- 8. a deliberate run refreshes the canonical baseline and archives it ---

repo="$(fixture case-refresh)"
status=0
run_fixture "$repo" "$work/case-refresh.log" bench-baseline.sh || status=$?
expect_exit "deliberate baseline run succeeds" 0 "$status"
expect_grep "deliberate baseline run refreshes the canonical baseline" \
  "BenchmarkStub" "$repo/benchmarks/data/baseline.txt"
if grep -rq "committed reference capture" "$repo/benchmarks/data/archive"; then
  pass "deliberate baseline run archives the previous canonical baseline"
else
  fail "deliberate baseline run archives the previous canonical baseline"
fi

# --- 9. --capture-only never refreshes the canonical baseline --------------

repo="$(fixture case-capture-only)"
cp "$repo/benchmarks/data/baseline.txt" "$work/case-capture-only.before"
capture="$repo/benchmarks/data/archive/scratch.txt"
status=0
run_fixture "$repo" "$work/case-capture-only.log" bench-baseline.sh \
  "$capture" --capture-only || status=$?
expect_exit "--capture-only succeeds" 0 "$status"
expect_file "--capture-only writes the requested capture" "$capture"
expect_same "--capture-only leaves the canonical baseline byte-identical" \
  "$work/case-capture-only.before" "$repo/benchmarks/data/baseline.txt"

# --- 10. unknown options are rejected --------------------------------------

repo="$(fixture case-usage)"
status=0
run_fixture "$repo" "$work/case-usage.log" bench-baseline.sh --nope || status=$?
expect_exit "unknown option exits 2" 2 "$status"
status=0
run_fixture "$repo" "$work/case-help.log" bench-compare.sh --help || status=$?
expect_exit "--help exits 0" 0 "$status"
expect_grep "--help states who owns the canonical baseline" \
  "baseline.txt" "$work/case-help.log"

if [[ "$failed" -ne 0 ]]; then
  echo "benchmark helper script tests failed" >&2
  exit 1
fi

echo "benchmark helper script tests passed"
