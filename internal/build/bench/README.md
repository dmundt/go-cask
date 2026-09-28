---
type: Guide
title: bench (build engine) — go-cask
description: Capture naming and baseline choice for the benchmark helpers.
version: v2
---

# bench

Benchmark helper decisions: capture naming; which capture a fresh run compares against.

- `Stamp` → UTC capture stamp `20060102-150405`; sorts chronologically as text →
  a directory listing reads as a history.
- `UniqueName` → first free path: `<base><ext>`, then `-1`, `-2`, …; caller supplies
  the "is it taken?" test → two runs in one second cannot overwrite each other.
- `Baseline` = comparison point: caller-named capture → canonical dump when it exists →
  newest archived capture; false when nothing to compare against.
- Caller-named capture returned as given even when absent; never silently replaced by a
  fallback; reporting that is the caller's job.
- `Newest` orders an archive listing by write time; tie broken by path → the choice is
  independent of the order the directory was listed in.
- Ownership: a compare-only capture never writes the reference; a deliberate refresh
  archives the previous reference before replacing it.
- Paths, naming convention, `go test` invocation = one repository's answers; live
  outside this package.

## Testing

`go test ./bench/` — UTC stamp, uniqueness counting, archive order incl. a tie, every
baseline fallback.
