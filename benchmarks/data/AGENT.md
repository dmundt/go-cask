---
type: Agent Instructions
title: Agent instructions — `benchmarks/data`
description: The rules for benchmarks/data — the canonical benchmark JSON is the source of truth, its schema lives beside it, and runner metadata stays intact so results remain machine-specific and traceable.
version: v2
---

# Agent instructions — `benchmarks/data`

Folder stores canonical benchmark JSON files and schema used to validate them.

## Rules

- Treat JSON file as source of truth for benchmark results.
- Keep schema in same directory as JSON result file it validates.
- Preserve exact field names and value shapes already used by benchmark runner.
- Benchmark matrix changes: update JSON first, then schema and any README summary that depends on it.
- Keep runner metadata (`generated_at`, `runner`, `statistic`): comparisons remain machine-specific and traceable.
- Never rename benchmark file without updating schema ID and references in benchmark docs.

## Canonical file pair

- `store-codec-hash-roundtrip.json`
- `store-codec-hash-roundtrip.schema.json`

## Validation

Use JSON schema validator or simple parser check after any change to benchmark data.

## Signed pull-request workflow

Repository policy requires signed commits: rebuild PR branches locally from
current `main`; never use GitHub's server-side rebase or update-branch operation.
Apply changes with `git cherry-pick -S`, verify every head commit with
`git verify-commit`, push with `git push --force-with-lease`. Enable
auto-merge only after signature verification and required checks pass.
