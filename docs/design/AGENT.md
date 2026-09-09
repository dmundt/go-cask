---
type: Agent Instructions
title: AGENT — go-cask (docs/design/)
description: This file governs docs/design/ — non-normative design docs (core-overview pointer, viewer-brief, mockups). Follows the conventions in docs/AGENT.md.
version: v2
tags: [go-cask]
status: stable
---

# AGENT — go-cask (docs/design/)

Governs `docs/design/` — non-normative design artifacts (core-overview pointer, viewer design brief, object-browser design JSON, viewer HTML mockup). Instruction specs live in `docs/specs/` (governed by `docs/specs/AGENT.md`). The repo-root `AGENTS.md` is the entry point. Before any change, read `docs/index.md` first.

## 1. File naming

- `.md` files lowercase kebab-case (`core-overview.md`, `viewer-brief.md`).
- Vendor file names preserved (`go-cask-object-browser.design.json`, `go-cask-viewer.html`).
- No `.instructions.md` suffix (that convention is for the spec folder).

## 2. Frontmatter (required)

Every `.md` file MUST begin with exactly three YAML keys:

```yaml
---
title: <Title> — go-cask
description: One sentence stating the document's purpose.
version: v1
---
```

- `version` is a simple marker; bump by one on material change, not on cosmetic fixes.
- HTML and JSON files have no frontmatter requirement — they are display/reference artifacts.

## 3. Cross-referencing

- Instruction specs by their `docs/specs/` paths; sibling design docs by relative path (`./viewer-brief.md`); `docs/index.md` as the entry point.

## 4. Maintenance

- These docs are **non-normative** — they inform but never override the instruction specs; on conflict the instruction spec wins (AGENTS.md §8).
- When the viewer-brief's outcomes are folded back into the viewer specs, delete the brief (prefer extending an existing file over a parallel one).
- `docs/design/` entries are listed in the design docs; no section-10-style inventory needed.
