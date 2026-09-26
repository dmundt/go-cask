---
type: Guide
title: shell (build) — go-cask
description: Shell scripts the build engine replaced; archived at parity, never executed.
version: v4
---

# shell

Scripts the engine replaced; archived at parity.

## What belongs here

Landing conditions — all three:

- rule = tested function in [`../core`](../core/README.md);
- go-cask's answer = table in [`../policy`](../policy/README.md);
- every caller moved to `go run ./cmd/buildtool`.

History, not fallback: nothing runs the copy. Behaviour only the copy has = gap in the
port, not a reason to call it. Unreferenced script → deleted, not archived; an orphan
records nothing. [git history](https://github.com/dmundt/go-cask) keeps either way. Each
copy's header names the replacing command.

## The archive

| Script | Replaced by | Rule it enforced |
|---|---|---|
| `docs-only.sh` | `go run ./cmd/buildtool scope` | is a Git diff documentation only — gate scope decision, CI scope job |
| `security.sh` | `go run ./cmd/buildtool security` | scan uses the pinned `govulncheck` release, wherever the binary lands |
| `bench-baseline.sh` | `go run ./cmd/buildtool bench-baseline` | one deliberate run owns the committed reference dump; archives the previous one first |
| `bench-compare.sh` | `go run ./cmd/buildtool bench-compare` | baseline chosen before capture; never writes the reference |
| `test-bench-scripts.sh` | `cmd/buildtool/bench_test.go` | the cases above, against stubbed binaries in throwaway repositories |
| `run-examples.sh` | `go run ./cmd/buildtool run-examples` | which examples terminate on their own, with which arguments; which one never runs |
| `land-lane.sh` | `go run ./cmd/buildtool land-lane` | who holds the local advisory slot; idle time; who may take it over |
| `test-land-lane.sh` | `cmd/buildtool/landlane_test.go`, `cmd/buildtool/prepush_test.go`, `internal/build/core/gate` | atomic claim, idle deadline, takeover record, gate stamp as a ledger |
| `pr-lane.sh` | `go run ./cmd/buildtool pr-lane` | who holds the server-side lane: compare-and-swap on `refs/lane/<NNN>`, the open pull request as the lease, the claim window, deliberate release |
| `test-pr-lane.sh` | `cmd/buildtool/prlane_test.go` | the cases above, against a fake remote whose ref create is atomic under a mutex |
| `worktree.sh` | `go run ./cmd/buildtool worktree` | worktree `.git` link relative, resolving to its own admin directory; registration locked against `git worktree prune` |

## Adding a script

1. Port the rule: decision → `../core` package + table test; patterns/thresholds →
   `../policy`; world-reading → `cmd/buildtool` subcommand.
2. Move every caller: gate, CI, other scripts.
3. Move the script here; header names the replacement; add its table row.

## Testing

Gate runs nothing here. Rules tested where they now live:

```bash
(cd internal/build/core && go test ./...)
go test ./internal/build/policy/
```
