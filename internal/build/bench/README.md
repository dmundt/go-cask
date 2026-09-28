---
type: Guide
title: bench (build engine) — go-cask
description: Capture naming and baseline choice for the benchmark helpers.
version: v3
---

# bench

- `Stamp` → UTC capture stamp `20060102-150405`; sorts chronologically as text → a directory
  listing reads as a history.
- `UniqueName` → first free path: `<base><ext>`, then `-1`, `-2`, …; caller supplies the "is
  it taken?" test → two runs in one second cannot overwrite each other.
- `Baseline` = comparison point: caller-named capture → canonical dump when it exists →
  newest archived capture; false when nothing to compare against.
- A caller-named capture is returned as given even when absent — never silently replaced by
  a fallback; reporting it is the caller's job.
- `Newest` orders an archive listing by write time; tie broken by path → independent of the
  order the directory was listed in.
- A compare-only capture never writes the reference; a deliberate refresh archives the
  previous reference before replacing it.

## Testing

`go test ./bench/` — UTC stamp, uniqueness counting, archive order incl. a tie, every
baseline fallback.
