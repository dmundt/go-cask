#!/usr/bin/env bash
# Land lane — the single-slot lock that serializes who may push.
#
# Parallel sessions on one repository otherwise invalidate each other's PR
# branch: every merge makes the other branches "behind", the branch must be
# rebuilt, and the rebuild + gate window is long enough for the next merge to
# arrive first. One slot held for the whole landing (gate -> push -> merge)
# removes that loop instead of managing it.
#
#   land-lane.sh status             exit 0 = you hold it, 1 = free, 2 = someone else
#   land-lane.sh acquire <label>    take it; refuses a fresh holder
#   land-lane.sh acquire --force <label>
#   land-lane.sh release            only the holder may release
#
# The slot lives in the shared git dir ($(git rev-parse --git-common-dir)), so
# every worktree of this repository sees the same one. A lock older than
# LAND_LANE_STALE_MINUTES (90) is treated as abandoned. Nothing here is committed.
#
# The slot is claimed with an exclusive create (`set -C`), never with a check
# followed by a write: two waiters retrying on the same cadence both used to find
# it absent and both claim it (#299), which defeats the whole point of the lane.
set -euo pipefail

stale_minutes=${LAND_LANE_STALE_MINUTES:-90}
lane="$(git rev-parse --path-format=absolute --git-common-dir)/dsh-land-lane"
owner="$lane/owner"
repo_id_file="$lane/repo-id"

# The holder identity MUST be byte-identical in both toolchains that work in this
# repository: Windows git records `D:/x/repo`, WSL git records `/mnt/d/x/repo`, so
# a lane taken in one is invisible to the other's `.githooks/pre-push`. That
# matters because on Windows the gate only runs under WSL (the race build needs a
# C compiler) while the push only runs under the Windows git client (WSL's git has
# no HTTPS and no SSH key here) — the two halves of one landing use different
# toolchains by necessity.
#
# Repo identity: a random id written once into the shared git dir, so every
# worktree of this clone agrees and no absolute path is involved.
if [[ ! -f "$repo_id_file" ]]; then
  mkdir -p "$lane"
  if [[ -r /proc/sys/kernel/random/uuid ]]; then
    printf '%s\n' "$(cat /proc/sys/kernel/random/uuid)" >"$repo_id_file"
  else
    printf 'repo-%s-%s\n' "$(date -u +%s)" "${RANDOM:-0}${RANDOM:-0}" >"$repo_id_file"
  fi
fi
repo_id="$(cat "$repo_id_file" 2>/dev/null || echo repo-unknown)"

# Worktree identity: the primary checkout is `<common-dir>` itself; a linked one
# is identified by the name this repository gave it (`wt-<task>` under
# `.gocache/`), a bare directory name and therefore toolchain-neutral.
worktree="primary"
if [[ "$(git rev-parse --absolute-git-dir 2>/dev/null)" != "$(git rev-parse --path-format=absolute --git-common-dir)" ]]; then
  worktree="$(basename "$(git rev-parse --show-toplevel)")"
fi
me="$repo_id#primary:$worktree#$(git rev-parse --abbrev-ref HEAD)"

show() { # sets pid, epoch, label, who from the owner file (best effort)
  pid=0
  epoch=0
  label="?"
  who="?"
  IFS=$'\t' read -r pid epoch label who <"$owner" 2>/dev/null || true
}

case "${1:-status}" in
whoami)
  # The holder identity as this worktree computes it. Exposed so a test — and a
  # person debugging a refused push — can compare the identity two toolchains
  # produce without reverse-engineering the owner file.
  echo "$me"
  ;;
status)
  if [[ ! -f "$owner" ]]; then
    echo "land lane: free"
    exit 1
  fi
  show
  age=$((($(date -u +%s) - epoch) / 60))
  echo "land lane: $label — $who (pid $pid, ${age}m)"
  if [[ "$who" == "$me" ]]; then exit 0; else exit 2; fi
  ;;
acquire)
  shift
  force=false
  if [[ "${1:-}" == "--force" ]]; then
    force=true
    shift
  fi
  # The caller's label, kept apart from `label`: show() below overwrites `label`
  # with the holder's, and the slot must be written with the caller's.
  my_label=${1:-}
  if [[ -z "$my_label" ]]; then
    echo "usage: land-lane.sh acquire [--force] <label>" >&2
    exit 2
  fi
  mkdir -p "$lane"
  # Claim the slot by CREATING it, never by checking and then writing: two waiters
  # that retry on the same cadence both found the owner file absent and both wrote
  # one, so two landings held the lane and gated at the same time (#299). `set -C`
  # (noclobber) turns the redirection into an O_EXCL create, so of any number of
  # simultaneous acquirers exactly one can win it; every loser re-reads the
  # winner's metadata and decides again.
  attempt=0
  while :; do
    attempt=$((attempt + 1))
    if (set -C; printf '%s\t%s\t%s\t%s\n' "$$" "$(date -u +%s)" "$my_label" "$me" >"$owner") 2>/dev/null; then
      echo "land lane: acquired by $my_label ($me)"
      exit 0
    fi
    if [[ ! -f "$owner" ]]; then
      # The holder released between the create attempt and this read; try again.
      if [[ "$attempt" -ge 5 ]]; then
        echo "land lane: could not claim the slot; retry" >&2
        exit 1
      fi
      continue
    fi
    show # sets pid, epoch, label, who from the holder's file
    if [[ "$who" == "$me" ]]; then
      echo "land lane: already held by $label ($me)"
      exit 0
    fi
    age=$((($(date -u +%s) - epoch) / 60))
    if [[ "$force" != true && "$age" -lt "$stale_minutes" ]]; then
      echo "land lane: held by $label — $who for ${age}m; wait for it, or use --force when you know it is dead" >&2
      exit 1
    fi
    if [[ "$attempt" -ge 5 ]]; then
      echo "land lane: $label — $who keeps winning the takeover race; retry" >&2
      exit 1
    fi
    echo "land lane: taking over from $label — $who (${age}m)" >&2
    rm -f "$owner"
  done
  ;;
release)
  if [[ ! -f "$owner" ]]; then
    echo "land lane: already free"
    exit 0
  fi
  show
  if [[ "$who" != "$me" ]]; then
    echo "land lane: held by $label — $who; only the holder releases it" >&2
    exit 1
  fi
  rm -f "$owner"
  echo "land lane: released"
  ;;
*)
  echo "usage: land-lane.sh [status | whoami | acquire [--force] <label> | release]" >&2
  exit 2
  ;;
esac
