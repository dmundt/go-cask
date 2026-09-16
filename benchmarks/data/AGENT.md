# Agent instructions — `benchmarks/data`

This folder stores the canonical benchmark JSON files and the schema used to validate them.

## Rules

- Treat the JSON file as the source of truth for benchmark results.
- Keep the schema in the same directory as the JSON result file it validates.
- Preserve the exact field names and value shapes already used by the benchmark runner.
- When a benchmark matrix changes, update the JSON first, then update the schema and any README summary that depends on it.
- Keep runner metadata (`generated_at`, `runner`, `statistic`) so comparisons remain machine-specific and traceable.
- Do not rename the benchmark file without updating the schema ID and any references in the benchmark docs.

## Canonical file pair

- `store-codec-hash-roundtrip.json`
- `store-codec-hash-roundtrip.schema.json`

## Validation

Use a JSON schema validator or a simple parser check after any change to the benchmark data.
