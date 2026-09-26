---
type: Guide
title: changes (build engine) — go-cask
description: Change-set classification — a rule's path coverage and the verdict it decides.
version: v2
---

# changes

Classifying a change set: which paths a rule covers, and what that decides.

- `Pattern` matches a changed path three ways: whole path (`Exact`); directory prefix
  plus its separator (`Prefix`); trailing extension (`Suffix`). Exactly one field set
  per pattern; an empty pattern matches nothing, not everything.
- `Rule` = one classification + how it reads the set:
  - `Any` → holds when ≥1 path matches ("this change touches Go source");
  - `All` → holds when the set is non-empty and every path matches ("this change is
    documentation only").
- `All` fails on an empty set deliberately: "nothing changed" read as "documentation
  only" would silently shrink what a gate verifies.
- `Also` = earlier rules that imply a rule → "a security-relevant change touches Go
  **or** the CI configuration" without restating the Go patterns.
- `Classify` rejects a nameless rule, a duplicate name, an implication of a later rule
  and an unknown mode → a table cannot be circular; reads top to bottom.
- `Select` = the caller's split: paths a pattern list covered; paths it did not.
- No shipped pattern list, no shipped rule: which paths count as documentation, and
  which facts imply another, are one repository's decisions.

## Testing

`go test ./changes/` — each pattern form, the `Any`/`All` verdicts incl. the empty set,
the implication, every rejected table, the split.
