---
type: Guide
title: taskstate — build core
description: Which branches carry work no pull request tracks — the record the report is built from, and the two findings worth acting on.
version: v2
---

# taskstate

Which branches carry work that no pull request tracks.

## What it decides

Two states, and nothing else:

- **carries commits, no pull request** — commits the base ref does not have and no open pull
  request to land them. This is the state that loses work: nothing on the forge knows the
  commits exist.
- **pull request finished, branch still here** — the pull request merged, so its content is on
  the base ref and the local branch is debris.

A branch with an **open** pull request is never a finding: the pull request is the lease, and it
is what makes the work visible. A branch with **nothing ahead** of the base ref is not one
either. A pull request in a state this build does not know is reported as unlanded rather than
as finished — a report that stays quiet about work it does not understand is the failure the
package exists to prevent.

## Shape

Rule only; the caller reads the world.

```go
type Branch struct {
	Name, Head          string
	Ahead, Behind       int
	Dirty, Worktree     bool
	PullRequest         *PullRequest // nil when no pull request names the branch
}

func Findings(branches []Branch) []Finding
```

`internal/build/policy` carries go-cask's answers (the base ref, the listing limit); the command
`cmd/buildtool task-status` runs the git and forge calls and renders the table.

## Testing

```go
(cd internal/build && go test ./taskstate/)
```

No fuzz target: the package parses nothing. Reading `git` and `gh` output is the command's, and
that is where a parser would earn one.

## See also

- [`../README.md`](../README.md) — the engine and its packages
- [`../../policy/README.md`](../../policy/README.md) — go-cask's tables
- [`../../../../docs/specs/landing.md`](../../../../docs/specs/landing.md) — the lane the report serves
