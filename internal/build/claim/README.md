---
type: Guide
title: claim (build engine) — go-cask
description: Server-side landing-lane records — reading a coordination ref, matching an issue to a branch, and the verdict shared by check and claim.
version: v3
---

# claim

Server-side lane: one open pull request = one lane, claimed with a compare-and-swap on a
coordination ref (`refs/lane/<NNN>`). The local advisory slot stays different — it serializes
gate runs inside one clone ([`../lane`](../lane/README.md)).

Owns the reading and the decision, never the calls: no `gh`, git, path or ref namespace; the
caller supplies the ref prefix and a populated `LaneInput`, `cmd/gate` makes the calls.

## The ref names an issue

`IssueOf(ref, refPrefix)` → the ref's last segment, which must be digits. `BranchNamesIssue`
→ whether a head branch names that issue. A branch carries its issue as a whole segment
(`<type>/<NNN>-<kebab>`), so `feat/3890-x` does not name issue 389: a prefix match would let
one lane's pull request hold another's.

`ClaimMessage(branch, worktree)` renders the record an annotated tag carries. Both parts
path-free: one landing runs through two toolchains spelling this directory `D:/x/...` and
`/mnt/d/x/...`, so a path would make two clones' claims indistinguishable.

## The verdict is one function

`DecideLane(input, window, now)` → `Free`, `Held`, `Claiming`, `Stale` or `Unreadable`.

One order, read by both `check` and `claim` → they cannot disagree:

- **no ref** → free, whatever pull requests name the issue (the ref is the claim; a
  coordination marker never created holds nothing);
- **unreadable record** → decided from nothing: only a deliberate release clears it —
  guessing it stale would take a live landing away;
- **an open pull request** → held whatever the claim's age. The PR is the lease, so a dead
  holder shows as a PR nobody advances, not as a claim nobody can interpret;
- **no pull request** → the window decides: inside it the claim is honoured (the time a
  claimer has to create its worktree and run the gate), past it the lane is stale and the next
  claimer takes it over, no human judging whether the holder is dead. An unreadable *moment*
  leaves the claim inside its window, for the same reason.

`Status` + `StatusJSON` are the `status --json` shape: a typed struct, so field names, order
and null cases are pinned by a test.

## Testing

`go test ./claim/` — ref and branch parsing (including the near-miss numbers), the record's
path-free shape, every `DecideLane` outcome including the exact-window boundary, the state
strings, and the JSON shape with its nulls and its empty array.
