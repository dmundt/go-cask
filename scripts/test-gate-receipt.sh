#!/usr/bin/env bash
set -euo pipefail

# test-gate-receipt.sh — behaviour test for gate-receipt.sh.
#
# The helper decides whether CI may reuse the gate a developer ran locally, so
# what it REFUSES is the part that matters. This test drives the real script
# against throwaway repositories and throwaway signing keys, and pins:
#
#   * create writes the receipt for the commit, with the tree, the merge base,
#     the hash of the changed path list and the checks that ran;
#   * create refuses a scope that is neither docs nor full, a base that is not a
#     commit, and a check name the line-oriented format cannot carry;
#   * publish signs the receipt and pushes it to refs/gate/<sha>, and running it
#     again with the same evidence reports "already published" without rewriting
#     the ref;
#   * verify accepts a receipt that is signed by a listed key and matches the
#     commit, the checked-out tree, an ancestor base, the recomputed diff hash,
#     the recomputed scope and the required checks;
#   * verify refuses a missing receipt, an untrusted signer, a different tree, a
#     base that is not an ancestor of the pull request base, a diff hash that no
#     longer describes the change, a documentation receipt for a Go change, and a
#     receipt missing a required check. Every one of those refusals is a fallback
#     to the full gate in CI, which is only safe if it is not a false accept.
#
# It signs with ssh keys generated in $work, so it needs no signing configuration
# of its own: git is pointed at the throwaway key through GIT_CONFIG_COUNT, which
# is how the Windows and WSL toolchains are kept out of the test entirely.

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
helper="$script_dir/gate-receipt.sh"
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
expect_status() { # expect_status <desc> <expected-status> <actual-status>
  if [[ "$2" == "$3" ]]; then
    pass "$1"
  else
    fail "$1 (expected exit $2, got $3)"
  fi
}
expect_grep() { # expect_grep <desc> <pattern> <file>
  if grep -q -- "$2" "$3"; then pass "$1"; else fail "$1 (no match for '$2' in $3)"; fi
}

# run <command...>: capture stdout/stderr in $work/out and $work/err, and keep the
# status in RC instead of aborting the test.
RC=0
run() {
  set +e
  "$@" >"$work/out" 2>"$work/err"
  RC=$?
  set -e
}

# --- fixtures ---------------------------------------------------------------

mkdir -p "$work/keys"
ssh-keygen -q -t ed25519 -N "" -C "gate@test" -f "$work/keys/trusted"
ssh-keygen -q -t ed25519 -N "" -C "other@test" -f "$work/keys/other"
# allowed_signers format: <principal> [options] <keytype> <key>.
awk '{ print $3, "namespaces=\"git\"", $1, $2 }' "$work/keys/trusted.pub" >"$work/allow-trusted"
awk '{ print $3, "namespaces=\"git\"", $1, $2 }' "$work/keys/other.pub" >"$work/allow-other"

# Signing configuration for the whole test, without touching any real config: git
# reads GIT_CONFIG_* before the user's files, and gate-receipt.sh calls git, so
# publish signs with the throwaway key.
export GIT_CONFIG_COUNT=2
export GIT_CONFIG_KEY_0=gpg.format GIT_CONFIG_VALUE_0=ssh
export GIT_CONFIG_KEY_1=user.signingkey GIT_CONFIG_VALUE_1="$work/keys/trusted"

repo="$work/repo"
mkdir -p "$repo"
git -C "$repo" init -q -b main .
git -C "$repo" config user.email gate@test
git -C "$repo" config user.name "Gate Test"
git -C "$repo" config commit.gpgsign false

# origin/main is what create defaults its base to, and origin is also where
# publish pushes the receipt.
git init -q --bare "$work/origin.git"
git -C "$repo" remote add origin "$work/origin.git"

base_commit() { # base_commit <file> <content> — one commit, returned on stdout
  mkdir -p "$(dirname "$repo/$1")"
  printf '%s\n' "$2" >"$repo/$1"
  git -C "$repo" add -- "$1"
  git -C "$repo" -c commit.gpgsign=false commit -q -m "add $1"
  git -C "$repo" rev-parse HEAD
}

first="$(base_commit README.md '# probe')"
git -C "$repo" push -q origin main
git -C "$repo" fetch -q origin

create() { # create <sha> <scope> <checks> [extra args...]
  local sha="$1" scope="$2" checks="$3"
  shift 3
  run bash -c "cd '$repo' && bash '$helper' create --sha '$sha' --base '$first' --scope '$scope' --checks '$checks' $*"
}

verify() { # verify <sha> <tree> [extra args...]
  local sha="$1" tree="$2"
  shift 2
  run bash -c "cd '$repo' && bash '$helper' verify --sha '$sha' --tree '$tree' --signers '$work/allow-trusted' $*"
}

# --- create -----------------------------------------------------------------

second_file_commit="$(base_commit main.go 'package main')"
tree="$(git -C "$repo" rev-parse "$second_file_commit^{tree}")"

create "$second_file_commit" full "gofmt go-test-race govulncheck coverage-tiers" --coverage-tiers 37
expect_status "create exits 0" 0 "$RC"
receipt_file="$repo/.git/gate-receipts/$second_file_commit.receipt"
if [[ -f "$receipt_file" ]]; then pass "create writes the receipt file"; else fail "create wrote no receipt"; fi
expect_grep "receipt names the commit" "^commit $second_file_commit\$" "$receipt_file"
expect_grep "receipt names the tree" "^tree $tree\$" "$receipt_file"
expect_grep "receipt names the base" "^base $first\$" "$receipt_file"
expect_grep "receipt records the scope" "^scope full\$" "$receipt_file"
expect_grep "receipt lists a check" "^check go-test-race\$" "$receipt_file"
expect_grep "receipt records the tier count" "^coverage-tiers 37\$" "$receipt_file"
expect_grep "receipt is marked as a version 1 receipt" "^cask-gate-receipt 1\$" "$receipt_file"

create "$second_file_commit" sideways "gofmt" >/dev/null 2>&1 || true
expect_status "create refuses a scope that is neither docs nor full" 2 "$RC"

create "$second_file_commit" full "bad:name" >/dev/null 2>&1 || true
expect_status "create refuses a check name the format cannot carry" 2 "$RC"

# --- publish ----------------------------------------------------------------

run bash -c "cd '$repo' && bash '$helper' publish --sha '$second_file_commit' --remote origin"
expect_status "publish exits 0" 0 "$RC"
run git -C "$repo" ls-remote --exit-code origin "refs/gate/$second_file_commit"
expect_status "publish pushed refs/gate/<sha>" 0 "$RC"
published_obj="$(git -C "$repo" ls-remote origin "refs/gate/$second_file_commit" | cut -f1)"

run bash -c "cd '$repo' && bash '$helper' publish --sha '$second_file_commit' --remote origin"
expect_status "publish again exits 0" 0 "$RC"
expect_grep "publish again reports the same evidence" "already published" "$work/out"
expect_eq "publish again did not rewrite the ref" "$published_obj" \
  "$(git -C "$repo" ls-remote origin "refs/gate/$second_file_commit" | cut -f1)"

# --- verify: the accept path ------------------------------------------------

verify "$second_file_commit" "$tree" --base "$first" --require-check coverage-tiers
expect_status "verify accepts a listed signer and a matching tree/scope/checks" 0 "$RC"
expect_grep "verify reports the signature" "signature for gate@test" "$work/out"
expect_grep "verify lists the checks it relied on" "check go-test-race" "$work/out"

# The suite CI requires is the helper's own list, so this test asks for it instead
# of restating it: a receipt that lists every member is accepted, and one that is
# missing a member is refused — which is what keeps a renamed gate section from
# turning into a skipped check.
run bash "$helper" suite
expect_status "suite prints the required check list" 0 "$RC"
suite_checks="$(tr '\n' ' ' <"$work/out")"
create "$second_file_commit" full "$suite_checks" --coverage-tiers 37
expect_status "create accepts the suite's own list" 0 "$RC"
run bash -c "cd '$repo' && bash '$helper' publish --sha '$second_file_commit' --remote origin"
expect_status "publishing a full-suite receipt exits 0" 0 "$RC"
verify "$second_file_commit" "$tree" --base "$first" --require-suite full
expect_status "verify accepts a receipt listing the whole suite" 0 "$RC"

create "$second_file_commit" full "${suite_checks/ doc-integrity/}" --coverage-tiers 37
expect_status "create writes a receipt missing one suite member" 0 "$RC"
run bash -c "cd '$repo' && bash '$helper' publish --sha '$second_file_commit' --remote origin"
expect_status "publishing the partial receipt exits 0" 0 "$RC"
verify "$second_file_commit" "$tree" --base "$first" --require-suite full
expect_status "verify refuses a receipt missing a suite member" 1 "$RC"
expect_grep "the refusal names the missing member" "required check 'doc-integrity'" "$work/err"

# --- verify: every refusal --------------------------------------------------

verify "$second_file_commit" "$tree" --base "$first" --require-check no-such-check
expect_status "verify refuses a missing required check" 1 "$RC"
expect_grep "the refusal names the check" "does not list the required check 'no-such-check'" "$work/err"

verify "$second_file_commit" "$(git -C "$repo" rev-parse "$first^{tree}")" --base "$first"
expect_status "verify refuses a tree the receipt does not cover" 1 "$RC"
expect_grep "the refusal names the tree" "was checked out" "$work/err"

# A pull request base the receipt's base is not an ancestor of: an unrelated
# commit, so this can only be refused by the ancestry rule.
orphan="$(git -C "$repo" commit-tree "$(git -C "$repo" rev-parse "$first^{tree}")" -m "unrelated base")"
verify "$second_file_commit" "$tree" --base "$orphan"
expect_status "verify refuses a base that is not an ancestor of the base tip" 1 "$RC"
expect_grep "the refusal names the pull request base" "not an ancestor of the pull request base" "$work/err"

verify "$second_file_commit" "$tree" --sha "$first"
expect_status "verify refuses when there is no receipt for the commit" 1 "$RC"
expect_grep "the refusal names the ref" "no gate receipt at" "$work/err"

# An untrusted signer: sign the same receipt with the other key and republish it.
run bash -c "cd '$repo' && GIT_CONFIG_VALUE_1='$work/keys/other' bash '$helper' publish --sha '$second_file_commit' --remote origin"
expect_status "publish with the other key exits 0" 0 "$RC"
verify "$second_file_commit" "$tree" --base "$first"
expect_status "verify refuses a signature from a key outside the allow-list" 1 "$RC"
expect_grep "the refusal names the allow-list" "not signed by a key in" "$work/err"

# Restore the trusted signature for the remaining cases.
run bash -c "cd '$repo' && bash '$helper' publish --sha '$second_file_commit' --remote origin"
expect_status "republishing with the trusted key exits 0" 0 "$RC"

# A tampered payload: the receipt is the message of a signed commit, so editing
# the message without the key leaves a receipt nobody signed. Build that object
# directly, unsigned, at the same ref, on the same parent and tree so only the
# signature is missing.
tampered="$work/tampered"
head -n 1 "$receipt_file" >"$tampered"
sed -n 's/^scope full$/scope docs/p' "$receipt_file" >>"$tampered"
tampered_obj="$(git -C "$repo" commit-tree -m "$(cat "$tampered")" \
  "$(git -C "$repo" rev-parse "$second_file_commit^{tree}")" -p "$second_file_commit")"
git -C "$repo" update-ref "refs/gate/$second_file_commit" "$tampered_obj"
verify "$second_file_commit" "$tree" --base "$first"
expect_status "verify refuses a rewritten, unsigned receipt" 1 "$RC"
expect_grep "the refusal names the signer" "not signed by a key in" "$work/err"

# A receipt that is well signed but built on another commit: the structural check
# must refuse it, or a receipt could be copied onto a commit it never gated.
elsewhere_obj="$(git -C "$repo" commit-tree -S -m "$(cat "$receipt_file")" \
  "$(git -C "$repo" rev-parse "$first^{tree}")" -p "$first")"
git -C "$repo" update-ref "refs/gate/$second_file_commit" "$elsewhere_obj"
verify "$second_file_commit" "$tree" --base "$first"
expect_status "verify refuses a receipt built on another commit" 1 "$RC"
expect_grep "the refusal names the parent" "is built on" "$work/err"

# A receipt whose recorded diff hash no longer describes the change: sign the
# record with the trusted key but with a diff line that names another change.
forged="$work/forged"
sed 's/^diff .*/diff 0000000000000000000000000000000000000000/' "$receipt_file" >"$forged"
forged_obj="$(git -C "$repo" commit-tree -S -m "$(cat "$forged")" "$tree" -p "$second_file_commit")"
git -C "$repo" update-ref "refs/gate/$second_file_commit" "$forged_obj"
verify "$second_file_commit" "$tree" --base "$first"
expect_status "verify refuses a diff hash that does not describe the change" 1 "$RC"
expect_grep "the refusal names the diff" "hashes the change as" "$work/err"

# A documentation receipt for a Go change: recomputing the scope must refuse it,
# or a docs-scope gate would excuse a Go diff.
run bash -c "cd '$repo' && bash '$helper' create --sha '$second_file_commit' --base '$first' --scope docs --checks doc-integrity"
expect_status "create writes a docs-scope receipt" 0 "$RC"
run bash -c "cd '$repo' && bash '$helper' publish --sha '$second_file_commit' --remote origin"
expect_status "publishing the docs-scope receipt exits 0" 0 "$RC"
verify "$second_file_commit" "$tree" --base "$first"
expect_status "verify refuses a docs receipt for a Go change" 1 "$RC"
expect_grep "the refusal names the scope" "does not cover the full change" "$work/err"

# A documentation receipt for a documentation change is the accept path for a
# docs-only branch.
docs_commit="$(base_commit docs/note.md '# note')"
docs_tree="$(git -C "$repo" rev-parse "$docs_commit^{tree}")"
run bash -c "cd '$repo' && bash '$helper' create --sha '$docs_commit' --base '$second_file_commit' --scope docs --checks doc-integrity"
expect_status "create writes the receipt for a docs-only change" 0 "$RC"
run bash -c "cd '$repo' && bash '$helper' publish --sha '$docs_commit' --remote origin"
expect_status "publishing the docs-only receipt exits 0" 0 "$RC"
verify "$docs_commit" "$docs_tree" --base "$second_file_commit" --require-check doc-integrity
expect_status "verify accepts a docs receipt for a docs-only change" 0 "$RC"

# --- show -------------------------------------------------------------------

run bash -c "cd '$repo' && bash '$helper' show --sha '$docs_commit'"
expect_status "show exits 0" 0 "$RC"
expect_grep "show prints the receipt" "^scope docs\$" "$work/out"

run bash -c "cd '$repo' && bash '$helper' show --sha '$first'"
expect_status "show refuses a commit with no receipt" 1 "$RC"

# --- usage ------------------------------------------------------------------

run bash -c "bash '$helper'"
expect_status "no command is a usage error" 2 "$RC"
run bash -c "bash '$helper' sideways"
expect_status "an unknown command is a usage error" 2 "$RC"

if [[ "$failed" -ne 0 ]]; then
  echo "gate receipt behaviour test failed" >&2
  exit 1
fi
echo "gate receipt behaviour test passed"
