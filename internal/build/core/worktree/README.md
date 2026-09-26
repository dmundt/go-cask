---
type: Guide
title: worktree (build engine) — go-cask
description: Linked-worktree rules for two toolchains — relative git link, admin directory, lock.
version: v3
---

# worktree

Linked worktree usable from more than one toolchain.

## The link is relative

- `GitFile(worktreeDir, adminDir)` → the worktree's `.git` file as a *relative* `gitdir:`
  line, forward slashes.
- `git worktree add` writes an absolute path in the creating toolchain's form → the other
  toolchain cannot resolve it → walks up to the enclosing repository, operates on the
  primary checkout → a gate run in such a worktree tests the wrong tree.
- `Resolves` → the caller's proof after writing: the link, read from the worktree, must lead
  to the admin directory `Admin(commonDir, name)` names, and to no other worktree.
- `SamePath` → absorbs what a git-printed path and a caller-joined path differ by:
  separators, trailing slash, redundant segment.
- `SamePath` never equates the two toolchains' forms (`D:/x` vs `/mnt/d/x`) → comparing
  across them is a bug, not a match; the check above compares paths from one toolchain.

## The lock is the protection

- `git worktree prune` deletes a registration whose admin `gitdir` it cannot resolve; that
  reverse link holds one toolchain's path form → every worktree the *other* toolchain made
  looks prunable.
- Git skips a locked worktree from either toolchain → `locked` is the only protection: no
  pre-command hook, no alias shadowing a built-in.
- Lock file name + text = caller's: they say who locked it and why.

## Testing

`go test ./worktree/` — relative rendering, steps up from another location, both path forms
read back, resolution to the right admin directory and refusal of every other, the
comparison's deliberate limits.
