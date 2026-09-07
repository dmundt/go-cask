---
type: Agent Instructions
title: AGENT — go-cask (docs/design/)
description: This file governs docs/design/ — non-normative design docs (core-overview pointer, viewer-brief, mockups). Follows the conventions in docs/AGENT.md.
version: v1
tags: [go-cask]
status: stable
---

# AGENT — go-cask (docs/design/)

> This file governs **`docs/design/`** — non-normative design artifacts:
> the core-overview pointer doc, the viewer design brief, the object-browser
> design JSON, and the viewer HTML mockup. Instruction specs live in
> `docs/specs/` (governed by `docs/specs/AGENT.md`); this file
> covers everything else in `docs/design/`.
>
> The repo-root `AGENTS.md` is the entry point for AI agents. Before any
> change, read [`docs/index.md`](../index.md) first.

---

## 1. File Naming

- Lowercase kebab-case for `.md` files: `core-overview.md`, `viewer-brief.md`.
- Vendor file names preserved: `go-cask-object-browser.design.json`,
  `go-cask-viewer.html`.
- No `.instructions.md` suffix (that convention is for the spec folder).

---

## 2. Frontmatter (required)

Every `.md` file in `docs/design/` MUST begin with YAML frontmatter, exactly
three keys:

```yaml
---
title: <Title> — go-cask
description: One sentence stating the document's purpose.
version: v1
---
```

- `version` is a simple marker (`v1`, `v2`, …). Bump by one on material
  change. Cosmetic fixes do NOT bump.
- HTML and JSON files have no frontmatter requirement; they are display
  and reference artifacts.

---

## 3. Cross-Referencing

- Refer to instruction specs by their `docs/specs/` paths.
- Refer to sibling design docs by relative path (`./viewer-brief.md`).
- Reference the rule index as the entry point: `docs/index.md`.

---

## 4. Maintenance

- These docs are **non-normative** — they inform but never override the
  instruction specs. If a conflict arises, the instruction spec wins
  (AGENTS.md §8).
- When the viewer-brief's outcomes are folded back into the viewer specs
  (viewer-design.md, viewer-security.md), delete the brief per AGENT.md
  §1: prefer extending an existing file over keeping a parallel one.
- `docs/design/` entries are listed in `docs/design/core-overview.md` and
  `docs/design/viewer-brief.md`; no section‑10‑style inventory needed.
