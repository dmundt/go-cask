---
type: Guide
title: verify (build engine) — go-cask
description: The gate run's own decisions — what a run covers, how many packages it builds at once, and whether an escape hatch dropped a step.
version: v2
---

# verify

The gate run's own decisions, split from the run itself: what a run **covers**, how many
packages it may build at once, whether an escape hatch **dropped a step**.

Reads no environment, runs nothing. Caller supplies variable names and values, so nothing
here names a repository, a path or a command — that is what keeps the package reusable and
lets these decisions be tested without a process.

## The run's record is the point

The gate writes a record for the commit it verified; the pre-push hook believes that record
without asking anything else. So every comparison that could make a half-run look complete
lives here, tested, not beside the printing:

- `Escape(value, dropAll)` — did an escape hatch drop a step. Only the exact value the gate
  reads as set counts; the drop-everything switch wins whatever the individual value says.
  A hatch reading as set for the report and unset for the record is exactly how a half-run
  acquires a green stamp.

## The scope

`Requested(value, variable, docsOnly)` → `Full` or `Docs`.

Empty value = automatic scope: the change set decides. An **asserted** documentation scope
fails when the change is not documentation-only, so a caller that asked for the cheap scope
can never be handed a green run for a code change. A scope that is neither is refused, not
defaulted — silently running the whole gate would hide the caller's typo behind minutes of
work. `Scope.String()` renders the two spellings, one of which is written into the ledger.

## The concurrency

`Jobs(value, variable, fallback)` → a positive decimal integer, or the caller's fallback
when nothing is set.

Syntax is checked here, not handed to `strconv`, which accepts `" 8"`, `"+8"` and `"08"`.
A concurrency the caller did not mean is worse than the default: the number reaches how many
packages are built and tested at once, so a value that silently became zero or one would
turn the gate's longest step serial.

## Testing

`go test ./verify/` — every scope value with and without a documentation-only change, both
scope spellings, the concurrency reader across the shapes `strconv` would have accepted,
the escape comparison including casing and whitespace. `FuzzJobs` keeps the reader total:
anything it accepts renders back as the input it was given.
