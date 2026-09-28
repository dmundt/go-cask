---
type: Guide
title: landing (build engine) — go-cask
description: The landing machinery — the server-side lane, the local advisory slot, the gate stamp and the gate landing.
version: v1
---

# landing

One package for the four decisions a landing makes: the **claim** on the
server-side lane, the **slot** this clone holds while it gates, the **stamp** the
pre-push hook reads, and the **receipt** CI reuses.

## claim — the server-side lane



Server-side lane: one open pull request = one lane, claimed with a compare-and-swap on a
coordination ref (`refs/lane/<NNN>`). The local advisory slot stays different — it serializes
gate runs inside one clone ([`../lane`](../lane/README.md)).

Owns the reading and the decision, never the calls: no `gh`, git, path or ref namespace; the
caller supplies the ref prefix and a populated `LaneInput`, `cmd/gate` makes the calls.

### The ref names an issue

`IssueOf(ref, refPrefix)` → the ref's last segment, which must be digits. `BranchNamesIssue`
→ whether a head branch names that issue. A branch carries its issue as a whole segment
(`<type>/<NNN>-<kebab>`), so `feat/3890-x` does not name issue 389: a prefix match would let
one lane's pull request hold another's.

`ClaimMessage(branch, worktree)` renders the record an annotated tag carries. Both parts
path-free: one landing runs through two toolchains spelling this directory `D:/x/...` and
`/mnt/d/x/...`, so a path would make two clones' claims indistinguishable.

### The verdict is one function

`DecideLane(input, window, now)` → `LaneFree`, `LaneHeld`, `LaneClaiming`, `LaneStale` or `LaneUnreadable`.

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

### Testing

`go test ./claim/` — ref and branch parsing (including the near-miss numbers), the record's
path-free shape, every `DecideLane` outcome including the exact-window boundary, the state
strings, and the JSON shape with its nulls and its empty array.

## lane — the local advisory slot



Landing-lane records: who a slot says holds it; how long it has been idle; what an
acquisition may do with a slot it did not write.

### The identity carries no path

`Identity(repoID, worktree, branch)` = `<clone id>#primary:<worktree>#<branch>`. Clone id =
a random value written once into the shared git directory → one clone's slot is never
recognised as another's. No absolute path anywhere: one landing here runs through two
toolchains — push via the Windows Git client, gate under WSL — spelling this directory
`D:/x/...` and `/mnt/d/x/...`, so a path in the identity hides a lane from the other
toolchain's hook.

### Staleness is idle time

`Holder.Since` = last refresh; `Holder.Idle` measured from it, never from acquisition. Idle,
not age (#325): a gate run plus a push that outlasts the window is a live landing.
`renew` = the holder's own call → nothing refreshes a slot as a side effect of asking
(#299).

### What an acquisition may do

`Decide(slot, who, mine, window, force, now)` → `SlotCreated`, `SlotAlreadyMine`,
`SlotRefusedSameIdentity`, `SlotRefusedFresh`, `SlotTakeoverExpired`, `SlotTakeoverForced`, `SlotUnreadable`.
Two refusals carry the weight:

- slot held by **this identity through another acquisition** → refused: nothing in the
  worktree separates the two sessions, and a second landing is the double-claim the slot
  exists to prevent;
- slot **inside its idle window** → refused without `--force`: a live landing, not an
  abandoned slot.

`SlotTakeoverExpired` / `SlotTakeoverForced` = the only evicting outcomes; the caller records a
`Takeover` for them. `Takeover` shares the holder's five-field record, `how` in the token
field → one reader for both.

`SlotStatus` + `SlotState.ExitCode` = the shell contract: 0 held by this worktree, 1 free, 2
held by another.

Paths, record file names, idle-window default = caller's.

### Testing

`go test ./lane/` — identity shape, worktree name, both record round trips incl. partial
records, idle time, every acquisition outcome, the status exit codes.

## stamp — the gate stamp



### A ledger, not a slot

`Entry` = one verified commit: hash, scope, end time — three space-separated fields, RFC 3339
UTC stamp → entries sort as text, name no local zone. `Verified(ledger, sha)` = set membership
→ the pre-push hook's question. A single-line ledger would let one run in another worktree
invalidate every other branch's stamp and refuse its push. Keyed by commit → re-pushing an
unchanged commit costs no compute.

`Parse` accepts a line only when field 1 looks like a Git object name (lowercase hex, long
enough not to be a word) → a comment or a stray line is never read as a verification.
Unreadable timestamp → still an entry: the commit was verified, and refusing to read it would
refuse an authorised push.

### One line per commit, bounded

`Append(ledger, sha, scope, at, keep)`: replaces this commit's line; leaves every other entry
untouched (another worktree's verification is not this writer's to drop); keeps the newest
`keep` → one shared file cannot grow without limit.

Ledger = shared file in the git common directory, written by whichever toolchain ran the gate
→ the reader tolerates the other one's line endings.

### Testing

`go test ./gate/` — line format, rejected lines, set membership, the replace-don't-duplicate
writer, the bound, a CRLF ledger written by the other toolchain.

## receipt — the gate receipt



The gate receipt: the record of what a green gate run covered, made portable so CI reuses
that run instead of repeating the suite.

This package owns the **record**, not the transport — reading a ref, signing a commit object
and pushing it are git's, in `cmd/gate gate-receipt`. Nothing here runs a program or
names a repository.

### The format is the contract

`Version` is the first line (`cask-gate-receipt 1`); a record without it is refused rather
than read optimistically, because the fields are what a verification compares. After it, the
fields are `key value` lines in one order: `commit`, `tree`, `base`, `diff`, `scope`, one
`check` per check, then `coverage-tiers` when the run measured, then the run's own `go`,
`runner` and `run`. `Parse` accepts them in any order and ignores a field it does not know,
because the format is line-oriented and extensible; a repeated field keeps its first value,
which is what a verification reads. A value carrying a CR is not carried either: `Parse`
reads a CRLF as one line ending, so the CR would fold into the line ending a render wrote
and the value would come back shorter — or empty — on the next read. The line is ignored, as
a check name the character set cannot carry already was, and `Parse(Render(r))` is `r` for
every record `Parse` returns.

`CheckName` is the character-set rule. A check name is one token on one line: a space or a
newline in it would not make the receipt wrong, it would make it unreadable, and a receipt
that cannot be read is refused by every verification.

### Evidence, without the run

`Record.Identity()` renders the lines that say **what** was covered — commit, tree, base,
the diff hash, the scope, the checks, the coverage count — and none of the run's own. Two
receipts for one commit that agree here are the same evidence, however often the gate ran
and on what host, so republishing the second may be skipped. A change to any field above the
run line moves the identity — exactly the case where the ref has to replace what CI reads.

### One canonical path list

`CanonicalPaths` renders a changed path list as one path per line, sorted, blanks dropped,
with a trailing newline only when there is something to terminate. The **hash** is git's —
the repository's object format belongs to the repository, and a store may be sha256 — so the
caller pipes this text to `git hash-object --stdin`. What this owns is that both sides of a
verification feed it the same bytes: `git diff --name-only | sort` and a list a caller
collected have to hash the same way, or a receipt for an unchanged tree would be refused.

### Testing

`go test ./receipt/` — the render/parse round trip with the field order pinned, the
permissive-but-versioned reader, the two scope tokens, the check-name character set, the
identity with each evidence field moved in turn, and the canonical path list in both input
orders. `FuzzParse` keeps the reader total and its inverse honest: anything it accepts
re-renders to a record with the same identity, and the CR seed
(`testdata/fuzz/FuzzParse/seed-cr-in-value`) is the input that broke that half.
