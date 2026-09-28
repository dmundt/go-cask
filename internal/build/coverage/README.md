---
type: Guide
title: coverage (build engine) — go-cask
description: Coverage policy data — thresholds, exemptions, pass/fail decision.
version: v4
---

# coverage

Policy = **targets** (threshold + package + tier name per row) plus an **exemptions**
register — packages deliberately gated by nothing, each with a written reason.
`Policy.Validate` checks the table's shape: a malformed row would read as "no threshold", so
its package drops out of the gate.

## Two decisions

**Is the list complete?** `Policy.Uncovered` compares caller-discovered packages against the
table and returns those named in neither. A package the build reports but the policy never
mentions is measured by nothing; silence would read as compliance.

`StripModule` → Go import paths reduced to the module-relative form the table uses. A
discovered package outside the module is an error, not a silent omission; dropping it would
let a whole tree escape the check.

**Did each package pass?** `Result` = one measurement: threshold, package, coverage reported,
or `-1` when the run produced no number. Zero and *no measurement* are different failures:
zero → the tests covered nothing; `-1` → the measurement never happened. `CheckResults` → one
message per failure; `ParseResult` reads the `threshold|package|measured` line a gate writes.

`Policy.Measure` reads the run's own record instead: a `go test -coverprofile` profile, whose
lines are `"<file>:<span> <statements> <count>"`. One test pass therefore answers for every
tier. The file's directory, module path stripped, is the package a block belongs to; a file
outside the module is not this policy's. The percentage is rounded to the one decimal
`go test -cover` prints, so the number printed and the number judged cannot disagree. A
profile that is not a profile, or one whose paths carry no block inside the module, is an
error rather than a run that measured nothing.

## The table is the caller's

No shipped policy, no `Default()` — a host repository builds its own `Policy` and passes it
in.

```go
policy := coverage.Policy{Targets: []coverage.Target{
    {Threshold: 90, Package: "core", Tier: "core"},
}}

missing, err := policy.Uncovered(discovered)
failures := coverage.CheckResults(results)
```

## Output and testing

`Target`, `Exemption` and `Result` = plain values; failures come back as strings ready for a
log. `go test ./coverage/` — both readers (the measurement line and the profile, the second
fuzzed through the first's target), the threshold boundary (a measurement exactly on the
threshold passes), the rounding the suite prints, and the malformed-policy errors.
