---
type: Agent Instructions
title: Agent instructions — `benchmarks/data`
description: The rules for benchmarks/data — the canonical benchmark JSON is the source of truth, its schema lives beside it, and runner metadata stays intact so results remain machine-specific and traceable.
version: v3
---

# Agent instructions — `benchmarks/data`

Canonical benchmark JSON + the schema validating it.

## Rules

- JSON = source of truth for benchmark results.
- Schema in the same directory as the JSON result file it validates.
- Field names and value shapes already used by the benchmark runner: exact, preserved.
- Matrix change: JSON first, then schema, then any dependent README summary.
- Runner metadata (`generated_at`, `runner`, `statistic`): keep; comparisons stay machine-specific and traceable.
- Never rename a benchmark file without updating the schema ID and the benchmark-doc references.

## Canonical file pair

- `store-codec-hash-roundtrip.json`
- `store-codec-hash-roundtrip.schema.json`

## Validation

JSON schema validator or simple parser check after any change to benchmark data.

## Related

Signed-commit workflow (local rebuild from `main`, `git cherry-pick -S`, `git verify-commit`, `git push --force-with-lease`, no server-side rebase/update-branch, auto-merge after signatures and required checks): [`../../docs/specs/landing.md`](../../docs/specs/landing.md).
