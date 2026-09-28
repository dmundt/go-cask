---
type: Guide
title: examples (build engine) — go-cask
description: Example-runner selection — which programs run, with what, and which never runs.
version: v4
---

# examples

`Example` = directory; the store subdirectory opened inside the run's scratch root; the
arguments that make it complete rather than block; for a manual example, the commands a
reader starts by hand. `Runnable` splits the table into `Automated` and `Manual`.

`Select`: the caller's names, in the given order; every automated example when no name is
given. Unknown name → error. Manual example → refused with its start commands.

Caller's too: the table (which examples exist, what each needs, which are manual), the
scratch root `Store` resolves against, the `go run` invocation that executes a selection.

## Testing

`go test ./examples/` — automated/manual split, selection order, unknown name, refusal
carrying the manual commands.
