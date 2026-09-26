#!/usr/bin/env bash
# The gate receipt: the evidence a green local gate produced, in the one place CI
# can read it.
#
# scripts/verify.sh runs the whole gate under WSL for every commit, and
# .githooks/pre-push refuses a push whose HEAD holds no green stamp for that exact
# commit — but the stamp lives in the local shared git dir ($common/verify.ok), so
# GitHub cannot see it and re-runs the same suite from scratch. The receipt is that
# stamp made portable: a line-oriented record of WHAT was gated (commit, tree, the
# merge base it was measured from, a hash of the changed path list, the scope), by
# WHAT (the checks that ran, the toolchain, the time) and signed by WHOM.
#
# The transport is a coordination ref, like `refs/lane/<issue>` in pr-lane.sh:
# `refs/gate/<commit>` points at a receipt COMMIT — a signed commit object whose
# message is the receipt, whose tree is the gated commit's tree, and whose parent is
# the gated commit itself. Nothing is ever committed to it in the ordinary sense:
# the object is built with `git commit-tree`, it hangs off the head commit, it
# touches no branch, and deleting the ref deletes the record.
#
# It is a commit and not an annotated tag for a mechanical reason. The remote
# refuses a tag object under this namespace ("fatal error in commit_refs"), and the
# REST API route pr-lane.sh uses (`POST /git/tags`) builds its object server-side,
# so it cannot carry the developer's signature at all. A signed commit is
# pushable, verifiable with `git verify-commit`, and its shape gives CI two checks
# that do not depend on the receipt's own text: its parent must BE the commit CI is
# testing, and its tree must BE the tree CI checked out.
#
#   gate-receipt.sh create  --scope docs|full [--sha SHA] [--base SHA]
#                           [--checks "a b c"] [--coverage-tiers N]
#       Write the receipt for a green run into $common/gate-receipts/<sha>.receipt.
#       verify.sh calls this; it signs nothing and touches no network.
#   gate-receipt.sh publish [--sha SHA] [--remote NAME] [--quiet]
#       Sign the receipt as a commit object and push it to refs/gate/<sha>.
#       .githooks/pre-push runs this as part of a push, best effort; run it by hand
#       to republish.
#   gate-receipt.sh verify --sha SHA --tree TREE [--base SHA]
#                           [--require-check NAME]... [--signers FILE]
#       CI side. Exit 0 only when the receipt is a signed commit whose parent is
#       the commit CI is asking about, whose tree is the tree CI checked out, whose
#       signature a trusted key made, whose base is an ancestor of that commit and
#       of the pull request base, whose diff hash recomputed in CI matches, whose
#       scope covers the change, and which lists every required check. Anything
#       less exits 1, and the caller runs the whole gate instead — the fast path is
#       entered by evidence, never by absence of it.
#   gate-receipt.sh show [--sha SHA]
#       Print the receipt for a commit: the local file if this clone has one, else
#       the published receipt.
#   gate-receipt.sh suite
#       Print the checks a full-scope receipt must list for CI to skip its gate,
#       one per line. This is the list `verify --require-suite full` enforces.
#
# Exit codes: 0 success, 1 refused (no usable receipt, or publish failed),
# 2 usage error.
#
# Two properties are load-bearing and pinned by scripts/test-gate-receipt.sh:
# `verify` trusts only a signature from the pinned allow-list (the push authority
# that made the ref is not the trust decision the file records), and the receipt
# must be BUILT ON the commit it excuses and cover tree CI checked out — so a branch
# that fell behind main, or a receipt copied from another commit, falls back to the
# full gate rather than passing on a tree nobody gated.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
ref_prefix="refs/gate"

# The checks the required `verify` job in CI would have run itself. Keeping the
# list here, next to the format it has to match, means ci.yml names only
# `--require-suite full` and a section renamed in verify.sh cannot silently turn
# into a skipped check: the receipt stops carrying the name, and CI runs the whole
# gate instead. A section added to verify.sh must be added here and marked there
# (`mark_check`); a section that is merely renamed must be renamed in both.
suite_full=(
  gofmt
  go-mod-tidy
  go-build
  module-graph
  go-vet
  cross-platform
  layer-matrix
  gitlike-codec-guard
  pack-codec-guard
  go-test-race
  coverage-tiers
  fuzz-smoke
  helper-scripts
  version-fields
  doc-integrity
  package-graph
  website-footer
  website-examples
)
# NOT in the list, deliberately: `govulncheck`. CI owns that scan in its own
# required `security` job, so the `verify` job must not be excused by a receipt
# that merely happens to list it — and a local gate may legitimately have run with
# VERIFY_SKIP_SECURITY=true.

usage() {
  cat >&2 <<'EOF'
usage: gate-receipt.sh <command>

  create  --scope docs|full [--sha SHA] [--base SHA] [--checks "a b c"]
          [--coverage-tiers N]        write the receipt for a green gate run
  publish [--sha SHA] [--remote NAME] [--quiet]
                                      sign it and push refs/gate/<sha>
  verify  --sha SHA --tree TREE [--base SHA] [--require-check NAME]...
          [--require-suite full] [--signers FILE]
                                      accept the receipt, or exit 1
  show    [--sha SHA]                 print the receipt for a commit
  suite                               print the checks a full receipt must list
EOF
  exit 2
}

die() { echo "gate-receipt: $*" >&2; exit 2; }
refuse() { echo "gate-receipt: $*" >&2; exit 1; }

git rev-parse --show-toplevel >/dev/null 2>&1 ||
  die "not inside a git repository"

common="$(git rev-parse --path-format=absolute --git-common-dir)"
receipt_dir="$common/gate-receipts"

# The allow-list is a repo file, not a runner setting: an SSH signature has to be
# checked against keys this repository decided to trust, or `verify` would accept
# whatever key the runner happened to know. GATE_RECEIPT_SIGNERS overrides it for
# the tests, which sign with throwaway keys.
signers="${GATE_RECEIPT_SIGNERS:-$script_dir/../.github/gate-signers}"

# The identity of a change: the sorted set of paths it touches, hashed with git's
# own object hash so the helper needs no coreutils beyond what git already
# provides, and both sides of the comparison hash the same way.
path_list_hash() { # path_list_hash <base> <sha>
  git diff --name-only "$1" "$2" | LC_ALL=C sort | git hash-object --stdin
}

# The receipt body of a receipt commit: everything after the header block. The
# signature rides in the `gpgsig` header, so unlike a signed tag there is no
# signature block at the end of the message to strip.
receipt_body() { # receipt_body <ref-or-object>
  git cat-file commit "$1" | sed '1,/^$/d'
}

payload_of() { # payload_of <sha>  — the local receipt file, or nothing
  local file="$receipt_dir/$1.receipt"
  [[ -f "$file" ]] && cat "$file"
}

field() { # field <payload> <name>
  printf '%s\n' "$1" | sed -n "s/^$2 //p" | head -n 1
}

# The lines that say WHAT the receipt covers, without the run time: two receipts
# for one commit that agree here are the same evidence, however often the gate ran.
receipt_identity() {
  printf '%s\n' "$1" |
    grep -E '^(commit|tree|base|diff|scope|check|coverage-tiers) ' || true
}

cmd_create() {
  local scope="" sha="" base="" checks="${GATE_RECEIPT_CHECKS:-}" tiers=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
    --scope) scope="${2:?--scope needs a value}"; shift 2 ;;
    --sha) sha="${2:?--sha needs a value}"; shift 2 ;;
    --base) base="${2:?--base needs a value}"; shift 2 ;;
    --checks) checks="${2:-}"; shift 2 ;;
    --coverage-tiers) tiers="${2:?--coverage-tiers needs a value}"; shift 2 ;;
    *) die "create: unknown option '$1'" ;;
    esac
  done
  case "$scope" in
  docs | full) ;;
  *) die "create: --scope must be docs or full (got '${scope:-}')" ;;
  esac

  [[ -n "$sha" ]] || sha="$(git rev-parse HEAD)"
  sha="$(git rev-parse --verify "$sha^{commit}")" ||
    die "create: not a commit: $sha"
  # verify.sh passes the merge base it measured the change from, because that is
  # the base the scope decision used; a caller without one gets origin/main.
  if [[ -z "$base" ]]; then
    base="$(git merge-base origin/main "$sha" 2>/dev/null || true)"
    [[ -n "$base" ]] ||
      die "create: no merge base with origin/main; pass --base"
  fi
  base="$(git rev-parse --verify "$base^{commit}")" ||
    die "create: base is not a commit: $base"

  # A check name is a token in a line-oriented format, so it may not contain a
  # space or a newline; anything else would make the receipt unparseable rather
  # than merely wrong.
  local check
  for check in $checks; do
    case "$check" in
    *[!A-Za-z0-9._-]*) die "create: check name '$check' has invalid characters" ;;
    esac
  done

  local tree diff go_version runner run out tmp
  tree="$(git rev-parse "$sha^{tree}")"
  diff="$(path_list_hash "$base" "$sha")"
  go_version="$(go version 2>/dev/null | awk '{ print $3 }' || true)"
  [[ -n "$go_version" ]] || go_version=unknown
  runner="$(uname -s | tr '[:upper:]' '[:lower:]')/$(uname -m)"
  run="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

  mkdir -p "$receipt_dir"
  out="$receipt_dir/$sha.receipt"
  tmp="$(mktemp "$receipt_dir/.$sha.XXXXXX")"
  {
    printf 'cask-gate-receipt 1\n'
    printf 'commit %s\n' "$sha"
    printf 'tree %s\n' "$tree"
    printf 'base %s\n' "$base"
    printf 'diff %s\n' "$diff"
    printf 'scope %s\n' "$scope"
    for check in $checks; do printf 'check %s\n' "$check"; done
    [[ -z "$tiers" ]] || printf 'coverage-tiers %s\n' "$tiers"
    printf 'go %s\n' "$go_version"
    printf 'runner %s\n' "$runner"
    printf 'run %s\n' "$run"
  } >"$tmp"
  # Atomic, like the stamp: a receipt half written by an interrupted run must
  # never be the file a publish picks up.
  mv "$tmp" "$out"
  echo "$out"
}

cmd_publish() {
  local sha="" remote="origin" quiet=false
  while [[ $# -gt 0 ]]; do
    case "$1" in
    --sha) sha="${2:?--sha needs a value}"; shift 2 ;;
    --remote) remote="${2:?--remote needs a value}"; shift 2 ;;
    --quiet) quiet=true; shift ;;
    *) die "publish: unknown option '$1'" ;;
    esac
  done

  [[ -n "$sha" ]] || sha="$(git rev-parse HEAD)"
  sha="$(git rev-parse --verify "$sha^{commit}")" ||
    die "publish: not a commit: $sha"

  local payload_file="$receipt_dir/$sha.receipt"
  [[ -f "$payload_file" ]] ||
    refuse "no local gate receipt for $sha — run ./scripts/verify.sh, then publish"
  local payload
  payload="$(cat "$payload_file")"
  [[ "$(field "$payload" scope)" == "docs" || "$(field "$payload" scope)" == "full" ]] ||
    refuse "the receipt for $sha is malformed; run ./scripts/verify.sh again"

  # Sign through git, so the key, its format and the principal are whatever this
  # toolchain is configured to sign with — the same identity that signs the commits.
  # A toolchain without signing configured (the WSL git next to this host's Windows
  # signing key is the case here) cannot publish, and that is not an error: CI falls
  # back to the full gate, which is the safe direction.
  #
  # The object is a commit: parent = the gated commit, tree = its tree, message =
  # the receipt. Both structural fields are what CI checks, so a receipt that is not
  # built on the commit it excuses is refused however well it is signed.
  local obj ref signing_errors tree
  ref="$ref_prefix/$sha"
  tree="$(git rev-parse "$sha^{tree}")"
  [[ "$(git config --get gpg.format || true)" == "ssh" ]] ||
    refuse "gate receipts are signed with an SSH key; set 'git config gpg.format ssh' and user.signingkey, or publish from the toolchain that has them"
  signing_errors="$(mktemp)"
  if ! obj="$(git commit-tree -S -p "$sha" -m "$payload" "$tree" 2>"$signing_errors")"; then
    cat "$signing_errors" >&2
    rm -f "$signing_errors"
    refuse "cannot sign in this toolchain (git commit-tree -S failed); configure gpg.format, user.signingkey, user.name and user.email"
  fi
  rm -f "$signing_errors"

  git update-ref "$ref" "$obj"

  local remote_obj="" fetched=""
  remote_obj="$(git ls-remote --exit-code "$remote" "$ref" 2>/dev/null | head -n 1 | cut -f1 || true)"
  if [[ "$remote_obj" == "$obj" ]]; then
    $quiet || echo "gate-receipt: already published: $ref"
    return 0
  fi
  if [[ -n "$remote_obj" ]]; then
    # A newer run of the gate for the same commit produces different bytes (the
    # run time) but the same evidence. Replace the ref only when the evidence
    # itself moved, and say so when it happens: silently rewriting what CI reads
    # is the one thing this ref must never do.
    git fetch --no-tags --quiet "$remote" "$ref" 2>/dev/null || true
    fetched="$(git rev-parse --verify FETCH_HEAD 2>/dev/null || true)"
    if [[ -n "$fetched" ]] &&
      diff -q <(receipt_identity "$(receipt_body "$fetched")") \
        <(receipt_identity "$payload") >/dev/null; then
      $quiet || echo "gate-receipt: already published (same evidence): $ref"
      return 0
    fi
    git push --no-verify --quiet --force-with-lease="$ref:$remote_obj" \
      "$remote" "$ref:$ref" ||
      refuse "could not replace $ref on $remote"
    $quiet || echo "gate-receipt: replaced the receipt at $ref with this run"
    return 0
  fi
  git push --no-verify --quiet "$remote" "$ref:$ref" ||
    refuse "could not push $ref to $remote"
  $quiet || echo "gate-receipt: published $ref"
}

cmd_verify() {
  local sha="" tree="" tip="" signers_file="$signers" ref=""
  local require=()
  while [[ $# -gt 0 ]]; do
    case "$1" in
    --sha) sha="${2:?--sha needs a value}"; shift 2 ;;
    --tree) tree="${2:?--tree needs a value}"; shift 2 ;;
    --base) tip="${2:?--base needs a value}"; shift 2 ;;
    --ref) ref="${2:?--ref needs a value}"; shift 2 ;;
    --signers) signers_file="${2:?--signers needs a value}"; shift 2 ;;
    --require-check) require+=("${2:?--require-check needs a value}"); shift 2 ;;
    --require-suite)
      case "${2:?--require-suite needs a value}" in
      full) require+=("${suite_full[@]}") ;;
      *) die "verify: --require-suite knows only 'full'" ;;
      esac
      shift 2
      ;;
    *) die "verify: unknown option '$1'" ;;
    esac
  done
  [[ -n "$sha" ]] || die "verify: --sha is required"
  [[ -n "$tree" ]] || die "verify: --tree is required (the tree that was checked out)"
  [[ -n "$ref" ]] || ref="$ref_prefix/$sha"

  git rev-parse --verify --quiet "$ref" >/dev/null ||
    refuse "no gate receipt at $ref"
  [[ "$(git cat-file -t "$ref")" == "commit" ]] ||
    refuse "$ref is not a receipt commit"

  # Shape before text: the receipt commit must hang off the commit CI is asking
  # about and carry the tree CI has checked out. Both are structural, so neither
  # depends on what the receipt claims about itself.
  local parent built_tree
  parent="$(git rev-parse --verify --quiet "$ref^" || true)"
  [[ "$parent" == "$sha" ]] ||
    refuse "the receipt at $ref is built on ${parent:-nothing}, not on commit $sha"
  built_tree="$(git rev-parse "$ref^{tree}")"
  [[ "$built_tree" == "$tree" ]] ||
    refuse "the receipt at $ref carries tree $built_tree, but $tree was checked out"

  # The signature is what makes the receipt evidence rather than a claim, and the
  # allow-list is what makes the signature mean a key this repository trusts.
  [[ -f "$signers_file" ]] ||
    refuse "signer allow-list not found: $signers_file"
  local signature
  if ! signature="$(git -c gpg.format=ssh \
    -c "gpg.ssh.allowedSignersFile=$signers_file" verify-commit "$ref" 2>&1)"; then
    printf '%s\n' "$signature" >&2
    refuse "the receipt at $ref is not signed by a key in $signers_file"
  fi

  local payload
  payload="$(receipt_body "$ref")"
  [[ "$(printf '%s\n' "$payload" | head -n 1)" == "cask-gate-receipt 1" ]] ||
    refuse "the receipt at $ref is not a cask-gate-receipt version 1"

  local got_commit got_tree got_base got_diff got_scope
  got_commit="$(field "$payload" commit)"
  got_tree="$(field "$payload" tree)"
  got_base="$(field "$payload" base)"
  got_diff="$(field "$payload" diff)"
  got_scope="$(field "$payload" scope)"

  [[ "$got_commit" == "$sha" ]] ||
    refuse "the receipt names commit $got_commit, not $sha"
  [[ "$got_tree" == "$tree" ]] ||
    refuse "the receipt covers tree $got_tree, but $tree was checked out"
  [[ -n "$got_base" ]] || refuse "the receipt names no base"
  git merge-base --is-ancestor "$got_base" "$sha" ||
    refuse "the receipt base $got_base is not an ancestor of $sha"
  if [[ -n "$tip" ]]; then
    git merge-base --is-ancestor "$got_base" "$tip" ||
      refuse "the receipt base $got_base is not an ancestor of the pull request base $tip"
  fi

  local want_diff
  want_diff="$(path_list_hash "$got_base" "$sha")"
  [[ "$got_diff" == "$want_diff" ]] ||
    refuse "the receipt hashes the change as $got_diff, but $got_base..$sha is $want_diff"

  # Scope is recomputed, never taken from the receipt: a receipt may only excuse
  # what it actually covered. The docs-only rule is internal/build/changes' with
  # go-cask's pattern list in internal/build/policy, reached through
  # `go run ./cmd/buildtool scope` — the same command the gate's own scope decision
  # and CI's scope job ask, so this cannot drift from either. It is asked IN the
  # repository being verified, because that repository's change is what it
  # classifies; GATE_RECEIPT_SCOPE_CMD overrides the command, which is how the
  # behaviour test drives this in a throwaway repository that is not this module.
  local want_scope=full repo_root
  repo_root="$(git rev-parse --show-toplevel)"
  if [[ "$(cd "$repo_root" && ${GATE_RECEIPT_SCOPE_CMD:-go run ./cmd/buildtool scope} \
    --base "$got_base" --head "$sha" --rule docs_only)" == "true" ]]; then
    want_scope=docs
  fi
  case "$got_scope" in
  docs | full) ;;
  *) refuse "the receipt scope '$got_scope' is neither docs nor full" ;;
  esac
  [[ "$got_scope" == "$want_scope" ]] ||
    refuse "a $got_scope receipt does not cover the $want_scope change $got_base..$sha"

  local check
  for check in ${require[@]+"${require[@]}"}; do
    grep -qx "check $check" <<<"$payload" ||
      refuse "the receipt does not list the required check '$check'"
  done

  local count
  count="$(printf '%s\n' "$payload" | grep -c '^check ' || true)"
  echo "gate-receipt: accepted $ref"
  echo "gate-receipt: $signature"
  echo "gate-receipt: scope $got_scope, $count checks, gate ran $(field "$payload" run) on $(field "$payload" runner) with $(field "$payload" go)"
  printf '%s\n' "$payload" | sed -n 's/^check /gate-receipt:   check /p'
}

cmd_show() {
  local sha=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
    --sha) sha="${2:?--sha needs a value}"; shift 2 ;;
    *) die "show: unknown option '$1'" ;;
    esac
  done
  [[ -n "$sha" ]] || sha="$(git rev-parse HEAD)"
  sha="$(git rev-parse --verify "$sha^{commit}")" ||
    die "show: not a commit: $sha"

  if [[ -f "$receipt_dir/$sha.receipt" ]]; then
    cat "$receipt_dir/$sha.receipt"
    return 0
  fi
  if git rev-parse --verify --quiet "$ref_prefix/$sha" >/dev/null; then
    receipt_body "$ref_prefix/$sha"
    return 0
  fi
  refuse "no receipt for $sha: this clone has no local file and $ref_prefix/$sha is not published"
}

[[ $# -ge 1 ]] || usage
command="$1"
shift
case "$command" in
create) cmd_create "$@" ;;
publish) cmd_publish "$@" ;;
verify) cmd_verify "$@" ;;
show) cmd_show "$@" ;;
suite)
  [[ $# -eq 0 ]] || die "suite takes no arguments"
  printf '%s\n' "${suite_full[@]}"
  ;;
-h | --help | help) usage ;;
*) die "unknown command '$command'" ;;
esac
