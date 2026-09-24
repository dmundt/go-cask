#!/usr/bin/env bash
# Create or remove a task worktree that BOTH toolchains resolve.
#
# `git worktree add` run with the Windows git records a Windows absolute path in
# the new worktree's `.git` file. WSL's git cannot resolve that path, so it walks
# up to the enclosing repository and silently operates on the *primary checkout* —
# a gate run inside such a worktree tests the wrong tree. Writing the relative
# form fixes it for both toolchains (scripts/AGENT.md documents the mirror case,
# a worktree created from WSL), and verify.sh refuses to run when it detects the
# mismatch.
#
# Worktrees always land in the *primary* checkout's `.gocache/`, derived from the
# common git dir, so the command behaves the same run from any worktree.
#
# Each worktree is created "locked" (a `locked` file in its admin dir). The
# reverse link, <common>/worktrees/wt-<name>/gitdir, holds one toolchain's path
# form, so the *other* toolchain's `git worktree list` reports the worktree as
# prunable and `git worktree prune` would delete a live registration — which also
# deletes its index. Locked worktrees are never pruned. Never run
# `git worktree prune` in this repository.
#
#   scripts/worktree.sh add <name> [<type>/<kebab>]   # from origin/main
#   scripts/worktree.sh remove <name>
#   scripts/worktree.sh list
set -euo pipefail

cmd="${1:-}"
shift || true

# `<primary>/.git` — shared by every linked worktree.
common="$(git rev-parse --path-format=absolute --git-common-dir)"
primary="$(dirname "$common")"
wt_parent="$primary/.gocache"

case "$cmd" in
add)
  name="${1:?usage: worktree.sh add <name> [<branch>]}"
  branch="${2:-}"
  dir="$wt_parent/wt-$name"
  if [[ -e "$dir" ]]; then
    echo "worktree.sh: $dir already exists" >&2
    exit 1
  fi
  git -C "$primary" fetch --quiet --all 2>/dev/null || true
  if [[ -n "$branch" ]]; then
    git -C "$primary" worktree add "$dir" -b "$branch" origin/main
  else
    git -C "$primary" worktree add "$dir" --detach origin/main
  fi
  # Relative to <primary>/.gocache/wt-<name>, and identical for both toolchains.
  printf 'gitdir: ../../.git/worktrees/wt-%s\n' "$name" >"$dir/.git"
  # Lock it: a stray `git worktree prune` from the other toolchain must not be
  # able to remove this registration (and with it the worktree's index).
  printf 'locked by scripts/worktree.sh — the .git link is toolchain-relative; never run git worktree prune\n' \
    >"$common/worktrees/wt-$name/locked"
  # Prove it: git must resolve the worktree to itself, not to the primary checkout.
  resolved="$(git -C "$dir" rev-parse --path-format=absolute --show-toplevel)"
  if [[ "$resolved" != "$(cd "$dir" && pwd)" ]]; then
    echo "worktree.sh: $dir resolves to '$resolved' — refusing to leave a broken worktree" >&2
    exit 2
  fi
  echo "worktree ready: $dir (${branch:-detached}) — .git normalized and locked"
  ;;
remove)
  name="${1:?usage: worktree.sh remove <name> [--force]}"
  force="${2:-}"
  dir="$wt_parent/wt-$name"
  if [[ -d "$dir" && "$force" != "--force" ]] && [[ -n "$(git -C "$dir" status --porcelain 2>/dev/null)" ]]; then
    echo "worktree.sh: $dir has uncommitted changes — commit, or pass --force to discard" >&2
    exit 3
  fi
  # `--force --force` also unlocks. The command needs to read the admin gitdir
  # back, which only the creating toolchain can resolve, so on failure remove both
  # halves directly. Never `git worktree prune` here: it is repo-wide and would
  # delete the *other* worktrees whose gitdir holds the other toolchain's form.
  if ! git -C "$primary" worktree remove --force --force "$dir" 2>/dev/null; then
    rm -rf "$dir" "$common/worktrees/wt-$name"
  fi
  echo "worktree removed: wt-$name"
  ;;
list)
  git -C "$primary" worktree list
  ;;
*)
  echo "usage: worktree.sh [add <name> [<branch>] | remove <name> | list]" >&2
  exit 2
  ;;
esac
