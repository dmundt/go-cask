---
type: Agent Instructions
title: AGENT — go-cask
description: Meta-guide for the docs/ folder — file naming, frontmatter conventions, versioning, and the maintenance checklist that keeps every document in this tree consistent. This file governs docs/index.md, docs/instructions/, docs/design/, docs/performance/, and any future subdirectories.
version: v2
---

# AGENT — go-cask (docs/ folder)

> This file governs **all non-instruction docs in `docs/`**: `docs/index.md`,
> `docs/design/`, `docs/performance/`, and any future subdirectories.
> The instruction specs under `docs/instructions/` are governed by their own
> [`AGENT.md`](instructions/AGENT.md). This file fills the gap for documents
> outside that folder.
>
> **To reduce token usage, consult the hierarchical index first.**
> 1. Start at [`docs/index.md`](index.md) — the top-level rule index maps
>    any path to its rule file.
> 2. For design docs, open [`docs/design/index.md`](design/index.md).
> 3. For instruction specs, open [`docs/instructions/index.md`](instructions/index.md).
> 4. For performance/benchmark docs, open [`docs/performance/index.md`](performance/index.md).
> 5. Only after matching your path should you open the detailed spec file.

---

## 1. File Naming

- **Top-level docs** at `docs/*.md` use lowercase kebab-case:
  `index.md`, `benchmarks.md`, etc.
- **Design docs** in `docs/design/` use lowercase kebab-case:
  `core-overview.md`, `viewer-brief.md`, `go-cask-viewer.html`.
- Instruction specs in `docs/instructions/` follow the naming rules in
  `docs/instructions/AGENT.md` §2.

---

## 2. Frontmatter (required)

Every `.md` file in `docs/` (outside `docs/instructions/`) MUST begin with
YAML frontmatter, exactly three keys:

```yaml
---
title: <Title> — go-cask
description: One sentence describing the document's purpose.
version: v2
---
```

- `version` is a simple marker (`v1`, `v2`, …). Bump by one whenever the
  file is materially extended or changed. Cosmetic fixes (typos, formatting)
  do NOT bump the version.
- The `description` is a single line: imperative, mentions the key
  components.

---

## 3. Cross-Referencing

- Refer to sibling files by relative path in backticks:
  `[design/index.md](design/some-doc.md)`.
- Reference the rule index as the entry point: see `docs/index.md`.
- When a change affects a documented convention, update **all** files that
  reference it in one pass.

---

## 4. Diagram & Formatting Rules

- Mermaid for relationships/flow, ASCII only alongside mermaid (raw views).
- Code fences always carry a language tag: `go`, `yaml`, `text`, `mermaid`.
- Line width ≤ ~100 chars; LF endings; UTF-8.

---

## 5. Editing & Maintenance Checklist

Before committing any change to a file in `docs/` (outside `docs/instructions/`):

- [ ] Frontmatter present, `title` == H1, one-line `description`
- [ ] `version` bumped by one when the change is material (§2)
- [ ] Cross-references updated in ALL files that mention the changed term
- [ ] Diagrams valid mermaid; fences tagged
- [ ] Every mermaid block balanced (openers == closers)
- [ ] `docs/index.md` updated when a rule file is added, removed, or its
      scope materially changes
