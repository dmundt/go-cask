---
type: Guide
title: layers (build engine) — go-cask
description: Dependency-layer rule — which packages of a module may import which.
version: v3
---

# layers

Dependency-layer rule: which packages of a module may import which.

Caller states layers as a table of arms. Arm **owns** a package set; lists the module-local
prefixes its members may import. Module-local import not allowed = violation. Import from
outside the module (stdlib, dependency) → outside the rule, never reported.

## Table is the caller's

No shipped table. Build one from `Layer` + the three ownership constructors, which own the
matching semantics — the separator rule keeping a root like `…/cas` from claiming
`…/casket`:

```go
matrix := []layers.Layer{
    {Name: "core/", Owns: layers.OwnsTree(module + "/core"), Allowed: []string{module + "/core"}},
    {Name: "app/",  Owns: layers.OwnsAnyTree(module + "/app", module + "/tool"),
                    Allowed: []string{module + "/core", module + "/app"}},
}

for _, violation := range layers.Check(module, matrix, packages) {
    fmt.Fprintln(os.Stderr, violation)
}
```

`OwnsTree` = package + everything beneath; `OwnsExact` = one package; `OwnsAnyTree` =
several trees. First arm claiming a package owns it → order matters when trees nest.

## Input

`Package` = what `go list -f '{{.ImportPath}}|{{join .Imports " "}}' ./...` reports.
Production imports only: `go list` omits `_test.go`-only imports → a test may import what
its package may not.

## Output

`Check` → `Violation` values sorted by package then import → two runs log identically. Each
names the offending package, its layer, the breaking import. `Owner` → the arm claiming a
package; caller checks its table for exhaustiveness over the module's trees.

## Testing

`go test ./layers/` — the mechanism exercised against a table built in the test: a table is
caller policy, not engine material.
