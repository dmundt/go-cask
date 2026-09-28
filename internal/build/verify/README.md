---
type: Guide
title: verify (build engine) — go-cask
description: The gate run's own decisions — what a run covers, how many packages it builds at once, and whether an escape hatch dropped a step.
version: v1
---

# verify

The gate run's own decisions, separated from the run itself: what a run **covers**, how
many packages it may build at once, and whether an escape hatch **dropped a step**.

It reads no environment and runs nothing. The caller supplies the variable names and their
values, so nothing here names a repository, a path or a command — which is what keeps the
package reusable, and what lets these decisions be tested without a process.

## The run's record is the point

The gate writes a record for the commit it verified, and the pre-push hook believes that
record without asking anything else. So every comparison that could make a half-run look
complete lives here, with tests, rather than beside the printing:

- `Escape(value, dropAll)` — whether an escape hatch dropped a step. Only the exact value
  the gate reads as set counts, and the drop-everything switch wins whatever the
  individual value says. A hatch that read as set for the report and unset for the record
  is precisely how a half-run acquires a green stamp.

## The scope

`Requested(value, variable, docsOnly)` → `Full` or `Docs`.

An empty value is the automatic scope: the change set decides. An **asserted**
documentation scope fails when the change is not documentation-only, so a caller that
asked for the cheap scope can never be handed a green run for a code change; and a scope
that is neither is refused rather than defaulted, because silently running the whole gate
would hide the caller's typo behind minutes of work. `Scope.String()` renders the two
spellings one of which is written into the ledger.

## The concurrency

`Jobs(value, variable, fallback)` → a positive decimal integer, or the caller's fallback
when nothing is set.

The syntax is checked here rather than handed to `strconv`, which accepts `" 8"`, `"+8"`
and `"08"`. A concurrency the caller did not mean is worse than the default: the number
reaches how many packages are built and tested at once, so a value that silently became
zero or one would turn the gate's longest step serial.

## Testing

`go test ./verify/` — every scope value with and without a documentation-only change, both
scope spellings, the concurrency reader across the shapes `strconv` would have accepted,
and the escape comparison including its casing and whitespace. `FuzzJobs` keeps the reader
total: anything it accepts renders back as the input it was given.
