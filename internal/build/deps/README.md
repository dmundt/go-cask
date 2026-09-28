---
type: Guide
title: deps (build engine) — go-cask
description: Two dependency rules — forbidden transitive dependency; well-formed module graph.
version: v2
---

# deps

Two rules about what a package may depend on.

## A package must not reach a forbidden dependency

`CodecGuard` = a package that must stay free of a forbidden prefix + the remedy to quote
when it does not. `CheckCodecDeps` takes the guards and, per guarded package, the
transitive dependency list `go list -deps` reports; returns a `CodecViolation` per
package that reaches the layer.

Path-boundary matching → a sibling whose name merely starts with the forbidden prefix is
not the layer. One violation per guarded package however many dependencies match; result
sorted by package → two runs log identically.

Guards = the caller's; the engine ships none. `ForbiddenPath` = the one constant a caller
changes for a different boundary.

```go
violations := deps.CheckCodecDeps(guards, depsByPackage)
```

## The module graph must name this module

`CheckModuleGraph` → whether `go list -m -json all` produced a graph whose **main**
module is the expected one. The main-module requirement matters: a graph in which the
module appears as a *requirement* describes a build of something else; a bare containment
test would accept it. Guards a gate that writes that command's output to a file and reads
it back: an empty or malformed graph would otherwise read as "no modules to check" and
pass silently.

## Testing

`go test ./deps/` — both directions of the guard, the path boundary (`cas/codecbase` is
not `cas/codec`), the one-violation-per-package rule, and the module-graph cases
including a dependency-only appearance.
