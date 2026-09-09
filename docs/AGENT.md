---
type: Agent Instructions
title: AGENT — go-cask (docs/ folder)
description: Meta-guide for the docs/ folder — OKF format, file naming, versioning, trimming rules, and the maintenance checklist. This file governs docs/index.md, docs/specs/, docs/design/, and any future subdirectories. (The benchmark guide lives at benchmark/README.md.) The root AGENTS.md is the entry point for AI agents; this file governs the docs subtree after that.
version: v6
---

# AGENT — go-cask (docs/ folder)

> This file governs all non-instruction docs in `docs/`. The instruction
> specs under `docs/specs/` have their own
> [`AGENT.md`](specs/AGENT.md). This file fills the gap for documents
> outside that folder.
>
> **Before any change**, read [`docs/index.md`](index.md) first — it maps
> path → spec file in one table. Then read this file (or
> `docs/specs/AGENT.md`) for detailed conventions.

---

## 1. Format — OKF v0.2

Every `.md` file in `docs/` MUST be a valid
[Open Knowledge Format (OKF) v0.2](https://github.com/GoogleCloudPlatform/open-knowledge-format)
concept document.

### 1.1 Frontmatter rules

| Field | When | Example |
|---|---|---|
| `type:` | Every non-index file | `type: Specification`, `type: Design Document`, `type: Guide`, `type: Agent Instructions` |
| `okf_version: "0.2"` | Only `index.md` files (root + subdirectory) | `okf_version: "0.2"` |
| `title:` | Always | `title: CAS Core — go-cask` |
| `description:` | Always (single line) | `description: Core library specification for go-cask.` |
| `version:` | Always (our custom key) | `version: v1` |
| `tags:` | Optional | `tags: [go-cask]` |
| `status:` | Optional — stable \| draft \| deprecated | `status: stable` |

Both `type` and `version` are required. All other OKF standard keys
(`sources`, `generated`, `verified`, `stale_after`, `tags`, `status`) are
optional but MUST be used when applicable.

### 1.2 Type values and their locations

| Type | Applies to |
|---|---|
| `Specification` | Every file in `docs/specs/` |
| `Design Document` | Every file in `docs/design/` |
| `Guide` | `benchmark/README.md` and similar how-to files |
| `Agent Instructions` | Any `AGENT.md` file |

### 1.3 Index files

Every subdirectory MUST have an `index.md` (OKF progressive disclosure).
Root `docs/index.md` is the top-level rule index. Subdirectory index files
(`docs/design/index.md`, `docs/specs/index.md`) are shorter. All index files carry
`okf_version: "0.2"` in frontmatter and no `type`.

---

## 2. Trimming — keep minimal, never duplicate

- **Every doc must earn its bytes.** If a doc can be replaced by a pointer
  to another doc, replace it. If a section repeats information already in
  another spec, remove it and reference the canonical source.
- **Cross-document duplication must be eliminated.** Each fact lives in one
  place (typically `defaults.md` or the owning spec) and is referenced, not
  restated. If you find the same number or rule in two files, remove one
  and add a link.
- **Mermaid diagrams are exempt from trimming.** They visualize complex
  relationships and are kept even when large. Their token cost is justified.
- **Dead code-style sections** (design sketches for deferred features,
  historical rationales, single-run benchmark samples) should be removed
  and replaced with a brief pointer to the deferral record.
- **Keep the three directory structure:**
  - `docs/specs/` — normative specs (20 files)
  - `docs/design/` — non-normative design documents
  - `benchmark/README.md` — the benchmark/performance guide (lives beside the benchmark code, outside `docs/`)

---

## 3. Adding a new file

1. Create the file at the correct path under `docs/`.
2. Add OKF frontmatter with at minimum `type`, `title`, `description`,
   `version`.
3. Add a row to the parent directory's `index.md`.
4. If the file introduces a new path pattern that agents should match,
   add a row to `docs/index.md`.

---

## 4. Removing or renaming a file

1. Remove or update its row in the parent directory's `index.md`.
2. Remove or update its row in `docs/index.md`.
3. Update all cross-references in other docs.
4. Bump `version` on every affected file.

---

## 5. Versioning

- `version: v1`, `v2`, … — bump by one on material changes.
- Cosmetic fixes (typos, formatting) do NOT bump.
- The `version` field is our custom key; OKF defines no required version
  field. We keep it because it is the mechanism for consumers to detect
  staleness.

---

## 6. Constructor Naming (when writing example code)

Any Go example embedded in these docs must name constructors correctly,
per `docs/specs/coding-guidelines.md` §1 "Constructors":

- Use plain `New()` when the package exposes **one** primary type:
  `fs.New`, `mem.New`, `json.New[T]`, `gob.New[T]`, `lru.New`.
- Use `NewType()` / `NewXyz()` when the package exposes **multiple**
  important types, or the constructed type is not the package's primary one:
  `cas.NewHash`, `cas.NewWalker`, `prefetch.NewSmartCache`, `cas.New`.

When the codebase diverges from this, the code wins — update the example to
match the actual identifier (a doc example that won't compile is a defect).

---

## 7. Diagram & Formatting Rules

- Mermaid for relationships/flow, ASCII only alongside mermaid (raw views).
- Code fences always carry a language tag: `go`, `yaml`, `text`, `mermaid`.
- Line width ≤ ~100 chars; LF endings; UTF-8.

---

## 8. Editing & Maintenance Checklist

Before committing any change to a file in `docs/` (outside `docs/specs/`):

- [ ] OKF frontmatter present (`type`, `title`, `description`, `version` for
      concept docs; `okf_version: "0.2"` for index files)
- [ ] `version` bumped on material change
- [ ] Duplication avoided — check `defaults.md` and owning specs first
- [ ] Cross-references updated in ALL files that mention the changed term
- [ ] If a file is added/removed/renamed, `docs/index.md` and the parent
      `index.md` updated
- [ ] Mermaid blocks balanced (openers == closers); all code fences tagged
- [ ] LF endings, UTF-8