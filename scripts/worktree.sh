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
# deletes its index. Locked worktrees are never pruned, from either toolchain
# (measured, not assumed: an unlocked registration with an unresolvable `gitdir`
# is removed by `prune`, an otherwise identical locked one survives). `add` locks
# what it creates, `verify.sh` locks anything else it finds, and `prune` below
# refuses. Never run `git worktree prune` in this repository.
#
#   scripts/worktree.sh add <name> [<type>/<kebab>]   # from origin/main
#   scripts/worktree.sh remove <name>
#   scripts/worktree.sh lock [<name>...]              # all registered, if none named
#   scripts/worktree.sh prune                         # refuses; see the message
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
  # A failed fetch (for example WSL git without a usable SSL backend) would
  # silently base the new worktree on a stale origin/main — say so instead.
  if ! git -C "$primary" fetch --quiet --all 2>/dev/null; then
    echo "worktree.sh: 'git fetch' failed — using the local origin/main, which may be stale" >&2
  fi
  base="$(git -C "$primary" rev-parse --short origin/main 2>/dev/null || echo unknown)"
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
  # Prove it: git inside the worktree must resolve to the worktree's own git dir,
  # never to the primary checkout's. Both paths are produced by the same git, so
  # the comparison holds whatever path form that toolchain prints (D:/… or /mnt/d/…)
  # — comparing `--show-toplevel` against `pwd` does not, because the shell's path
  # and git's path disagree between Git Bash and WSL.
  expected_git_dir="$common/worktrees/wt-$name"
  resolved_git_dir="$(git -C "$dir" rev-parse --path-format=absolute --git-dir)"
  if [[ "$resolved_git_dir" != "$expected_git_dir" ]]; then
    echo "worktree.sh: $dir resolves to '$resolved_git_dir', expected '$expected_git_dir' — refusing to leave a broken worktree" >&2
    exit 2
  fi
  echo "worktree ready: $dir (${branch:-detached}) on $base — .git normalized and locked"
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
lock)
  # Lock one, several, or every registered worktree. git skips locked worktrees in
  # `prune`; that is the whole protection, because a worktree created by plain
  # `git worktree add` has no `locked` file and the other toolchain sees it as
  # prunable. Written directly rather than via `git worktree lock`, which itself
  # has to read the admin `gitdir` back and so only works from the creating
  # toolchain.
  names="$*"
  if [[ -z "$names" ]]; then
    for adm in "$common"/worktrees/*/; do
      [[ -d "$adm" ]] || continue
      names="$names $(basename "$adm")"
    done
  fi
  if [[ -z "$names" ]]; then
    echo "worktree.sh: no linked worktrees to lock"
    exit 0
  fi
  for name in $names; do
    [[ "$name" == wt-* ]] || name="wt-$name"
    adm="$common/worktrees/$name"
    if [[ ! -d "$adm" ]]; then
      echo "worktree.sh: no such worktree: $name" >&2
      continue
    fi
    if [[ -e "$adm/locked" ]]; then
      echo "already locked: $name"
      continue
    fi
    printf 'locked by scripts/worktree.sh — the .git link is toolchain-relative; never run git worktree prune\n' \
      >"$adm/locked"
    echo "locked: $name"
  done
  ;;
prune)
  cat >&2 <<'EOF'
worktree.sh: refusing to prune.

`git worktree prune` deletes every registration whose admin `gitdir` file points
at a path this toolchain cannot resolve — and that link can only hold one absolute
path form, so it is unresolvable for every worktree the *other* toolchain created.
That is how three live worktrees lost their registrations and their indexes here.

git has no pre-command hook, so nothing can intercept `git worktree prune` and no
alias can shadow a built-in; git's own `locked` file is the only real protection,
and it does hold: prune skips a locked worktree from either toolchain, while an
identical unlocked one is deleted.

  scripts/worktree.sh lock [<name>...]   # lock one, several, or all worktrees
  scripts/worktree.sh list               # what is registered, and whether locked
  scripts/worktree.sh remove <name>      # remove exactly one, on purpose
EOF
  exit 2
  ;;
list)
  git -C "$primary" worktree list
  ;;
*)
  echo "usage: worktree.sh [add <name> [<branch>] | remove <name> | lock [<name>...] | prune | list]" >&2
  exit 2
  ;;
esac
