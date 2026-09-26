#!/usr/bin/env bash
# ARCHIVED — superseded by `go test ./cmd/buildtool/ -run PRLane`.
#
# The cases are unchanged: a free lane claimed by one atomic create, the refusal while a
# pull request is open, the claim window, the takeover of an abandoned claim, an unreadable
# record, a closed issue, four simultaneous claimers leaving exactly one winner, release and
# its idempotence, and whoami. The stub `gh` in a throwaway repository became an in-process
# fake remote (fakeLane in cmd/buildtool/prlane_test.go) whose ref create is atomic under a
# mutex, so the race case still measures the rule rather than the stub.
#
# Nothing runs this copy; see ../shell/README.md.
# Behaviour tests for scripts/pr-lane.sh — the pull-request lane.
#
# The lane is a server-side object, so the script is exercised against a stub
# `gh` that keeps refs, tag objects, issues and pull requests in a scratch
# directory. That is not a convenience: the properties worth pinning are the ones
# a real GitHub shows only under a race or after a crash — that claiming is one
# atomic create, that a lane with an open pull request is refused, that a fresh
# claim without a pull request is honoured, and that an abandoned claim is taken
# over without anyone deciding whether the holder is dead.
#
# The stub refuses any invocation it does not implement, so a change that starts
# calling gh differently fails here instead of passing silently.
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT
fails=0

check() { # check <description> <expected> <actual>
  if [[ "$2" == "$3" ]]; then
    echo "ok - $1"
  else
    echo "not ok - $1 (expected '$2', got '$3')"
    fails=$((fails + 1))
  fi
}

contains() { # contains <description> <needle> <haystack>
  if grep -q -- "$2" <<<"$3"; then
    echo "ok - $1"
  else
    echo "not ok - $1 (no '$2' in: $3)"
    fails=$((fails + 1))
  fi
}

# ---- the stub remote ----------------------------------------------------------
state="$scratch/ghstate"
mkdir -p "$state/refs/lane" "$state/tags" "$scratch/bin"
printf 'test/repo\n' >"$state/repo"
printf '389 OPEN\n390 OPEN\n391 CLOSED\n392 OPEN\n393 OPEN\n' >"$state/issues"
: >"$state/prs"

cat >"$scratch/bin/gh" <<'SHIM'
#!/bin/sh
# Stand-in for the gh calls pr-lane.sh makes (and nothing else).
set -eu
state="${PR_LANE_TEST_STATE:?}"
cmd="${1:-}"
shift || true

case "$cmd" in
repo)
  # repo view --json nameWithOwner --jq .nameWithOwner
  cat "$state/repo"
  ;;
issue)
  # issue view <n> --repo <slug> --json state --jq .state
  n="$2"
  found="$(awk -v n="$n" '$1 == n { print $2 }' "$state/issues")"
  [ -n "$found" ] || { echo "gh-shim: no such issue $n" >&2; exit 1; }
  printf '%s\n' "$found"
  ;;
pr)
  # pr list --repo <slug> --state open --limit 100 --json ... --jq ...
  cat "$state/prs"
  ;;
api)
  method=GET
  if [ "${1:-}" = "-X" ]; then
    method="$2"
    shift 2
  fi
  path="${1:-}"
  shift || true
  case "$path" in
  */git/matching-refs/lane)
    for f in "$state"/refs/lane/*; do
      [ -e "$f" ] || continue
      printf 'refs/lane/%s %s\n' "$(basename "$f")" "$(cat "$f")"
    done
    ;;
  */git/ref/lane/*)
    n="${path##*/}"
    if [ -e "$state/refs/lane/$n" ]; then
      cat "$state/refs/lane/$n"
    else
      echo "gh-shim: Not Found (HTTP 404)" >&2
      exit 1
    fi
    ;;
  */git/refs/lane/*)
    n="${path##*/}"
    if [ "$method" = "DELETE" ]; then
      if [ -e "$state/refs/lane/$n" ]; then
        rm -f "$state/refs/lane/$n"
      else
        echo "gh-shim: Reference does not exist (HTTP 422)" >&2
        exit 1
      fi
    else
      echo "gh-shim: unsupported method $method on $path" >&2
      exit 2
    fi
    ;;
  */git/refs)
    ref=""
    sha=""
    for a in "$@"; do
      case "$a" in
      ref=*) ref="${a#ref=}" ;;
      sha=*) sha="${a#sha=}" ;;
      esac
    done
    n="${ref##*/}"
    # The real endpoint is an atomic create; `set -C` is what makes the stub one
    # too, so the race test measures the rule and not the stub.
    if ! (set -C; printf '%s' "$sha" >"$state/refs/lane/$n") 2>/dev/null; then
      echo "gh-shim: Reference already exists (HTTP 422)" >&2
      exit 1
    fi
    printf '%s\n' "$ref"
    ;;
  */git/tags)
    tag=""
    message=""
    object=""
    for a in "$@"; do
      case "$a" in
      tag=*) tag="${a#tag=}" ;;
      message=*) message="${a#message=}" ;;
      object=*) object="${a#object=}" ;;
      esac
    done
    when="${PR_LANE_TEST_TAG_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
    sha="$(printf '%s|%s|%s|%s' "$tag" "$message" "$object" "$when" | cksum | tr -d ' ' | cut -c1-8)"
    sha="$(printf '%.0s0' $(seq 1 32))$sha"
    printf '%s\n%s\n%s\n' "$message" "$when" "$object" >"$state/tags/$sha"
    printf '%s\n' "$sha"
    ;;
  */git/tags/*)
    sha="${path##*/}"
    if [ -e "$state/tags/$sha" ]; then
      cat "$state/tags/$sha"
    else
      echo "gh-shim: Not Found (HTTP 404)" >&2
      exit 1
    fi
    ;;
  *)
    echo "gh-shim: unsupported api path: $path" >&2
    exit 2
    ;;
  esac
  ;;
*)
  echo "gh-shim: unsupported command: $cmd $*" >&2
  exit 2
  ;;
esac
SHIM
chmod +x "$scratch/bin/gh"

# A real repository, so `git ls-remote origin refs/heads/main` answers the way it
# does in the checkout the script ships in.
git init -q --bare "$scratch/remote.git"
git init -q "$scratch/work"
git -C "$scratch/work" config user.email test@example.com
git -C "$scratch/work" config user.name test
git -C "$scratch/work" remote add origin "$scratch/remote.git"
mkdir -p "$scratch/work/scripts"
printf 'seed\n' >"$scratch/work/file.txt"
git -C "$scratch/work" add file.txt
git -C "$scratch/work" commit -qm seed
git -C "$scratch/work" branch -M main
git -C "$scratch/work" push -q origin main
cp "$root/scripts/pr-lane.sh" "$scratch/work/scripts/pr-lane.sh"
chmod +x "$scratch/work/scripts/pr-lane.sh"
head_sha="$(git -C "$scratch/work" rev-parse HEAD)"

export PATH="$scratch/bin:$PATH"
export PR_LANE_TEST_STATE="$state"
lane() { # lane <args...> : sets run_status and run_out
  run_status=0
  run_out="$(cd "$scratch/work" && ./scripts/pr-lane.sh "$@" 2>&1)" || run_status=$?
}
record="$scratch/work/.git/pr-lane"

# ---- 1. a free lane is claimed by one atomic create ---------------------------
lane check 389
check "a free lane checks clean" "0" "$run_status"
contains "check says the lane is free" "lane #389 is free" "$run_out"

lane claim 389
check "the first claim wins the lane" "0" "$run_status"
contains "the claim names the coordination ref" "refs/lane/389" "$run_out"
contains "the claim tells the claimer to open the PR" "the PR is the lease" "$run_out"
check "the lane ref exists once" "1" "$(ls "$state/refs/lane" | grep -c '^389$' || true)"
check "the claim is recorded in this worktree" "1" "$(grep -cx '389' "$record" || true)"

# The ref points at the claim record, and the record names the claimer: that is
# what makes a claim without a pull request readable by another session.
claim_object="$(cat "$state/refs/lane/389")"
check "the ref points at a claim record" "1" "$([[ -s "$state/tags/$claim_object" ]] && echo 1 || echo 0)"
check "the claim record names the branch" "1" "$(grep -c 'branch=main' "$state/tags/$claim_object" || true)"
check "the claim record names the worktree" "1" "$(grep -c 'worktree=work' "$state/tags/$claim_object" || true)"

# ---- 2. a fresh claim with no pull request yet is held, not stolen ------------
lane claim 389
check "a second claimer is refused inside the claim window" "1" "$run_status"
contains "the refusal says why" "claim window" "$run_out"
check "the refused claimer did not take the lane" "$claim_object" "$(cat "$state/refs/lane/389")"

lane check 389
check "check refuses a lane inside the claim window" "1" "$run_status"
contains "check names the claim window" "claim window" "$run_out"

# ---- 3. an open pull request holds the lane ------------------------------------
printf '412 chore/389-pr-as-lane\n' >"$state/prs"
lane claim 389
check "a lane whose pull request is open is refused" "1" "$run_status"
contains "the refusal names the pull request" "pull request #412" "$run_out"
contains "the refusal names the branch" "chore/389-pr-as-lane" "$run_out"

lane check 389
check "check refuses a held lane" "1" "$run_status"
contains "check names the holder's pull request" "pull request #412" "$run_out"

# ---- 4. status is the fleet's view --------------------------------------------
lane status
check "status succeeds with lanes present" "0" "$run_status"
contains "status names the issue" "#389" "$run_out"
contains "status names the ref" "refs/lane/389" "$run_out"
contains "status names the pull request" "pull request #412" "$run_out"
contains "status marks the lane this worktree claimed" "this worktree" "$run_out"

lane status --json
check "status --json succeeds" "0" "$run_status"
contains "status --json carries the pull request number" '"pull_request":412' "$run_out"
contains "status --json marks ownership" '"mine":true' "$run_out"
contains "status --json reports the state" '"state":"held"' "$run_out"
contains "status --json is an array" '^\[' "$run_out"

lane status 389
check "status filters to one issue" "0" "$run_status"
contains "the filtered status keeps its lane" "#389" "$run_out"
if grep -q '#392' <<<"$run_out"; then
  check "the filtered status hides other lanes" "hidden" "shown"
else
  check "the filtered status hides other lanes" "hidden" "hidden"
fi

# ---- 5. release needs the pull request to be finished --------------------------
lane release 389
check "release refuses while the pull request is open" "1" "$run_status"
contains "the refusal says what to do" "merge or close it first" "$run_out"
lane release 389 --force
check "release --force frees the lane deliberately" "0" "$run_status"
check "the ref is gone" "0" "$(ls "$state/refs/lane" | grep -c '^389$' || true)"
check "the local record is cleaned" "0" "$(grep -cx '389' "$record" || true)"

# ---- 6. an abandoned claim is taken over, and an old one is stale --------------
old_date="$(date -u -d '3 hours ago' +%Y-%m-%dT%H:%M:%SZ)"
old_object="$(PR_LANE_TEST_TAG_DATE="$old_date" gh api -X POST repos/test/repo/git/tags \
  -f tag=lane-390 -f message='branch=chore/390-abandoned worktree=gone' -f object="$head_sha" -f type=commit --jq .sha)"
printf '%s' "$old_object" >"$state/refs/lane/390"
lane check 390
check "check reports an abandoned lane as claimable" "0" "$run_status"
contains "check says it is stale" "stale" "$run_out"
lane claim 390
check "the abandoned lane is taken over" "0" "$run_status"
contains "the takeover is announced" "stale" "$run_out"
check "the takeover is recorded here" "1" "$(grep -cx '390' "$record" || true)"
check "the taken-over lane now carries a fresh claim" "1" "$([[ "$(cat "$state/refs/lane/390")" != "$old_object" ]] && echo 1 || echo 0)"

# A lane whose record cannot be read is refused rather than stolen: the ref is
# there, so someone claimed it, and only a deliberate release may free it.
printf '%s' '0000000000000000000000000000000000000000' >"$state/refs/lane/393"
lane claim 393
check "an unreadable claim record is refused" "1" "$run_status"
contains "the refusal asks for a deliberate release" "release" "$run_out"
rm -f "$state/refs/lane/393"

# ---- 7. an issue that is not open cannot be landed ----------------------------
lane claim 391
check "a closed issue is refused" "1" "$run_status"
contains "the refusal names the issue state" "issue #391 is CLOSED" "$run_out"

# ---- 8. four simultaneous claimers leave exactly one winner -------------------
wins="$scratch/wins"
losses="$scratch/losses"
: >"$wins"
: >"$losses"
pids=()
for i in 1 2 3 4; do
  (
    cd "$scratch/work"
    if ./scripts/pr-lane.sh claim 392 >/dev/null 2>&1; then
      echo won >>"$wins"
    else
      echo lost >>"$losses"
    fi
  ) &
  pids+=($!)
done
for pid in "${pids[@]}"; do wait "$pid" 2>/dev/null || true; done
check "four simultaneous claimers leave exactly one winner" "1" "$(grep -c won "$wins" || true)"
check "and three losers" "3" "$(grep -c lost "$losses" || true)"
check "the raced lane exists exactly once" "1" "$(ls "$state/refs/lane" | grep -c '^392$' || true)"

# ---- 9. releasing a finished lane is idempotent -------------------------------
lane release 390
check "a finished lane is released" "0" "$run_status"
lane release 390
check "releasing a free lane is not an error" "0" "$run_status"
contains "the second release says it was already free" "already free" "$run_out"

lane whoami
check "whoami reports the repository" "0" "$run_status"
contains "whoami names the repository" "test/repo" "$run_out"
contains "whoami lists the lanes recorded here" "392" "$run_out"

lane status
check "status still lists the raced lane" "0" "$run_status"
contains "the raced lane is listed" "#392" "$run_out"

if [[ "$fails" -ne 0 ]]; then
  echo "pr-lane tests failed: $fails"
  exit 1
fi
echo "pr-lane tests passed"
