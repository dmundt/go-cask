---
type: Guide
title: deps (build engine) — go-cask
description: The module's import structure — forbidden dependencies, the layer matrix, and the committed package graph.
version: v4
---

# deps

One subject: what may import what. The transitive-dependency bans and codec
guards, the layer matrix, and the graph drawn from `go list`.

## deps — forbidden dependencies



### A package must not reach a forbidden dependency

`CodecGuard` = a package that must stay free of a forbidden prefix + the remedy to quote when
it does not. `CheckCodecDeps` takes the guards and, per guarded package, the transitive
dependency list `go list -deps` reports; returns a `CodecViolation` per package that reaches
the layer.

Path-boundary matching → a sibling whose name merely starts with the forbidden prefix is not
the layer. One violation per guarded package however many dependencies match; result sorted
by package → two runs log identically.

`ForbiddenPath` = the one constant a caller changes for a different boundary.

```go
violations := deps.CheckCodecDeps(guards, depsByPackage)
```

### The module graph must name this module

`CheckModuleGraph` → whether `go list -m -json all` produced a graph whose **main** module is
the expected one. The main-module requirement matters: a graph in which the module appears as
a *requirement* describes a build of something else, which a bare containment test would
accept. Guards a gate that writes that command's output to a file and reads it back: an empty
or malformed graph would otherwise read as "no modules to check" and pass silently.

### Testing

`go test ./deps/` — both directions of the guard, the path boundary (`cas/codecbase` is
not `cas/codec`), the one-violation-per-package rule, and the module-graph cases
including a dependency-only appearance.

## layers — the dependency-layer rule



Dependency-layer rule: which packages of a module may import which.

Caller states layers as a table of arms. An arm **owns** a package set and lists the
module-local prefixes its members may import. Module-local import not allowed = violation.
Import from outside the module (stdlib, dependency) → outside the rule, never reported.

### Table is the caller's

No shipped table. Build one from `Layer` + the three ownership constructors:

```go
matrix := []deps.Layer{
    {Name: "core/", Owns: deps.OwnsTree(module + "/core"), Allowed: []string{module + "/core"}},
    {Name: "app/",  Owns: deps.OwnsAnyTree(module + "/app", module + "/tool"),
                    Allowed: []string{module + "/core", module + "/app"}},
}

for _, violation := range deps.Check(module, matrix, packages) {
    fmt.Fprintln(os.Stderr, violation)
}
```

`OwnsTree` = package + everything beneath; `OwnsExact` = one package; `OwnsAnyTree` =
several trees. First arm claiming a package owns it → order matters when trees nest. A
constructor's separator rule keeps a root like `…/cas` from claiming `…/casket`.

### Input

`Package` = what `go list -f '{{.ImportPath}}|{{join .Imports " "}}' ./...` reports.
Production imports only: `go list` omits `_test.go`-only imports → a test may import what
its package may not.

### Output

`Check` → `Violation` values sorted by package then import → two runs log identically; each
names the offending package, its layer, the breaking import. `Owner` → the arm claiming a
package; caller checks its table for exhaustiveness over the module's trees.

### Testing

`go test ./layers/` — the mechanism exercised against a table built in the test: a table is
caller policy, not engine material.

## depgraph — the committed graph



### The graph

`Derive` takes the module path + the package list `go list` reports; returns three sorted
sets:

- **nodes** — module-relative package paths;
- **edges** — `from>to` pairs, one per local import;
- **leaves** — packages importing no local package; filled style, so a reader need not trace
  every arrow to find the bottom.

Local edges only: an import outside the module is not a node. An import target is a node even
when its own package was not listed → an edge always has both ends. Everything ordered by
byte value → the document is reproducible on any host.

### The document

`Document` renders the frontmatter, the caller's introduction, the diagram and the caller's
closing sections. Title, description, generator name, **subgraphs** and prose are the
caller's; this package ships none of it.

`Subgraph` = id + title + `Claims` predicate. **The first subgraph that claims a package owns
it** → the order is the rule; trees nest (`cas/backend/fs` inside `cas`), so a table must test
the narrower tree first and make each claim exclusive, else a package is drawn twice.

```go
doc := deps.Doc{Title: "Graph", Generator: "…", Subgraphs: []deps.Subgraph{
    {ID: "CORE", Title: "core", Claims: ownedBy("/core")},
}}
graph := deps.Derive(module, packages)
rendered := deps.Document(doc, graph, version)
```

`Version` reads a committed document's frontmatter version; `Bump` moves it by one →
re-render at the version already on disk (regeneration stays idempotent), and move the
version only when the body actually changed.

### Testing

`go test ./depgraph/` — node ids, derivation from a fixture package list, the version
arithmetic, and that the document carries the caller's prose verbatim.
