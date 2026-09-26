---
type: Guide
title: lane (build engine) — go-cask
description: Landing-lane records — slot identity, idle staleness, acquisition and refusal decisions.
version: v3
---

# lane

Landing-lane records: who a slot says holds it; how long it has been idle; what an
acquisition may do with a slot it did not write; the holder identity.

## The identity carries no path

`Identity(repoID, worktree, branch)` = `<clone id>#primary:<worktree>#<branch>`. Clone id =
a random value written once into the shared git directory → one clone's slot is never
recognised as another's. No absolute path anywhere: one landing here runs through two
toolchains — push via the Windows Git client, gate under WSL — spelling this directory
`D:/x/...` and `/mnt/d/x/...` → a path in the identity hides a lane from the other
toolchain's hook.

## Staleness is idle time

`Holder.Since` = last refresh; `Holder.Idle` measured from it, never from acquisition. Idle,
not age (#325): a gate run plus a push that outlasts the window is a live landing; the old
acquisition-time rule evicted it mid-flight. `renew` = the holder's own call → nothing
refreshes a slot as a side effect of asking (#299).

## What an acquisition may do

`Decide(slot, who, mine, window, force, now)` → `Created`, `AlreadyMine`,
`RefusedSameIdentity`, `RefusedFresh`, `TakeoverExpired`, `TakeoverForced`, `Unreadable`.
Two refusals carry the weight:

- slot held by **this identity through another acquisition** → refused: nothing in the
  worktree separates the two sessions; a second landing is the double-claim the slot exists
  to prevent;
- slot **inside its idle window** → refused without `--force`: a live landing, not an
  abandoned slot.

`TakeoverExpired` / `TakeoverForced` = the only evicting outcomes; the caller records a
`Takeover` for them — the evicted holder's only way to learn. `Takeover` shares the holder's
five-field record, `how` in the token field → one reader for both.

`SlotStatus` + `Status.ExitCode` = the shell contract: 0 held by this worktree, 1 free, 2
held by another.

Paths, record file names, idle-window default = caller's: this package owns the records and
the decisions, not the layout.

## Testing

`go test ./lane/` — identity shape, worktree name, both record round trips incl. partial
records, idle time, every acquisition outcome, the status exit codes.
