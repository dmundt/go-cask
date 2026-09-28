---
type: Guide
title: receipt (build engine) — go-cask
description: The gate receipt's format — parse, render, check-name rule, evidence identity, and the canonical changed-path list both sides hash.
version: v2
---

# receipt

The gate receipt: the record of what a green gate run covered, made portable so CI can reuse
that run instead of repeating the suite.

This package owns the **record**, not the transport. Reading a ref, signing a commit object
and pushing it are git's, and they live in `cmd/buildtool gate-receipt`; nothing here runs a
program or names a repository.

## The format is the contract

`Version` is the first line (`cask-gate-receipt 1`) and a record without it is refused
rather than read optimistically: the fields are what a verification compares, so a reader
that guessed at an unknown layout would be comparing nothing. After it, the fields are
`key value` lines in one order — `commit`, `tree`, `base`, `diff`, `scope`, one `check` per
check, then `coverage-tiers` when the run measured, then the run's own `go`, `runner` and
`run`. `Parse` accepts them in any order and ignores a field it does not know, because the
format is line-oriented and extensible; a repeated field keeps its first value, which is
what a verification reads. A value carrying a CR is not carried either: `Parse` reads a
CRLF as one line ending, so the CR of such a value would fold into the line ending a
render wrote and the value would come back shorter — or empty — on the next read. The
line is ignored, as a check name the character set cannot carry already was, and
`Parse(Render(r))` is `r` for every record `Parse` returns.

`CheckName` is the character-set rule. A check name is one token on one line, so a space or
a newline in it would not make the receipt wrong — it would make it unreadable, and a
receipt that cannot be read is refused by every verification.

## Evidence, without the run

`Record.Identity()` renders the lines that say **what** was covered — commit, tree, base,
the diff hash, the scope, the checks, the coverage count — and none of the run's own. Two
receipts for one commit that agree here are the same evidence, however often the gate ran
and on what host, so republishing the second may be skipped. A change to any field above
the run line moves the identity, which is exactly the case where the ref has to replace
what CI reads.

## One canonical path list

`CanonicalPaths` renders a changed path list as one path per line, sorted, blanks dropped,
with a trailing newline only when there is something to terminate. The **hash** is git's —
the repository's object format belongs to the repository, and a store may be sha256 — so
the caller pipes this text to `git hash-object --stdin`. What this owns is that both sides
of a verification feed it the same bytes: `git diff --name-only | sort` and a list a caller
collected have to hash the same way, or a receipt for an unchanged tree would be refused.

## Testing

`go test ./receipt/` — the render/parse round trip with the field order pinned, the
permissive-but-versioned reader, the two scope tokens, the check-name character set, the
identity with each evidence field moved in turn, and the canonical path list in both input
orders. `FuzzParse` keeps the reader total and its inverse honest: anything it accepts
re-renders to a record with the same identity, and the CR seed
(`testdata/fuzz/FuzzParse/seed-cr-in-value`) is the input that broke that half.
