---
type: Guide
title: scope (build engine) — go-cask
description: What a change set triggers — its classification, and the gate run's scope, concurrency and escape hatches.
version: v1
---

# scope

Two readings of one question: what does this change touch, and what must a run of
the gate therefore do.

## changes — change-set classification



- `Pattern` matches a changed path three ways: whole path (`Exact`); directory prefix plus
  its separator (`Prefix`); trailing extension (`Suffix`). Exactly one field set per
  pattern; an empty pattern matches nothing, not everything.
- `Rule` = one classification + how it reads the set:
  - `Any` → holds when ≥1 path matches ("this change touches Go source");
  - `All` → holds when the set is non-empty and every path matches ("this change is
    documentation only"). Fails on an empty set deliberately: "nothing changed" read as
    "documentation only" would silently shrink what a gate verifies.
- `Also` = earlier rules that imply a rule → "a security-relevant change touches Go **or**
  the CI configuration" without restating the Go patterns.
- `Classify` rejects a nameless rule, a duplicate name, an implication of a later rule and an
  unknown mode → a table cannot be circular; reads top to bottom.
- `Select` = the caller's split: paths a pattern list covered; paths it did not.
- Ships no pattern list and no rule: documentation paths and implications are the
  repository's.

### Testing

`go test ./changes/` — each pattern form, the `Any`/`All` verdicts incl. the empty set,
the implication, every rejected table, the split.

## verify — the gate run



The gate run's own decisions, split from the run itself: what a run **covers**, how many
packages it may build at once, whether an escape hatch **dropped a step**.

Reads no environment, runs nothing. Caller supplies variable names and values, so nothing
here names a repository, a path or a command — that is what keeps the package reusable and
lets these decisions be tested without a process.

### The run's record is the point

The gate writes a record for the commit it verified; the pre-push hook believes that record
without asking anything else. So every comparison that could make a half-run look complete
lives here, tested, not beside the printing:

- `Escape(value, dropAll)` — did an escape hatch drop a step. Only the exact value the gate
  reads as set counts; the drop-everything switch wins whatever the individual value says.
  A hatch reading as set for the report and unset for the record is exactly how a half-run
  acquires a green stamp.

### The scope

`Requested(value, variable, docsOnly)` → `Full` or `Docs`.

Empty value = automatic scope: the change set decides. An **asserted** documentation scope
fails when the change is not documentation-only, so a caller that asked for the cheap scope
can never be handed a green run for a code change. A scope that is neither is refused, not
defaulted — silently running the whole gate would hide the caller's typo behind minutes of
work. `Scope.String()` renders the two spellings, one of which is written into the ledger.

### The concurrency

`Jobs(value, variable, fallback)` → a positive decimal integer, or the caller's fallback
when nothing is set.

Syntax is checked here, not handed to `strconv`, which accepts `" 8"`, `"+8"` and `"08"`.
A concurrency the caller did not mean is worse than the default: the number reaches how many
packages are built and tested at once, so a value that silently became zero or one would
turn the gate's longest step serial.

### Testing

`go test ./verify/` — every scope value with and without a documentation-only change, both
scope spellings, the concurrency reader across the shapes `strconv` would have accepted,
the escape comparison including casing and whitespace. `FuzzJobs` keeps the reader total:
anything it accepts renders back as the input it was given.
