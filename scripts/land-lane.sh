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
#   land-lane.sh renew              push your own slot's idle deadline out
#   land-lane.sh release            only the holder may release
#
# The slot lives in the shared git dir ($(git rev-parse --git-common-dir)), so
# every worktree of this repository sees the same one. Nothing here is committed.
#
# Staleness is IDLE time, not age since acquisition. The owner file carries the
# moment its holder last refreshed it — `acquire` sets it and `renew` refreshes
# it — and only a slot idle for longer than LAND_LANE_STALE_MINUTES (90) may be
# taken over. It used to compare against the acquisition time alone, which is a
# live landing's deadline rather than an abandoned slot's: a gate run plus push
# that crossed 90 minutes was evicted by the next session's `acquire`, so two
# landings held the lane (#325). Nothing refreshes the slot silently: renewing is
# the holder's own deliberate call, because a check that renews as a side effect
# hands a second session in the same worktree a lane it does not hold (#299).
#
# A takeover is recorded in `<lane>/takeover` before the previous slot is
# dropped, because the evicted holder has no other way to learn what happened:
# without it, `release` told it "only the holder releases it" — the same message
# a session that never held the lane gets, and no hint that it had been evicted
# mid-landing (#325).
#
# The slot is claimed with an exclusive create (`set -C`), never with a check
# followed by a write: two waiters retrying on the same cadence both used to find
# it absent and both claim it (#299), which defeats the whole point of the lane.
# Eviction goes through `mv` for the same reason: the slot is never unowned while
# its old holder is being removed, so two simultaneous evictors cannot both
# complete the sequence.
set -euo pipefail

stale_minutes=${LAND_LANE_STALE_MINUTES:-90}
lane="$(git rev-parse --path-format=absolute --git-common-dir)/dsh-land-lane"
owner="$lane/owner"
repo_id_file="$lane/repo-id"
takeover_file="$lane/takeover"
# The slot is per clone; the token that proves WHICH acquisition holds it is per
# worktree: `<state>/mine`. Only the acquisition that wrote the token may renew
# or release the slot, so two attempts from one worktree — the same branch
# re-acquired after its first slot aged out — cannot free each other's lane.
state="$(git rev-parse --path-format=absolute --git-dir)"
mine="$state/dsh-land-lane-mine"

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

read_owner() { # read_owner <file> : sets r_pid, r_epoch, r_label, r_who, r_token
  r_pid=0
  r_epoch=0
  r_label="?"
  r_who="?"
  r_token=none
  IFS=$'\t' read -r r_pid r_epoch r_label r_who r_token <"$1" 2>/dev/null || true
  if [[ -z "$r_who" ]]; then
    r_who="?"
  fi
  if [[ -z "$r_token" ]]; then
    r_token=none
  fi
}

show() { # the holder of the slot as it stands
  read_owner "$owner"
  pid="$r_pid"
  epoch="$r_epoch"
  label="$r_label"
  who="$r_who"
  token="$r_token"
}

show_takeover() { # sets t_pid, t_epoch, t_label, t_who, t_how from <lane>/takeover
  t_pid=0
  t_epoch=0
  t_label="?"
  t_who="?"
  t_how="expired"
  IFS=$'\t' read -r t_pid t_epoch t_label t_who t_how <"$takeover_file" 2>/dev/null || true
}

new_token() {
  if [[ -r /proc/sys/kernel/random/uuid ]]; then
    cat /proc/sys/kernel/random/uuid
  else
    printf '%s-%s-%s-%s\n' "$(date -u +%s)" "$$" "${RANDOM:-0}" "${RANDOM:-0}"
  fi
}

my_token() { # the token of the acquisition this worktree currently owns
  cat "$mine" 2>/dev/null || echo none
}

write_token() { # write_token <token>
  mkdir -p "$state"
  printf '%s\n' "$1" >"$mine"
}

# A release or renew must not touch a slot that was replaced under it. Taking the
# slot with `mv` — rather than checking the owner and then removing it — is what
# makes that safe: of two racing callers exactly one moves the file, and the
# loser backs off. If the file it moved is not ours, it is put straight back.
take_aside() { # yields slot_taken=1 when the slot was taken (and is left in $slot_tmp)
  slot_taken=0
  slot_tmp=""
  tmp="$owner.read.$$.${RANDOM:-0}"
  if mv "$owner" "$tmp" 2>/dev/null; then
    slot_tmp="$tmp"
    slot_taken=1
  fi
}

restore_slot() { # put a slot we moved back when it was not ours
  # Only if the slot is still free: while we held it aside another holder may have
  # moved in, and that slot is not ours to overwrite. In that case the copy we
  # moved is dropped here — never left in the lane directory.
  if [[ -n "$slot_tmp" && -f "$slot_tmp" ]]; then
    if [[ ! -f "$owner" ]]; then
      mv "$slot_tmp" "$owner" 2>/dev/null || rm -f "$slot_tmp"
    else
      rm -f "$slot_tmp"
    fi
  fi
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
  echo "land lane: $label — $who (pid $pid, idle ${age}m)"
  if [[ "$who" == "$me" && "$token" != none && "$token" == "$(my_token)" ]]; then
    echo "land lane: you hold it"
    exit 0
  fi
  if [[ "$who" == "$me" ]]; then
    echo "land lane: this identity is recorded, but this worktree holds no outstanding acquisition for it" >&2
  fi
  exit 2
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
    # A fresh token per acquisition, in both the slot and this worktree's record:
    # the pair is what makes "this worktree holds THIS slot" checkable later.
    my_new_token="$(new_token)"
    if (set -C; printf '%s\t%s\t%s\t%s\t%s\n' "$$" "$(date -u +%s)" "$my_label" "$me" "$my_new_token" >"$owner") 2>/dev/null; then
      write_token "$my_new_token"
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
    show # the holder's pid, epoch, label, who, token
    age=$((($(date -u +%s) - epoch) / 60))
    if [[ "$who" == "$me" ]]; then
      if [[ "$token" != none && "$token" == "$(my_token)" ]]; then
        # This worktree already holds the slot through this very acquisition. A
        # second session in it cannot be told apart from the first by any file it
        # shares, so say "held" rather than hand out a second landing; refreshing
        # the deadline is `renew`, which the holder calls deliberately.
        echo "land lane: already held by $label ($me)"
        exit 0
      fi
      echo "land lane: held by $label — $who, and this worktree holds no outstanding acquisition for that identity (an earlier acquisition in it owns the slot); wait for it, or use --force when you know it is dead" >&2
      exit 1
    fi
    if [[ "$force" != true && "$age" -lt "$stale_minutes" ]]; then
      echo "land lane: held by $label — $who, idle ${age}m; wait for it, or use --force when you know it is dead" >&2
      exit 1
    fi
    if [[ "$attempt" -ge 5 ]]; then
      echo "land lane: $label — $who keeps winning the takeover race; retry" >&2
      exit 1
    fi
    # Record the eviction before the slot changes hands: it is the only trace the
    # evicted holder can read afterwards. Same field layout as the slot — pid,
    # epoch, label, who — with `how` in place of the token.
    how=expired
    if [[ "$force" == true && "$age" -lt "$stale_minutes" ]]; then
      how=forced
    fi
    printf '%s\t%s\t%s\t%s\t%s\n' "$pid" "$(date -u +%s)" "$label" "$who" "$how" >"$takeover_file.tmp.$$"
    mv "$takeover_file.tmp.$$" "$takeover_file" 2>/dev/null || true
    if [[ "$how" == forced ]]; then
      echo "land lane: taking over from $label — $who (idle ${age}m, --force)" >&2
    else
      echo "land lane: taking over from $label — $who (idle ${age}m, expired)" >&2
    fi
    take_aside # move the stale slot out of the way
    if [[ "$slot_taken" != 1 ]]; then
      # Another evictor moved it first; the next attempt claims the free slot.
      continue
    fi
    rm -f "$slot_tmp"
  done
  ;;
renew)
  # A holder's own verb: push the idle deadline out without giving the slot up, so
  # a gate run or a push that outlasts LAND_LANE_STALE_MINUTES is not evicted
  # mid-landing (#325). It is also the honest way to find out that it WAS evicted:
  # it fails and prints what happened instead of leaving the holder to guess.
  if [[ ! -f "$owner" ]]; then
    echo "land lane: free — nothing to renew; acquire it before landing" >&2
    exit 1
  fi
  take_aside
  if [[ "$slot_taken" != 1 ]]; then
    echo "land lane: the slot changed hands while renewing; re-check with 'status'" >&2
    exit 1
  fi
  slot_token=0
  read_owner "$slot_tmp"
  s_pid="$r_pid"
  s_epoch="$r_epoch"
  s_label="$r_label"
  s_who="$r_who"
  s_token="$r_token"
  if [[ "$s_who" != "$me" || "$s_token" != "$(my_token)" ]]; then
    show_takeover
    echo "land lane: this worktree does not hold the slot — $s_label — $s_who does" >&2
    if [[ -f "$takeover_file" ]]; then
      echo "land lane: taken over from $t_label — $t_who (${t_how}) at $(date -u -d "@$t_epoch" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || echo "epoch $t_epoch")" >&2
    fi
    restore_slot
    exit 1
  fi
  printf '%s\t%s\t%s\t%s\t%s\n' "$s_pid" "$(date -u +%s)" "$s_label" "$s_who" "$s_token" >"$slot_tmp"
  # While the slot was held aside it was absent, so an acquirer may have seen it
  # free and claimed it. Put ours back only if it is still free; if it is not,
  # the claim wins and this renewal is reported as lost rather than overwriting it.
  if [[ -f "$owner" ]]; then
    rm -f "$slot_tmp"
    echo "land lane: an acquirer took the slot while renewing; this worktree no longer holds it" >&2
    exit 1
  fi
  mv "$slot_tmp" "$owner"
  echo "land lane: renewed by $s_label ($me); idle 0m"
  ;;
release)
  if [[ ! -f "$owner" ]]; then
    echo "land lane: already free"
    exit 0
  fi
  take_aside
  if [[ "$slot_taken" != 1 ]]; then
    echo "land lane: the slot changed hands while releasing; re-check with 'status'" >&2
    exit 1
  fi
  slot_token=0
  read_owner "$slot_tmp"
  s_pid="$r_pid"
  s_epoch="$r_epoch"
  s_label="$r_label"
  s_who="$r_who"
  s_token="$r_token"
  if [[ "$s_who" != "$me" || "$s_token" != "$(my_token)" ]]; then
    # Either someone else holds the lane, or it is held by another acquisition in
    # this same worktree — neither may be released from here.
    show_takeover
    echo "land lane: held by $s_label — $s_who; only the holder releases it" >&2
    if [[ -f "$takeover_file" ]]; then
      echo "land lane: the slot was taken over from $t_label — $t_who (${t_how}) at $(date -u -d "@$t_epoch" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || echo "epoch $t_epoch")" >&2
    fi
    restore_slot
    exit 1
  fi
  rm -f "$slot_tmp"
  echo "land lane: released"
  ;;
*)
  echo "usage: land-lane.sh [status | whoami | acquire [--force] <label> | renew | release]" >&2
  exit 2
  ;;
esac
