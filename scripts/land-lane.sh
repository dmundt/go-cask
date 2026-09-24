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
set -euo pipefail

stale_minutes=${LAND_LANE_STALE_MINUTES:-90}
lane="$(git rev-parse --path-format=absolute --git-common-dir)/dsh-land-lane"
owner="$lane/owner"
me="$(git rev-parse --show-toplevel)#$(git rev-parse --abbrev-ref HEAD)"

show() { # sets pid, epoch, label, who from the owner file (best effort)
  pid=0
  epoch=0
  label="?"
  who="?"
  IFS=$'\t' read -r pid epoch label who <"$owner" 2>/dev/null || true
}

case "${1:-status}" in
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
  label=${1:-}
  if [[ -z "$label" ]]; then
    echo "usage: land-lane.sh acquire [--force] <label>" >&2
    exit 2
  fi
  mkdir -p "$lane"
  if [[ -f "$owner" ]]; then
    show
    age=$((($(date -u +%s) - epoch) / 60))
    if [[ "$who" != "$me" && "$force" != true && "$age" -lt "$stale_minutes" ]]; then
      echo "land lane: held by $label — $who for ${age}m; wait for it, or use --force when you know it is dead" >&2
      exit 1
    fi
    echo "land lane: taking over from $label — $who (${age}m)" >&2
  fi
  printf '%s\t%s\t%s\t%s\n' "$$" "$(date -u +%s)" "$label" "$me" >"$owner"
  echo "land lane: acquired by $label ($me)"
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
  echo "usage: land-lane.sh [status | acquire [--force] <label> | release]" >&2
  exit 2
  ;;
esac
