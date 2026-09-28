---
type: Guide
title: board — go-cask
description: The coordinator's three readings — the board over open issues and lanes, the file-overlap matrix in flight, and the six checks that prove a landing.
version: v1
---

# board

The coordinator's readings, as rules over data the caller already read: the board over open
issues and claimed lanes, the file-overlap matrix in flight, and the six checks that prove a
landing (`docs/specs/coordination.md` §2, §4, §6).

## Claims are not evidence

Every row names its own authority, and the two are never mixed. A claim is a lane ref and the
tag object it points at — who claimed what, and when. Evidence is the clone ledger
`verify.ok` **and** a receipt for the same commit; a lane reporting "gated green" is a claim,
and only those two files together make it a fact. `Row.Gated` is true for neither alone, and
`board.Verdict` carries the signature and the published `refs/gate/<sha>` as separate readings
so a report can say which half is missing.

## Shape

Rule only; the caller reads the world.

```go
type Issue struct { Number, Title, URL string; Labels []string }
type Lane struct { Ref, Object, Message string; Claimed time.Time; AgeMinutes *int }
type PullRequest struct { Number; Branch, HeadOID string; Draft, AutoMerge bool; MergeState, Mergeable string }
type Row struct { Issue; Lane *Lane; PullRequest *PullRequest; Worktree, Holder string; Claim Staleness; Gated, Abandoned bool; Next Action }

func BoardOf(issues []Issue, lanes map[int]*Lane, pulls map[int]*PullRequest, where map[string]Where,
             unreadable map[int]bool, gated map[string]bool, notes []string, windowMinutes int) Board
func NextAction(row Row) Action
```

`cmd/buildtool board` runs the `gh` and `git` calls and renders the table; the forge listings
are parsed here (`ParseIssues`, `ParsePullRequests`, `ParseWorktrees`, `ParseDiff`) so the
decision is covered by a fixture test rather than by a live remote.

## Collisions

`Collide(lanes []Files, points []string, notes []string) Collisions` reports every
serialization point the caller names — untouched ones included, because a point missing from
the report looks like a point that does not exist — and then the ordinary overlaps. A point
with more than one lane is `Conflict`, and `Conflicting()` is the wave's binding constraint: two
lanes in one serialization point conflict by construction, whatever their line spans.

`ParseDiff` reads `git diff --unified=0` and keeps the new-side spans, which is what two lanes
diffed against one merge base can be compared on. A shared file with no intersecting span is
reported with no lines: it may still merge cleanly, and saying so is the difference between a
wave and a rebuild.

## Verify-landing

`Checks(Verdict) Verification` returns the six lines in the spec's order, each with the evidence
it was read from. Two lines are deliberately strict: a body that does not cite `Closes #NNN`
fails the merge check whatever the branch is named, and a scope nobody could read fails the
scope check rather than passing — a check that passes when it could not read its input is the
one that lets a drive-by file through.

## Testing

```bash
go test ./internal/build/board/
```

Table tests over fixtures: the `gh` and `git` output shapes, every staleness and next-action
arm, the overlap matrix including the serialization points, and the six checks in both
directions. No test needs a network, a remote or a second clone.

## See also

- [`../README.md`](../README.md) — the engine and its packages
- [`../claim/README.md`](../claim/README.md) — the lane verdict the board's staleness reading follows
- [`../taskstate/README.md`](../taskstate/README.md) — the branch report beside this one
- [`../../../../docs/specs/coordination.md`](../../../../docs/specs/coordination.md) — the contract these readings serve
