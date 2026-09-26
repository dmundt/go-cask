---
type: Guide
title: release (build engine) — go-cask
description: Changelog section → GitHub release notes; publish guards.
version: v3
---

# release

Changelog section → GitHub release notes; guards deciding whether a tag may publish.

## The notes

- `Section` → a tag's changelog section: lines between its `## [<tag>]` heading and the next
  release heading; the heading itself is not body.
- Blank lines between two sections dropped: a command substitution strips them; keeping them
  → a triple newline before the compare link.
- `Reshape` → change groups `###` → `##` → they read as release-note sections.
- Promote a group heading only when the line is *exactly* the heading; a trailing space or a
  suffix → left alone.
- `Notes` → the reshaped body, a blank line, the `**Full Changelog**: <compare link>` line.
- `Build` → both steps from changelog text.
- `PreviousTag` → the previous release from a tag list already in Git's version-descending
  order; the ordering rule lives in the Git invocation, not here.

```go
notes, err := release.Build(changelogText, "v1.2.0", "v1.1.0")
```

## The publish guards

`ValidatePublish` → why a tag may not be published, or `nil`. Caller gathers `State` with
Git; this package decides. One message per guard (remedy differs):

- **dirty working tree** → notes may not describe the tagged tree;
- **tag absent** locally;
- **tag not at HEAD** → notes describe something superseded;
- **no `main`** locally;
- **tag unreachable from `main`** → a release of a branch.

Publishing = caller's: runs `gh`.

## Testing

`go test ./release/` — section boundary (stops at the next release), group promotion and its
strictness, the exact compare-link spelling, tag selection, every guard, a run against a
real changelog when one is present.
