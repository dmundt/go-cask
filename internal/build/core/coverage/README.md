---
type: Guide
title: coverage (build engine) — go-cask
description: Coverage policy data — thresholds, exemptions, pass/fail decision.
version: v2
---

# coverage

Holds a coverage policy; decides whether measurements satisfy it.

Policy = data: **targets** (threshold + package + tier name per row) plus an
**exemptions** register — packages deliberately gated by nothing, each with a written
reason. `Policy.Validate` checks the table's own shape: a malformed row would otherwise
read as "no threshold" → its package drops out of the gate.

## Two decisions

**Is the list complete?** `Policy.Uncovered` compares caller-discovered packages against
the table; returns those named in neither. Drift check: a package the build reports but
the policy never mentions is measured by nothing; silence would read as compliance.

`StripModule` → Go import paths reduced to the module-relative form the table uses. A
discovered package outside the module = error, not a silent omission; dropping it would
let a whole tree escape the check.

**Did each package pass?** `Result` = one measurement: threshold, package, coverage
reported, or `-1` when the run produced no coverage line at all. Zero and *no
measurement* are different failures: zero → the tests covered nothing; `-1` → the
measurement never happened. `CheckResults` → one message per failure; `ParseResult` reads
the `threshold|package|measured` line a gate writes.

## The table is the caller's

No shipped policy; no `Default()`: a host repository builds its own `Policy` and passes
it in → importing the engine cannot inherit another project's thresholds.

```go
policy := coverage.Policy{Targets: []coverage.Target{
    {Threshold: 90, Package: "core", Tier: "core"},
}}

missing, err := policy.Uncovered(discovered)
failures := coverage.CheckResults(results)
```

## Output and testing

`Target`, `Exemption` and `Result` = plain values; failures come back as strings ready
for a log. `go test ./coverage/` — the parser, the threshold boundary (a measurement
exactly on the threshold passes), the malformed-policy errors.
