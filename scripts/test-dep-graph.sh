#!/usr/bin/env bash
set -euo pipefail

# test-dep-graph.sh — regression test for dep-graph.sh's artifact ownership.
#
# dep-graph.sh owns docs/design/package-graph.md, and scripts/AGENT.md requires
# that owner to be exclusive, with a mode that never touches the file and a test
# in verify.sh that pins the split. This is that test.
#
# What it pins:
#   * a fresh run creates the document at frontmatter version v1;
#   * regenerating an unchanged graph is a byte-for-byte no-op — no rewrite and
#     no version bump, so check-version-fields.sh sees a bump only when the
#     artifact really changed;
#   * a changed graph regenerates and moves the version by one;
#   * --check never writes the document: it exits 0 when the document matches the
#     tree, exits 1 when it does not (or when the document is missing), and
#     leaves the whole working tree untouched in every case;
#   * unknown options exit 2 and --help exits 0.
#
# It runs in throwaway git repositories with a stub `go`, so it is fast, offline,
# and never touches the real repo or the real Go toolchain.

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

# A hash of every tracked-by-nothing file in a fixture, so "writes nothing" can
# be asserted for the whole tree and not just for the generated document.
tree_hash() {
  (
    cd "$1" &&
      find . -path ./.git -prune -o -path ./.gocache -prune -o -type f -print |
      LC_ALL=C sort |
      xargs md5sum
  ) | md5sum
}

# --- stub toolchain --------------------------------------------------------

mkdir -p "$work/bin"
cat > "$work/bin/go" <<'STUB'
#!/usr/bin/env bash
# Stub `go`: answer only the two `go list` shapes dep-graph.sh uses, so the test
# never needs a Go toolchain or a real module.
if [[ "${1:-}" == "list" ]]; then
  for arg in "$@"; do
    if [[ "$arg" == "-m" ]]; then
      echo "github.com/example/stub"
      exit 0
    fi
  done
  printf '%s\n' \
    'github.com/example/stub/cas|context io strings' \
    'github.com/example/stub/cas/backend/fs|context github.com/example/stub/cas' \
    'github.com/example/stub/cas/repo|github.com/example/stub/cas' \
    'github.com/example/stub/internal/web|github.com/example/stub/cas'
  # Touching the marker file adds a package, which is how the test changes the
  # graph without touching any script.
  if [[ -n "${STUB_EXTRA_MARKER:-}" && -f "${STUB_EXTRA_MARKER}" ]]; then
    printf '%s\n' 'github.com/example/stub/extra|github.com/example/stub/cas'
  fi
  exit 0
fi
echo "stub go: unexpected invocation: $*" >&2
exit 1
STUB
chmod +x "$work/bin/go"
export PATH="$work/bin:$PATH"
export STUB_EXTRA_MARKER="$work/extra-enabled"

# --- fixture ---------------------------------------------------------------

fixture() { # fixture <name> -> prints the repository path
  local dir="$work/$1"
  mkdir -p "$dir/scripts" "$dir/docs/design"
  cp "$script_dir/dep-graph.sh" "$dir/scripts/"
  chmod +x "$dir/scripts/dep-graph.sh"
  git -C "$dir" init -q -b main
  printf '%s' "$dir"
}

# Run the fixture script from the fixture root; usage: run_fixture <repo> <log> [args...]
run_fixture() {
  local repo="$1" log="$2"
  shift 2
  ( cd "$repo" && bash ./scripts/dep-graph.sh "$@" ) > "$log" 2>&1
}

doc_of() { printf '%s/docs/design/package-graph.md' "$1"; }

# --- 1. a fresh run creates the document at v1 -----------------------------

repo="$(fixture case-fresh)"
doc="$(doc_of "$repo")"
expect_absent "no document exists before the first run" "$doc"
status=0
run_fixture "$repo" "$work/case-fresh.log" || status=$?
expect_exit "fresh generation succeeds" 0 "$status"
expect_file "fresh generation writes the owned document" "$doc"
expect_grep "fresh generation reports what it wrote" "wrote docs/design/package-graph.md" "$work/case-fresh.log"
expect_grep "a new document starts at frontmatter version v1" "^version: v1$" "$doc"
expect_grep "the document declares its generator" "^generated: scripts/dep-graph.sh$" "$doc"
expect_grep "the graph draws the core package" '^    cas\["cas"\]$' "$doc"
expect_grep "the graph draws an edge to the core" '^  cas_backend_fs --> cas$' "$doc"
expect_grep "the graph groups the byte layer" 'subgraph BYTE\[' "$doc"
expect_grep "the graph groups the helper layer" 'subgraph HELP\[' "$doc"
expect_grep "the graph groups internal packages" 'subgraph INTERNAL\[' "$doc"
# verify.sh's doc-integrity rule: every ```mermaid opener needs a closer. The
# document also fences the shell snippet, so closers are counted, not paired.
mermaid_openers="$(grep -c '^```mermaid$' "$doc" || true)"
fence_closers="$(grep -c '^```$' "$doc" || true)"
if [[ "$mermaid_openers" -ge 1 && "$fence_closers" -ge "$mermaid_openers" ]]; then
  pass "the generated document has balanced mermaid fences"
else
  fail "the generated document has balanced mermaid fences (mermaid $mermaid_openers, closers $fence_closers)"
fi

# --- 2. an unchanged regeneration is a byte-for-byte no-op -----------------

before_doc="$work/case-noop.doc"
before_tree="$(tree_hash "$repo")"
cp "$doc" "$before_doc"
status=0
run_fixture "$repo" "$work/case-noop.log" || status=$?
expect_exit "regenerating an unchanged graph succeeds" 0 "$status"
expect_grep "an unchanged regeneration says it wrote nothing" "nothing written" "$work/case-noop.log"
if cmp -s "$before_doc" "$doc"; then
  pass "an unchanged regeneration leaves the document byte-identical"
else
  fail "an unchanged regeneration leaves the document byte-identical"
fi
expect_eq "an unchanged regeneration does not bump the version" v1 "$(sed -n 's/^version: //p' "$doc")"

# --- 3. --check is clean when the document matches the tree ---------------

status=0
run_fixture "$repo" "$work/case-check-clean.log" --check || status=$?
expect_exit "--check exits 0 when the document matches" 0 "$status"
expect_grep "--check reports the document as current" "is up to date" "$work/case-check-clean.log"
expect_eq "--check leaves the whole tree untouched" "$before_tree" "$(tree_hash "$repo")"

# --- 4. a changed graph regenerates and moves the version -----------------

touch "$STUB_EXTRA_MARKER"
status=0
run_fixture "$repo" "$work/case-changed.log" || status=$?
expect_exit "regenerating after a graph change succeeds" 0 "$status"
expect_eq "a changed graph moves the version by one" v2 "$(sed -n 's/^version: //p' "$doc")"
expect_grep "a changed graph draws the new package" '^    extra\["extra"\]$' "$doc"
status=0
run_fixture "$repo" "$work/case-changed-check.log" --check || status=$?
expect_exit "--check exits 0 once the changed graph is regenerated" 0 "$status"

# --- 5. --check refuses a stale document without writing it ---------------

printf '\nhand-edited drift\n' >>"$doc"
stale_doc="$work/case-stale.doc"
stale_tree="$(tree_hash "$repo")"
cp "$doc" "$stale_doc"
status=0
run_fixture "$repo" "$work/case-stale.log" --check || status=$?
expect_exit "--check exits 1 when the document is stale" 1 "$status"
expect_grep "--check says the document is stale" "is stale" "$work/case-stale.log"
expect_grep "--check tells the operator how to fix it" "regenerate it with: ./scripts/dep-graph.sh" "$work/case-stale.log"
if cmp -s "$stale_doc" "$doc"; then
  pass "--check leaves a stale document byte-identical"
else
  fail "--check leaves a stale document byte-identical"
fi
expect_eq "--check writes nothing anywhere in the tree" "$stale_tree" "$(tree_hash "$repo")"

# --- 6. --check is stale when the graph moved but the document did not ----

rm -f "$STUB_EXTRA_MARKER"
status=0
run_fixture "$repo" "$work/case-graph-moved.log" --check || status=$?
expect_exit "--check exits 1 when the graph no longer matches" 1 "$status"
expect_eq "--check still wrote nothing after a graph change" "$stale_tree" "$(tree_hash "$repo")"

# --- 7. --check never creates a missing document --------------------------

repo_missing="$(fixture case-missing)"
expect_absent "the second fixture starts without a document" "$(doc_of "$repo_missing")"
status=0
run_fixture "$repo_missing" "$work/case-missing.log" --check || status=$?
expect_exit "--check exits 1 when the document is missing" 1 "$status"
expect_absent "--check does not create the missing document" "$(doc_of "$repo_missing")"

# --- 8. usage handling ----------------------------------------------------

repo_usage="$(fixture case-usage)"
status=0
run_fixture "$repo_usage" "$work/case-unknown.log" --nope || status=$?
expect_exit "an unknown option exits 2" 2 "$status"
expect_absent "an unknown option writes no document" "$(doc_of "$repo_usage")"
status=0
run_fixture "$repo_usage" "$work/case-two.log" --check --check || status=$?
expect_exit "two options are rejected" 2 "$status"
status=0
run_fixture "$repo_usage" "$work/case-help.log" --help || status=$?
expect_exit "--help exits 0" 0 "$status"
expect_grep "--help names the owned document" "docs/design/package-graph.md" "$work/case-help.log"

if [[ "$failed" -ne 0 ]]; then
  echo "dependency-graph script tests failed" >&2
  exit 1
fi

echo "dependency-graph script tests passed"
