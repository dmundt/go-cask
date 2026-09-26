---
type: Agent Instructions
title: AGENT — go-cask (docs/ folder)
description: Meta-guide for the docs/ folder — OKF format, file naming, versioning, trimming rules, and the maintenance checklist. This file governs docs/index.md, docs/specs/, docs/design/, and any future subdirectories. (The benchmark guide lives at benchmarks/README.md.) The root AGENTS.md is the entry point for AI agents; this file governs the docs subtree after that.
version: v20
---

# AGENT — go-cask (docs/ folder)

Governs all non-instruction docs in `docs/`. Instruction specs under `docs/specs/` have their own `AGENT.md`. **Before any change**, read `docs/index.md` first (path → spec table), then this file (or `docs/specs/AGENT.md`) for conventions.

## Table of contents

- [Format — OKF v0.2](#1-format--okf-v02)
- [Trimming](#2-trimming)
- [Adding a file](#3-adding-a-file)
- [Removing/renaming a file](#4-removingrenaming-a-file)
- [Versioning](#5-versioning)
- [Constructor naming](#6-constructor-naming-in-example-code)
- [Diagram and formatting rules](#7-diagram-and-formatting-rules)
- [Editing and maintenance checklist](#8-editing-and-maintenance-checklist)

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

`type` and `version` required. Other OKF keys (`sources`, `generated`,
`verified`, `stale_after`, `tags`, `status`) optional, MUST be used when
applicable.

Every `AGENT.md` in repository carries same four keys, not only ones under
`docs/`: package-local guide such as `cas/AGENT.md`, `scripts/AGENT.md`
or `benchmarks/data/AGENT.md` gets own `type`, `title` (identical to its
H1), one-line `description` and `version` for exactly the reason a spec does —
so a reader can tell how current the instructions are. Keys = ones the
table above lists; no package-local guide adds a key of its own.

### 1.2 Type values and locations

`Specification` → every `docs/specs/` file; `Design Document` → every `docs/design/` file; `Guide` → `benchmarks/README.md` and similar how-to; `Agent Instructions` → every `AGENT.md`, wherever it lives (root `AGENTS.md`, `docs/AGENT.md`, package-local guides under `cas/`, `cas/codec/`, `cas/verify/`, `benchmarks/`, `benchmarks/data/`, `examples/`, `scripts/`, `website/`, `.agents/` and `.github/`).

### 1.3 Index files

Every subdirectory MUST have an `index.md`. Root `docs/index.md` = top-level rule index; subdirectory indexes (`docs/design/index.md`, `docs/specs/index.md`) shorter. All index files carry `okf_version: "0.2"` and no `type`.

### 1.4 Markdown-only content

Raw HTML forbidden in every Markdown file, including HTML comments, tags,
layout wrappers, HTML/XML/SVG code fences. Use Markdown constructs, Mermaid,
or links instead. HTML belongs only in dedicated non-Markdown assets such as
viewer mockup; it MUST NOT be copied into a `.md` file.

## 2. Trimming

- Every doc must earn its bytes: replaceable-by-a-pointer → pointer; a section repeating another spec → remove and reference the canonical source.
- No cross-document duplication: each fact lives in one place (`defaults.md` or its owning spec), referenced not restated.
- **Mermaid diagrams are exempt** from trimming (visualize complex relationships, kept even when large).
- Deferred-feature sketches, historical rationales and single-run benchmark samples → remove, replace with a pointer to the deferral record.
- Keep the three directories: `docs/specs/` (normative, 21 files), `docs/design/` (non-normative), `benchmarks/README.md` (guide beside the benchmark code, outside `docs/`).

## 3. Adding a file

1. Create at correct `docs/` path. 2. Add frontmatter (`type`, `title`, `description`, `version`). 3. Add row to parent `index.md`. 4. If it introduces a new agent-matchable path pattern, add row to `docs/index.md`.

## 4. Removing/renaming a file

1. Remove/update its row in parent `index.md` and in `docs/index.md`. 2. Update all cross-references in other docs. 3. Bump `version` on every affected file.

## 5. Versioning

`version: v1, v2, …` — bump by one on material change; cosmetic fixes (typos/formatting) do NOT bump. `version` = custom key (OKF defines none) — mechanism for consumers to detect staleness.

## 6. Constructor naming (in example code)

Go examples in these docs MUST name constructors per `coding-guidelines.md` §1: plain `New()` when package exposes one primary type (`fs.New`, `backmem.New`, `json.New[T]`, `gob.NewRaw[T]`, `lru.New`); `NewType()`/`NewXyz()` for multiple important types or a non-primary constructed type (`cas.NewDigest`, `cas.NewWalker`, `cas.New`), with `prefetch.NewSmartCache` the frozen-surface exception that keeps its name (cas-core §7.1). When code and example diverge code wins — update the example (a non-compiling doc example is a defect).

## 7. Diagram and formatting rules

- Mermaid for relationships/flow; ASCII only alongside mermaid (raw views).
- Code fences always carry a language tag (`go`, `yaml`, `text`, `mermaid`).
- In prose, never use the ampersand as a substitute for `and` unless the text is code, a literal symbol, a diagram, or a mermaid block where syntax or the source domain requires the symbol.
- Line width ≤ ~100 chars; LF endings; UTF-8.

## 8. Editing and maintenance checklist

Before committing any change to a file in `docs/` (outside `docs/specs/`):
- [ ] OKF frontmatter present (`type`, `title`, `description`, `version`; `okf_version: "0.2"` for indexes) — on every `AGENT.md` too, not only files under `docs/` (§1.1)
- [x] `version` bumped on material change — enforced by `internal/build/core/versioning` from `verify.sh` (it detects a missing bump, not a cosmetic one)
- [ ] No duplication — check `defaults.md` and owning specs first
- [ ] Cross-references updated in ALL files mentioning the changed term
- [ ] Ampersand used only where required by code, literal symbol text, or diagram syntax
- [ ] On add/remove/rename, `docs/index.md` and the parent `index.md` updated
- [ ] Mermaid blocks balanced; all code fences tagged
- [ ] No raw HTML, HTML comments, or HTML/XML/SVG fences
- [ ] LF endings, UTF-8

The same item list applies, shape adapted to the artifact, when adding or
changing a repository skill under `.agents/skills/`; that directory's own
[`AGENT.md`](../.agents/AGENT.md) governs the OKF fields a skill replaces with
its two-key frontmatter, its size budget, its provenance requirement.

## 9. Signed pull-request workflow

Repository policy requires signed commits: rebuild PR branches locally from
current `main`; never use GitHub's server-side rebase or update-branch operation.
Apply changes with `git cherry-pick -S`, verify every head commit with
`git verify-commit`, push with `git push --force-with-lease`. Enable
auto-merge only after signature verification and required checks pass.
