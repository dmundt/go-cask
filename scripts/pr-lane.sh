#!/usr/bin/env bash
# The pull-request lane: one open pull request is one lane.
#
# A landing is coordinated through the one record every session, every clone and
# the operator can already see — the pull request — and the claim on it is a
# server-side compare-and-swap, not a local file. Creating the coordination ref
# `refs/lane/<NNN>` succeeds exactly once: the API answers 422 when the ref
# exists, so two sessions cannot both claim one lane however their attempts
# interleave, and every clone, machine and toolchain reads the same answer. The
# ref is a coordination marker, not a branch: nothing is committed to it and no
# push touches it, so it stays outside the branch namespace branch-naming.md §2
# governs.
#
#   pr-lane.sh claim <issue>       take the lane; 0 = yours, 1 = refused
#   pr-lane.sh check <issue>       report whether a claim would succeed; writes nothing
#   pr-lane.sh status [<issue>] [--json]
#                                  every lane on the remote and the pull request behind it
#   pr-lane.sh release <issue> [--force]
#                                  give the lane up (deletes refs/lane/<issue>)
#   pr-lane.sh whoami              this worktree and the lanes it recorded
#
# The ref points at an annotated tag object that records WHO claimed the lane and
# WHEN, because the ref itself carries no timestamp and the claimant's pull
# request does not exist yet at claim time. That record is what makes liveness
# objective:
#
#   * an OPEN pull request naming the issue holds the lane — the PR is the lease,
#     so a holder that dies is visible as a PR nobody is advancing, not as a file
#     nobody can interpret;
#   * a claim with no pull request yet is honoured for PR_LANE_STALE_MINUTES
#     (default 90) — the window in which a claimer creates its worktree and runs
#     the gate — and taken over after that, with no human deciding whether the
#     holder is dead;
#   * `release` is the deliberate exit, for an abandoned claim or a finished one.
#
# The one convention that matters: push the branch and open the pull request as
# DRAFT as early as you can. The PR is the lease; until it exists the lane is
# only protected by the window.
#
# The local advisory slot (scripts/land-lane.sh) is a different thing and stays a
# different thing: it keeps two gate runs in ONE clone from overlapping, so a
# clone does not pay twice for the same tree. It is not the lane, and
# .githooks/pre-push no longer requires it.
set -euo pipefail

stale_minutes=${PR_LANE_STALE_MINUTES:-90}

usage() {
  cat >&2 <<'EOF'
usage: pr-lane.sh [claim <issue> | check <issue> | status [<issue>] [--json] | release <issue> [--force] | whoami]

  claim    take the lane for an issue (server-side CAS on refs/lane/<issue>)
  check    report whether claim would succeed, without claiming anything
  status   list the lanes on the remote and the pull request behind each one
  release  give the lane up, after its pull request merged or was closed
  whoami   this worktree and the lanes it recorded locally
EOF
  exit 2
}

die() { echo "pr-lane: $*" >&2; exit 2; }
refuse() { echo "pr-lane: $*" >&2; exit 1; }

# The lane lives on the remote, so the GitHub CLI is not optional — but which
# binary it is depends on the toolchain: plain `gh` under Linux, `gh.exe` where
# Git Bash carries the Windows PATH, and the Windows CLI again when the gate's
# WSL shell is the one running this (interop makes `/mnt/c/...gh.exe` callable).
# `PR_LANE_GH` overrides the search for a host that keeps it elsewhere.
GH_BIN=""
gh() {
  if [[ -z "$GH_BIN" ]]; then
    local candidate resolved
    for candidate in "${PR_LANE_GH:-}" gh gh.exe \
      "/mnt/c/Program Files/GitHub CLI/gh.exe" \
      "/c/Program Files/GitHub CLI/gh.exe"; do
      [[ -n "$candidate" ]] || continue
      if [[ -x "$candidate" ]]; then
        GH_BIN="$candidate"
        break
      fi
      # `type -P` and not `command -v`: this very function is named `gh`, and
      # `command -v gh` would report the function and then fail to run it.
      resolved="$(type -P "$candidate" 2>/dev/null || true)"
      if [[ -n "$resolved" ]]; then
        GH_BIN="$resolved"
        break
      fi
    done
  fi
  [[ -n "$GH_BIN" ]] || die "gh is required: the lane lives on the remote, not in this clone"
  command "$GH_BIN" "$@"
}

# The repository is read from the origin remote, so no call and no credential is
# needed to answer "which lanes are these" — and `PR_LANE_REPO` overrides it for
# a caller that has no remote to read.
repo=""
resolve_repo() {
  [[ -n "$repo" ]] && return 0
  if [[ -n "${PR_LANE_REPO:-}" ]]; then
    repo="$PR_LANE_REPO"
    return 0
  fi
  local url
  url="$(git config --get remote.origin.url 2>/dev/null || true)"
  case "$url" in
    *://*/*) repo="${url#*://}"; repo="${repo#*/}" ;;
    *@*:*) repo="${url#*:}" ;;
  esac
  repo="${repo%.git}"
  if [[ -z "$repo" || "$repo" != */* ]]; then
    repo="$(gh repo view --json nameWithOwner --jq .nameWithOwner 2>/dev/null || true)"
  fi
  [[ -n "$repo" && "$repo" == */* ]] \
    || die "cannot resolve the repository from the origin remote; set PR_LANE_REPO"
}

# The base a lane is claimed against: the commit the remote calls main. Read with
# `ls-remote` rather than a fetch, so status and check never rewrite the caller's
# worktree refs.
base_sha() {
  local sha=""
  sha="$(git ls-remote origin refs/heads/main 2>/dev/null | cut -f1)" || true
  if [[ -z "$sha" ]]; then
    sha="$(git rev-parse origin/main 2>/dev/null)" || die "cannot resolve origin/main"
  fi
  printf '%s\n' "$sha"
}

# This worktree, as the claim record names it. Both parts are path-free on
# purpose: the record is written by one toolchain and may be read by the other,
# where `D:/x/repo` and `/mnt/d/x/repo` never compare equal.
worktree_name() {
  printf '%s\n' "$(basename "$(git rev-parse --show-toplevel 2>/dev/null || echo primary)")"
}

branch_name() {
  git rev-parse --abbrev-ref HEAD 2>/dev/null || echo detached
}

# Every lane on the remote, as `<ref> <sha>` lines. The remote is the authority: a
# lane claimed by another clone, machine or person is visible here, which is what
# the local slot could never do.
lane_refs() {
  resolve_repo
  gh api "repos/$repo/git/matching-refs/lane" \
    --jq '.[] | "\(.ref) \(.object.sha)"' 2>/dev/null || true
}

# read_lane <issue>: sets l_sha (the ref's tag object), l_ident, l_claimed, l_base.
# Returns 1 when the lane is free, 2 when its record cannot be read.
read_lane() {
  l_sha=""
  l_ident=""
  l_claimed=""
  l_base=""
  local out=""
  l_sha="$(gh api "repos/$repo/git/ref/lane/$1" --jq .object.sha 2>/dev/null)" || return 1
  [[ -n "$l_sha" ]] || return 1
  out="$(gh api "repos/$repo/git/tags/$l_sha" --jq '.message, .tagger.date, .object.sha' 2>/dev/null)" || return 2
  { read -r l_ident; read -r l_claimed; read -r l_base; } <<<"$out"
  [[ -n "$l_ident" && -n "$l_claimed" ]] || return 2
  return 0
}

# age_minutes <iso8601>: whole minutes since the instant, empty when unreadable.
age_minutes() {
  local then now
  then="$(date -u -d "$1" +%s 2>/dev/null)" || return 1
  now="$(date -u +%s)"
  printf '%s\n' "$(( (now - then) / 60 ))"
}

# The open pull request that names an issue, as `<number> <branch>`. A PR names
# its issue through its head branch, whose name carries the issue number
# (branch-naming.md §2, `<type>/<NNN>-<kebab>`).
open_pr_for() {
  resolve_repo
  gh pr list --repo "$repo" --state open --limit 100 --json number,headRefName \
    --jq '.[] | "\(.number) \(.headRefName)"' 2>/dev/null |
    awk -v n="$1" '$2 ~ ("(^|/)" n "(-|$)") { print; exit }' || true
}

# The claim record is an annotated tag object, so the identity and the moment a
# lane was claimed are readable from any clone without a local file.
make_claim_object() { # make_claim_object <issue> <base sha> -> tag object sha
  local message
  message="branch=$(branch_name) worktree=$(worktree_name)"
  gh api -X POST "repos/$repo/git/tags" \
    -f "tag=lane-$1" -f "message=$message" -f "object=$2" -f "type=commit" \
    --jq .sha 2>/dev/null
}

# try_claim <issue> <tag sha>: 0 = the CAS won, 1 = the lane exists, 2 = error.
# The create is the whole protocol: it is atomic on the server, so exactly one of
# any number of simultaneous claimers wins, whatever order they arrive in.
try_claim() {
  local out=""
  if out="$(gh api -X POST "repos/$repo/git/refs" \
    -f "ref=refs/lane/$1" -f "sha=$2" --jq .ref 2>&1)"; then
    return 0
  fi
  if grep -qi 'already exists' <<<"$out"; then
    return 1
  fi
  echo "pr-lane: claiming the lane for #$1 failed: $out" >&2
  return 2
}

# drop_ref <issue>: 0 = deleted, 1 = it was already gone, 2 = another error.
drop_ref() {
  local out=""
  if out="$(gh api -X DELETE "repos/$repo/git/refs/lane/$1" 2>&1)"; then
    return 0
  fi
  if grep -qi 'does not exist\|not found' <<<"$out"; then
    return 1
  fi
  echo "pr-lane: releasing the lane for #$1 failed: $out" >&2
  return 2
}

# This worktree's record of the lanes it claimed. The claim itself is on the
# server; the record only answers "did THIS checkout start this landing", so a
# session tells its own lane from a stranger's without a network round trip.
record_file() {
  printf '%s\n' "$(git rev-parse --path-format=absolute --git-dir)/pr-lane"
}

record_add() {
  local f
  f="$(record_file)"
  grep -qx "$1" "$f" 2>/dev/null || printf '%s\n' "$1" >>"$f"
}

record_remove() {
  local f tmp
  f="$(record_file)"
  [[ -f "$f" ]] || return 0
  tmp="$f.tmp.$$"
  grep -vx "$1" "$f" >"$tmp" 2>/dev/null || true
  mv "$tmp" "$f"
}

record_list() {
  local f
  f="$(record_file)"
  [[ -f "$f" ]] && cat "$f" || true
}

issue_number() {
  case "${1:-}" in
    ''|*[!0-9]*) die "an issue number is required (got '${1:-}')" ;;
  esac
  printf '%s\n' "$1"
}

require_open_issue() {
  resolve_repo
  local state
  state="$(gh issue view "$1" --repo "$repo" --json state --jq .state 2>/dev/null)" \
    || die "cannot read issue #$1 (does it exist?)"
  if [[ "$state" != "OPEN" ]]; then
    refuse "issue #$1 is ${state:-unknown} — a lane lands an open issue's pull request"
  fi
}

# lane_verdict <issue>: sets v_state (free|held|claiming|stale|unreadable), v_pr,
# v_ident, v_age. Shared by check and claim so the two can never disagree.
lane_verdict() {
  local pr
  v_state=free
  v_pr=""
  v_ident=""
  v_age=""
  local rc=0
  read_lane "$1" || rc=$?
  if [[ "$rc" == 1 ]]; then
    return 0
  fi
  if [[ "$rc" == 2 ]]; then
    v_state=unreadable
    return 0
  fi
  pr="$(open_pr_for "$1")"
  if [[ -n "$pr" ]]; then
    v_state=held
    v_pr="$pr"
    v_ident="$l_ident"
    return 0
  fi
  v_ident="$l_ident"
  if v_age="$(age_minutes "$l_claimed")"; then
    if [[ "$v_age" -ge "$stale_minutes" ]]; then
      v_state=stale
    else
      v_state=claiming
    fi
  else
    v_age=""
    v_state=claiming
  fi
  return 0
}

cmd_claim() {
  local issue base tag attempt
  issue="$(issue_number "${1:-}")"
  require_open_issue "$issue"
  resolve_repo
  base="$(base_sha)"
  for attempt in 1 2 3; do
    tag="$(make_claim_object "$issue" "$base")" \
      || die "cannot record the claim for #$issue (creating the tag object failed)"
    [[ -n "$tag" ]] || die "cannot record the claim for #$issue (empty tag object)"
    if try_claim "$issue" "$tag"; then
      record_add "$issue"
      echo "pr-lane: claimed #$issue — refs/lane/$issue at ${base:0:12}"
      echo "pr-lane: push the branch and open the pull request as a draft now; the PR is the lease"
      return 0
    fi
    lane_verdict "$issue"
    case "$v_state" in
    held)
      refuse "lane #$issue is held by pull request #${v_pr%% *} (${v_pr#* }) — wait for it to merge, or close it if the work is abandoned; a closed PR releases the lane"
      ;;
    claiming)
      refuse "lane #$issue was claimed ${v_age:-some} minutes ago by ${v_ident:-another session} and has no pull request yet — it is inside the ${stale_minutes}-minute claim window; wait, or release it if that session is gone"
      ;;
    unreadable)
      refuse "lane #$issue exists but its claim record cannot be read — release it deliberately with 'pr-lane.sh release $issue' if it is abandoned"
      ;;
    esac
    if [[ "$attempt" -eq 1 ]]; then
      echo "pr-lane: lane #$issue is stale (${v_ident:-unknown claimer}, ${v_age:-?} minutes old, no pull request) — taking it over" >&2
    fi
    drop_ref "$issue" || true
  done
  refuse "another claimer keeps winning the race for #$issue; retry"
}

cmd_check() {
  local issue base tag
  issue="$(issue_number "${1:-}")"
  require_open_issue "$issue"
  resolve_repo
  lane_verdict "$issue"
  case "$v_state" in
  free)
    echo "pr-lane: lane #$issue is free"
    ;;
  held)
    refuse "lane #$issue is held by pull request #${v_pr%% *} (${v_pr#* })"
    ;;
  claiming)
    refuse "lane #$issue was claimed ${v_age:-some} minutes ago by ${v_ident:-another session}; no pull request yet, so it is inside the ${stale_minutes}-minute claim window"
    ;;
  stale)
    echo "pr-lane: lane #$issue is stale (${v_ident:-unknown claimer}, ${v_age:-?} minutes old, no pull request) — a claim would take it over"
    ;;
  unreadable)
    refuse "lane #$issue exists but its claim record cannot be read"
    ;;
  esac
}

json_escape() { printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g'; }

cmd_status() {
  local want="" as_json=false arg
  for arg in "$@"; do
    case "$arg" in
      --json) as_json=true ;;
      ''|*[!0-9]*) die "unknown argument '$arg' (status takes an optional issue number and --json)" ;;
      *) want="$arg" ;;
    esac
  done

  resolve_repo
  local lanes prs mine first=true ref sha issue
  lanes="$(lane_refs)"
  prs="$(gh pr list --repo "$repo" --state open --limit 100 --json number,headRefName \
    --jq '.[] | "\(.number) \(.headRefName)"' 2>/dev/null || true)"
  mine="$(record_list)"

  [[ "$as_json" == true ]] && printf '['
  while IFS=' ' read -r ref sha; do
    [[ -n "$ref" ]] || continue
    issue="${ref##*/}"
    [[ -z "$want" || "$want" == "$issue" ]] || continue
    local pr="" branch="" number="" ident="" age="" state="stale"
    while IFS=' ' read -r number branch; do
      [[ -n "$number" ]] || continue
      if [[ "$branch" =~ (^|/)${issue}(-|$) ]]; then
        pr="$number"
        break
      fi
      branch=""
    done <<<"$prs"
    if [[ -n "$pr" ]]; then
      state="held"
    else
      if read_lane "$issue"; then
        ident="$l_ident"
        age="$(age_minutes "$l_claimed" || echo '?')"
        if [[ "$age" != "?" && "$age" -lt "$stale_minutes" ]]; then
          state=claiming
        fi
      else
        ident="unreadable claim record"
        age="?"
      fi
    fi
    if [[ "$as_json" == true ]]; then
      [[ "$first" == true ]] || printf ','
      first=false
      printf '{"issue":%s,"ref":"%s","object":"%s","pull_request":%s,"branch":"%s","claimant":"%s","age_minutes":%s,"mine":%s,"state":"%s"}' \
        "$issue" "$ref" "$sha" "${pr:-null}" "$(json_escape "$branch")" \
        "$(json_escape "$ident")" "$([[ "$age" =~ ^[0-9]+$ ]] && echo "$age" || echo null)" \
        "$(grep -qx "$issue" <<<"$mine" && echo true || echo false)" "$state"
    elif [[ "$state" == "held" || -n "$pr" ]]; then
      printf '#%s  %s  pull request #%s (%s)%s\n' \
        "$issue" "$ref" "$pr" "$branch" \
        "$(grep -qx "$issue" <<<"$mine" && echo ' — this worktree' || true)"
    elif [[ "$state" == "claiming" ]]; then
      printf '#%s  %s  claimed %s minutes ago by %s, no pull request yet%s\n' \
        "$issue" "$ref" "$age" "$ident" \
        "$(grep -qx "$issue" <<<"$mine" && echo ' — this worktree' || true)"
    else
      printf '#%s  %s  stale: %s (%s minutes old)%s\n' \
        "$issue" "$ref" "$ident" "$age" \
        "$(grep -qx "$issue" <<<"$mine" && echo ' — this worktree' || true)"
    fi
  done <<<"$lanes"
  [[ "$as_json" == true ]] && printf ']\n'
  return 0
}

cmd_release() {
  local issue force=false pr
  issue="$(issue_number "${1:-}")"
  case "${2:-}" in
    "") : ;;
    --force) force=true ;;
    *) die "unknown argument '$2' (release takes an issue number and an optional --force)" ;;
  esac
  resolve_repo
  pr="$(open_pr_for "$issue")"
  if [[ -n "$pr" && "$force" != true ]]; then
    refuse "pull request #${pr%% *} (${pr#* }) is still open for #$issue — merge or close it first, or pass --force to free the lane anyway"
  fi
  if drop_ref "$issue"; then
    record_remove "$issue"
    echo "pr-lane: released #$issue — refs/lane/$issue deleted"
    return 0
  fi
  record_remove "$issue"
  echo "pr-lane: lane #$issue was already free"
}

cmd_whoami() {
  resolve_repo
  local lanes
  lanes="$(record_list | tr '\n' ' ')"
  printf 'pr-lane: %s @ %s (%s); claimed here: %s\n' \
    "$repo" "$(branch_name)" "$(worktree_name)" "${lanes:-none}"
}

case "${1:-}" in
claim) shift; cmd_claim "${1:-}" ;;
check) shift; cmd_check "${1:-}" ;;
status) shift; cmd_status "$@" ;;
release) shift; cmd_release "${1:-}" "${2:-}" ;;
whoami) cmd_whoami ;;
*) usage ;;
esac
