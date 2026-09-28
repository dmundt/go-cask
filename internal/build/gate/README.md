---
type: Guide
title: gate (build engine) — go-cask
description: Gate stamp — ledger of commits that ran the gate green; how it is read and written.
version: v4
---

# gate

## A ledger, not a slot

`Entry` = one verified commit: hash, scope, end time — three space-separated fields, RFC 3339
UTC stamp → entries sort as text, name no local zone. `Verified(ledger, sha)` = set membership
→ the pre-push hook's question. A single-line ledger would let one run in another worktree
invalidate every other branch's stamp and refuse its push. Keyed by commit → re-pushing an
unchanged commit costs no compute.

`Parse` accepts a line only when field 1 looks like a Git object name (lowercase hex, long
enough not to be a word) → a comment or a stray line is never read as a verification.
Unreadable timestamp → still an entry: the commit was verified, and refusing to read it would
refuse an authorised push.

## One line per commit, bounded

`Append(ledger, sha, scope, at, keep)`: replaces this commit's line; leaves every other entry
untouched (another worktree's verification is not this writer's to drop); keeps the newest
`keep` → one shared file cannot grow without limit.

Ledger = shared file in the git common directory, written by whichever toolchain ran the gate
→ the reader tolerates the other one's line endings.

## Testing

`go test ./gate/` — line format, rejected lines, set membership, the replace-don't-duplicate
writer, the bound, a CRLF ledger written by the other toolchain.
