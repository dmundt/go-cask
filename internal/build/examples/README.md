---
type: Guide
title: examples (build engine) — go-cask
description: Example-runner selection — which programs run, with what, and which never runs.
version: v3
---

# examples

Selecting the example programs a runner executes.

`Example` = directory; store subdirectory opened inside the run's scratch root; the
arguments that make it complete rather than block; for a manual example, the commands a
reader starts by hand. `Runnable` splits the table into `Automated` and `Manual`.

`Select`: the caller's names, in the given order; every automated example when no name
given. Unknown name → error. Manual example → refused with its start commands; a runner has
nothing else to say about a two-process pair.

Table = caller's: which examples exist, what each needs, which are manual. Same for the
scratch root the `Store` field resolves against, and the `go run` invocation that executes a
selected example.

## Testing

`go test ./examples/` — automated/manual split, selection order, unknown name, refusal
carrying the manual commands.
