#!/usr/bin/env bash
# Behaviour tests for the landing machinery: scripts/land-lane.sh and
# .githooks/pre-push.
#
# Both encode rules that are easy to get wrong and hard to see, because they only
# bite on a host where the gate and the push run in DIFFERENT toolchains. On this
# project's Windows host that split is forced: the race gate needs a C compiler
# and therefore WSL, while the push needs the Windows git client because WSL's git
# cannot open an HTTPS connection there. One landing is therefore carried out by
# two toolchains, and these tests pin the two properties that make that work:
#
#   1. the land-lane holder identity must not embed a toolchain-specific path, or
#      a lane taken in one toolchain is invisible to the other's pre-push hook;
#   2. the gate stamp must survive a gate run in another worktree, or a branch
#      that was verified green is refused at push time.
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

# ---- 1. the holder identity is toolchain-neutral ------------------------------
# A drive letter or a mount point is exactly what differs between the toolchains
# (`D:/x/repo` versus `/mnt/d/x/repo`), so the identity must carry neither; `~`
# joins its parts so no separator can appear at all. The worktree's own script is
# used (by absolute path, since the clone changes directory), so the assertion
# cannot accidentally measure the primary checkout.
wt_lane="$root/scripts/land-lane.sh"
me="$(bash "$wt_lane" whoami)"
if grep -qE '[A-Za-z]:/|/mnt/|^/' <<<"$me"; then
  check "identity carries no path from either toolchain" "portable" "$me"
else
  check "identity carries no path from either toolchain" "portable" "portable"
fi

# ---- 2. lane round trip, and a second clone must not share the identity -------
git clone -q --no-hardlinks --shared "$root" "$scratch/repo"
main_id="$(cat "$root/.git/dsh-land-lane/repo-id" 2>/dev/null || echo none)"
cd "$scratch/repo"
# The clone needs the scripts too; the mode bit does not survive the copy.
cp "$root/scripts/land-lane.sh" scripts/land-lane.sh
chmod +x scripts/land-lane.sh
# Reading the id materializes it, which is what the first real invocation does.
clone_id="$(./scripts/land-lane.sh whoami)"; clone_id="${clone_id%%~*}"
check "lane starts free" "1" "$(./scripts/land-lane.sh status >/dev/null 2>&1; echo $?)"
./scripts/land-lane.sh acquire test-label >/dev/null
check "the holder recognises its own lane" "0" "$(./scripts/land-lane.sh status >/dev/null 2>&1; echo $?)"
check "a clone records its own repo id" "distinct" \
  "$([[ "$clone_id" != "$main_id" && "$clone_id" != none && -n "$clone_id" ]] && echo distinct || echo shared)"
# The two identities must differ in their repo id alone: that is what keeps one
# clone's lane from being recognised as another's. Comparing only this field
# avoids depending on how any particular lane file happens to be resolved.
main_who="$(cd "$root" && bash "$wt_lane" whoami)"; main_who="${main_who%%~*}"
clone_who="$(./scripts/land-lane.sh whoami)"; clone_who="${clone_who%%~*}"
check "the clone's repo id is its own" "distinct" \
  "$([[ -n "$clone_who" && "$clone_who" != "$main_who" ]] && echo distinct || echo shared)"
./scripts/land-lane.sh release >/dev/null
check "release empties the lane" "1" "$(./scripts/land-lane.sh status >/dev/null 2>&1; echo $?)"

# ---- 2b. acquiring the lane is atomic -----------------------------------------
# A check followed by a write let two waiters retry into the same free slot and
# both claim it, so two landings gated and pushed at once (#299). Claiming is an
# exclusive create now, so of any number of simultaneous acquirers exactly one
# holds the lane and the rest refuse. Contenders need distinct identities, and an
# identity names its worktree, so they are linked worktrees of this clone: they
# share its lane file but not its identity.
lane_owner="$(git rev-parse --path-format=absolute --git-common-dir)/dsh-land-lane/owner"
mkdir -p "$scratch/race"
race_dirs=()
for i in 1 2 3 4; do
  d="$scratch/race/wt$i"
  git worktree add -q "$d" -b "race$i"
  cp "$root/scripts/land-lane.sh" "$d/scripts/land-lane.sh"
  chmod +x "$d/scripts/land-lane.sh"
  race_dirs+=("$d")
done
clean_rounds=0
for _round in 1 2 3; do
  # The test clears the slot directly: release is the holder's call, and the
  # holder here is whichever contender won the previous round.
  rm -f "$lane_owner" "$scratch/go"
  : >"$scratch/wins"
  : >"$scratch/losses"
  pids=()
  for d in "${race_dirs[@]}"; do
    (
      cd "$d"
      while [[ ! -e "$scratch/go" ]]; do :; done
      if ./scripts/land-lane.sh acquire race >/dev/null 2>&1; then
        echo won >>"$scratch/wins"
      else
        echo lost >>"$scratch/losses"
      fi
    ) &
    pids+=($!)
  done
  touch "$scratch/go"
  for pid in "${pids[@]}"; do wait "$pid" 2>/dev/null || true; done
  winners="$(grep -c won "$scratch/wins" || true)"
  losers="$(grep -c lost "$scratch/losses" || true)"
  if [[ "$winners" == "1" && "$losers" == "3" ]]; then
    clean_rounds=$((clean_rounds + 1))
  fi
done
rm -f "$lane_owner"
check "three rounds of four simultaneous acquirers leave exactly one holder" "3" "$clean_rounds"

# ---- 3. the gate stamp is a ledger, not a single slot -------------------------
# The hook's own code decides here: a real commit is pushed to a bare remote with
# the real `.githooks/pre-push` installed, and the ledger is the only variable.
#
# The hook checks the lane FIRST and the stamp second, so holding the lane for
# real would couple these cases to whatever another session is doing. Instead the
# lane is stubbed to "held" through a PATH shim, which keeps the assertion on the
# rule this section is about; the lane's own contract is covered in section 2.
remote="$scratch/remote.git"
git init -q --bare "$remote"
work="$scratch/work"
git init -q "$work"
git -C "$work" config user.email test@example.com
git -C "$work" config user.name test
git -C "$work" remote add origin "$remote"
git -C "$work" config core.hooksPath .githooks
mkdir -p "$work/.githooks" "$work/scripts" "$scratch/shimbin"
cp "$root/.githooks/pre-push" "$work/.githooks/pre-push"
cat >"$scratch/shimbin/land-lane.sh" <<'SHIM'
#!/bin/sh
# Stand-in for the lane: held. This section asserts the stamp rule.
exit 0
SHIM
chmod +x "$scratch/shimbin/land-lane.sh"
# `git push` runs the hook from the worktree root, where the shim must win.
cp "$scratch/shimbin/land-lane.sh" "$work/scripts/land-lane.sh"
printf 'seed\n' >"$work/file.txt"
git -C "$work" add file.txt
git -C "$work" commit -qm seed
head_sha="$(git -C "$work" rev-parse HEAD 2>/dev/null || true)"
if [[ -z "$head_sha" ]]; then
  echo "not ok - could not create a commit for the hook tests"
  fails=$((fails + 1))
fi
other_sha="$(printf 'b%.0s' $(seq 1 40))"
# The hook resolves the ledger as <absolute common dir>/verify.ok.
stamp="$(git -C "$work" rev-parse --path-format=absolute --git-common-dir)/verify.ok"

push_with_hook() { # push_with_hook <ledger> ; expect the hook's verdict as exit code
  printf '%s\n' "$1" >"$stamp"
  git -C "$work" push -q origin "HEAD:refs/heads/x" >/dev/null 2>&1
}

check "hook refuses a commit with no entry" "1" \
  "$(printf '%s docs 2026-01-01T00:00:00Z\n' "$other_sha" >"$stamp"; \
     git -C "$work" push -q origin HEAD:refs/heads/x >/dev/null 2>&1; echo $?)"

printf '%s full 2026-01-02T00:00:00Z\n' "$head_sha" >>"$stamp"
check "hook accepts a verified commit despite another worktree's entry" "0" \
  "$(git -C "$work" push -q origin HEAD:refs/heads/x >/dev/null 2>&1; echo $?)"
check "the other worktree's entry survives the push" "1" "$(grep -c "^$other_sha " "$stamp")"

# The writer side, mirroring verify.sh: only this commit's line is replaced.
{
  grep -v "^$head_sha " "$stamp" || true
  printf '%s full 2026-01-03T00:00:00Z\n' "$head_sha"
} | tail -n 200 >"$stamp.tmp" && mv "$stamp.tmp" "$stamp"
check "writer preserves the other worktree's entry" "1" "$(grep -c "^$other_sha " "$stamp")"
check "writer keeps one line per commit" "1" "$(grep -c "^$head_sha " "$stamp")"

if [[ "$fails" -ne 0 ]]; then
  echo "land-lane and hook tests failed: $fails"
  exit 1
fi
echo "land-lane and hook tests passed"
