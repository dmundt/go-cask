---
type: Guide
title: claim (build engine) — go-cask
description: Server-side landing-lane records — reading a coordination ref, matching an issue to a branch, and the verdict shared by check and claim.
version: v1
---

# claim

The server-side lane: one open pull request is one lane, claimed with a compare-and-swap on a
coordination ref (`refs/lane/<NNN>`). The local advisory slot is a different thing and stays
different — it serializes gate runs inside one clone ([`../lane`](../lane/README.md)).

This package owns the reading and the decision, never the calls: it names no `gh`, no git, no
path, no ref namespace. The caller supplies the ref prefix and a populated `LaneInput`; the
command in `cmd/buildtool` makes the calls.

## The ref names an issue

`IssueOf(ref, refPrefix)` → the ref's last segment, which must be digits. `BranchNamesIssue`
→ whether a head branch names that issue. A branch carries its issue as a whole segment
(`<type>/<NNN>-<kebab>`), so `feat/3890-x` does not name issue 389: a prefix match would let
one lane's pull request hold another's.

`ClaimMessage(branch, worktree)` renders the record an annotated tag carries. Both parts are
path-free on purpose: one landing runs through two toolchains that spell this directory
`D:/x/...` and `/mnt/d/x/...`, so a path in the record would make two clones' claims
indistinguishable.

## The verdict is one function

`DecideLane(input, window, now)` → `Free`, `Held`, `Claiming`, `Stale` or `Unreadable`.

The order is the protocol, and it is why `check` and `claim` cannot disagree — both read it:

- **no ref** → free, whatever pull requests name the issue (the ref is the claim; a
  coordination marker that was never created holds nothing);
- **unreadable record** → decided from nothing: only a deliberate release clears it, because
  guessing it is stale would take a live landing away;
- **an open pull request** → held whatever the claim's age. The PR is the lease, so a holder
  that died is visible as a PR nobody advances rather than as a claim nobody can interpret;
- **no pull request** → the window decides: inside it the claim is honoured (the time a
  claimer has to create its worktree and run the gate), past it the lane is stale and the
  next claimer takes it over with no human judging whether the holder is dead. An
  unreadable *moment* leaves the claim inside its window, for the same reason as above.

`Status` + `StatusJSON` are the `status --json` shape: a typed struct rather than hand-built
text, so the field names, their order and the null cases are pinned by a test.

## Testing

`go test ./claim/` — ref and branch parsing (including the near-miss numbers), the record's
path-free shape, every `DecideLane` outcome including the exact-window boundary, the state
strings, and the JSON shape with its nulls and its empty array.
