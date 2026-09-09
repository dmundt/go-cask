---
type: Agent Instructions
title: AGENT — go-cask (docs/ folder)
description: Meta-guide for the docs/ folder — OKF format, file naming, versioning, trimming rules, and the maintenance checklist. This file governs docs/index.md, docs/specs/, docs/design/, and any future subdirectories. (The benchmark guide lives at benchmark/README.md.) The root AGENTS.md is the entry point for AI agents; this file governs the docs subtree after that.
version: v6
---

# AGENT — go-cask (docs/ folder)

Governs all non-instruction docs in `docs/`. The instruction specs under `docs/specs/` have their own `AGENT.md`. **Before any change**, read `docs/index.md` first (path → spec table), then this file (or `docs/specs/AGENT.md`) for detailed conventions.

## 1. Format — OKF v0.2

Every `.md` file in `docs/` MUST be a valid OKF v0.2 concept document.

### 1.1 Frontmatter

| Field | When | Example |
|---|---|---|
| `type:` | Every non-index file | `Specification`/`Design Document`/`Guide`/`Agent Instructions` |
| `okf_version: "0.2"` | Only `index.md` files | |
| `title:` | Always | |
| `description:` | Always (single line) | |
| `version:` | Always (custom key) | `v1` |
| `tags:`/`status:` | Optional (`stable`/`draft`/`deprecated`) | |

`type` and `version` are required. Other OKF keys (`sources`, `generated`, `verified`, `stale_after`, `tags`, `status`) optional but MUST be used when applicable.

### 1.2 Type values & locations

`Specification` → every `docs/specs/` file; `Design Document` → every `docs/design/` file; `Guide` → `benchmark/README.md` and similar how-to; `Agent Instructions` → any `AGENT.md`.

### 1.3 Index files

Every subdirectory MUST have an `index.md`. Root `docs/index.md` is the top-level rule index; subdirectory indexes (`docs/design/index.md`, `docs/specs/index.md`) are shorter. All index files carry `okf_version: "0.2"` and no `type`.

## 2. Trimming

- Every doc must earn its bytes: replaceable-by-a-pointer → pointer; a section repeating another spec → remove + reference the canonical source.
- Eliminate cross-document duplication: each fact lives in one place (`defaults.md` or its owning spec), referenced not restated.
- **Mermaid diagrams are exempt** from trimming (they visualize complex relationships; kept even when large).
- Dead code-style sections (deferred-feature sketches, historical rationales, single-run benchmark samples) → remove, replace with a pointer to the deferral record.
- Keep the three-directory structure: `docs/specs/` (normative, 20 files), `docs/design/` (non-normative), `benchmark/README.md` (guide beside the benchmark code, outside `docs/`).

## 3. Adding a file

1. Create at the correct `docs/` path. 2. Add frontmatter (`type`, `title`, `description`, `version`). 3. Add a row to the parent `index.md`. 4. If it introduces a new agent-matchable path pattern, add a row to `docs/index.md`.

## 4. Removing/renaming a file

1. Remove/update its row in the parent `index.md` and in `docs/index.md`. 2. Update all cross-references in other docs. 3. Bump `version` on every affected file.

## 5. Versioning

`version: v1, v2, …` — bump by one on material change; cosmetic fixes (typos/formatting) do NOT bump. The `version` field is our custom key (OKF defines none) — it is the mechanism for consumers to detect staleness.

## 6. Constructor naming (in example code)

Go examples in these docs MUST name constructors per `coding-guidelines.md` §1: plain `New()` when the package exposes one primary type (`fs.New`, `mem.New`, `json.New[T]`, `gob.New[T]`, `lru.New`); `NewType()`/`NewXyz()` for multiple important types or a non-primary constructed type (`cas.NewHash`, `cas.NewWalker`, `prefetch.NewSmartCache`, `cas.New`). When the codebase diverges, the code wins — update the example (a non-compiling doc example is a defect).

## 7. Diagram & formatting rules

- Mermaid for relationships/flow; ASCII only alongside mermaid (raw views).
- Code fences always carry a language tag (`go`, `yaml`, `text`, `mermaid`).
- Line width ≤ ~100 chars; LF endings; UTF-8.

## 8. Editing & maintenance checklist

Before committing any change to a file in `docs/` (outside `docs/specs/`):
- [ ] OKF frontmatter present (`type`, `title`, `description`, `version`; `okf_version: "0.2"` for indexes)
- [ ] `version` bumped on material change
- [ ] No duplication — check `defaults.md` and owning specs first
- [ ] Cross-references updated in ALL files mentioning the changed term
- [ ] On add/remove/rename, `docs/index.md` and the parent `index.md` updated
- [ ] Mermaid blocks balanced; all code fences tagged
- [ ] LF endings, UTF-8
