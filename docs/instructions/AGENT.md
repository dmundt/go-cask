---
title: AGENT — go-cask Instruction Folder Guide
description: The meta-guide for docs/instructions/ — file naming, frontmatter, document structure, normative language, shared terminology, cross-referencing, precedence, and the maintenance checklist that keeps every instruction file consistent.
version: v8
---

# AGENT — go-cask Instruction Folder Guide

> This file governs **the other files in this folder**. Every agent (Copilot,
> other AI tooling) and every human maintainer editing any
> `docs/instructions/*.md` MUST follow it, so the folder stays
> a single, coherent specification set rather than a pile of docs.
>
> Scope: this folder contains the normative specs of the go-cask project
> (core architecture, coding style, security, APIs, viewer design, examples,
> performance, testing, operations). The repo-root `AGENTS.md` is the agent
> aggregator that points at them; it is outside this folder and follows the
> same style rules where they apply.

---

## 1. Purpose & Scope

- The folder is the **single source of truth** for how go-cask is designed,
  built, secured, tested, and operated.
- Every file states **requirements** (what MUST/SHALL be true) and **context**
  (why), not prose about the project.
- New files are added only when a real gap exists (see §5 of
  `examples.md` for the example-generation analogue); prefer
  extending an existing file over creating a new one.

---

## 2. File Naming

- Pattern: `<Topic>.md` — one topic per file, where the topic is a lowercase
  kebab-case domain noun. The folder name (`docs/instructions/`) already says
  "instructions", so filenames carry **no** `.instructions` suffix.
- Topics are domain nouns — the full set is: `api-design`,
  `backend-architecture`, `branch-naming`, `cas-core`, `cli`,
  `coding-guidelines`, `consistency`, `defaults`, `examples`, `extensions`,
  `frontend-architecture`, `library-design`, `object-versioning`,
  `operations`, `performance`, `testing-strategy`, `versioning`,
  `viewer-design`, `viewer-security`.
- No redundant prefixes: never prefix a topic with `go-` (the file is
  `coding-guidelines.md`) and never repeat "instructions" inside a name.
- One topic per file; `-api` / `-design` / `-security` suffixes disambiguate
  facets of the same domain (viewer).
- The specs live in `docs/instructions/` — a host-agnostic home that is not
  tied to GitHub (any host or agent can find it); the repo-root agent
  aggregator (`AGENTS.md`) points at this folder and is auto-read by any
  agent that honors the AGENTS.md convention. This meta-guide is the sole
  `AGENT.md`.

---

## 3. Frontmatter (required)

Every file MUST begin with YAML frontmatter, exactly three keys:

```yaml
---
title: <Topic> — go-cask
version: v5
description: One sentence stating what the file requires/documents and who it applies to.
---
```

Rules:

- `title` matches the H1 exactly (minus the leading `# `).
- `version` is a simple marker (`v1`, `v2`, …). All files start at `v1`.
  **Increment by one** (`v1` → `v2`, …) whenever the file is **significantly
  extended or changed** — a material change to requirements, contracts, or
  structure (e.g. a new section that adds requirements, a renamed concept).
  Cosmetic fixes (typos, formatting, wording) do NOT bump the version.
- `description` is a single line: imperative/descriptive, mentions the key
  components and the related specs where useful.
- No other frontmatter keys. No blank line before `---`.

---

## 4. Document Structure (template)

1. **H1** — `# <Title>`, identical to the frontmatter title.
2. **Intro blockquote** (`>`) — 2–6 lines: what this file governs, in one or
   two sentences, then a `Related:` line listing the specs it must be read
   with (relative paths, backticked). Example:
   ```
   > Related: `docs/instructions/cas-core.md` (…),
   > `docs/instructions/coding-guidelines.md` (…).
   ```
3. **Numbered `##` sections** — `## 1. Purpose & Scope` onwards. Sections are
   numbered; subsections are `### 3.1 …` (or `### 4.13` style when appended).
4. **`---` horizontal rules** between top-level sections.
5. **Closing `## N. Checklist`** — most spec files end with a checklist of
   acceptance items derived from the body.

Content rules:

- Requirements are stated once and referenced, never duplicated with drift.
- Tables for enumerations/contracts (methods, status codes, roles, aspects);
  fenced code blocks (`go`, `yaml`, `text`, `mermaid`) for the concrete
  shapes; prose for rationale.
- Every file references the shared glossary (§6) — do not redefine terms.

---

## 5. Normative Language & Tone

- **MUST / MUST NOT / SHALL / SHALL NOT** — hard requirements.
- **SHOULD / SHOULD NOT** — strong recommendation with a documented reason.
- **MAY** — optional; state the decision point.
- Imperative, present tense, active voice. No marketing, no "we", no filler.
- Rules are checkable: an agent or reviewer can tick them off.
- Where a requirement came from a decision (e.g. the DeepSeek design
  conversation), one provenance sentence is allowed in the intro — never
  repeated per section.

---

## 6. Terminology (shared glossary)

All files MUST use exactly these terms. **Forbidden synonyms are listed.**

| Term               | Meaning / usage                                                        |
| ------------------ | ---------------------------------------------------------------------- |
| go-cask / CASK     | The project (Content Addressable Store Kit).                           |
| CAS / CASK         | Acronyms, always ALL-CAPS when written out: "CAS" = Content Addressable Store, "CASK" = Content Addressable Store Kit. Never lowercase (lowercase `cas` is the Go package, next row). |
| `cas` package      | The generic core library (`cas/`, `package cas`).                      |
| `gitlike` package  | The example layer (`examples/gitlike/`) — Blob/Tree/Commit/Tag, Repository, Resolver. NOT part of `cas`. |
| the viewer         | The embedded technical browser UI (`internal/web/`). **Not** "debug UI".     |
| viewer API         | The hypermedia surface under `/viewer/` (HTML).                        |
| CAS API            | The JSON HTTP API **pattern** demonstrated by `examples/api` — the product ships no network surface.                                |
| `RawStore`         | The non-generic byte-storage interface; backends: `FSRawStore`, `MemoryRawStore`, … |
| `Store[T]`         | The generic typed store.                                               |
| `Hash`             | Content address `algo:hexdigest`; validated with `ParseHash`.          |
| fan-out            | The configurable directory layout (`FanOut`/`FanLevels`), Git-like default. |
| lock-free reads    | `Get`/`Exists`/`List`/`Stats` take no lock (atomic rename).            |
| CAS laws           | The invariants in `testing-strategy.md` §1.               |

Forbidden / deprecated:

- "debug UI" → **viewer**; "debug_ui" config key → **viewer**.
- "go-coding-guidelines" → **coding-guidelines** (the file was renamed).
- "Repository/Resolver in the core" → they are **gitlike example layer**.
- "sharded paths" → **fan-out** layouts.

---

## 7. Cross-Referencing & Related Specs

- Refer to sibling files by backticked path (`docs/instructions/cas-core.md`)
  or, inside a `Related:` line, by short backticked name (`cas-core.md`).
- Reference sections by their number (`§4.4`, `P-05`, `§2`) — never by
  approximate prose.
- When a change affects a contract, update **all** files that reference it in
  one pass; a `grep` for the changed term across `docs/instructions/` and
  `.github/` must come back clean.
- The `AGENTS.md` aggregator's "Related specs" list MUST list every
  instruction file (add new files there when created).

---

## 8. Precedence & Conflict Resolution

When two files appear to conflict, this order decides (highest first):

1. **Security** — `viewer-security.md` is non-negotiable for
   anything touching the viewer; nothing may weaken it.
2. **Common conventions** — `api-design.md` (HTTP design),
   `coding-guidelines.md` (Go style), `library-design.md`
   (lean-core/errors).
3. **Architecture** — `cas-core.md` defines the library
   component contracts; `backend-architecture.md` and
   `frontend-architecture.md` compose them into the viewer and
   browser-facing system; `performance`/`testing-strategy`/`operations`
   refine them.
4. **Examples** — `examples.md` may demonstrate, never redefine.
5. **This file (AGENT.md)** governs the documents themselves.

On any conflict: fix the **more specific** document to match the more general
one, unless the specific document is higher in this order. Never leave two
contradicting statements in the folder.

---

## 9. Diagram & Formatting Rules

- **Mermaid** for relationships/flow (renders on GitHub): `classDiagram` for
  object models, `flowchart` for flows. One diagram per concept, next to the
  concept it visualizes.
- **Mermaid blocks MUST be balanced**: every ` ```mermaid ` opener has a
  matching ` ``` ` closer (unbalanced fences break rendering and swallow the
  rest of the file). The ONLY exception is an **explicitly stated**
  unbalanced snippet — e.g. an illustrative mermaid fragment shown inside a
  code example — which MUST be labeled in the surrounding text (e.g.
  "unbalanced by design: illustrative fragment, not renderable"). Never leave
  a mermaid block unbalanced without that statement.
- **ASCII** diagrams are allowed alongside mermaid (raw/terminal views) but
  must stay box-aligned; prefer mermaid when both would exist.
- Code fences always carry a language tag: `go`, `yaml`, `text`, `json`,
  `html`, `mermaid`.
- Tables: pipe tables with a header separator row; alignment via
  `:---:` only where it adds meaning.
- Line width ≤ ~100 chars; LF endings; UTF-8.
- Numbers/lists: requirements numbered (`P-01…`, `A-…`) only when
  cross-referenced; otherwise bullet lists.

---

## 10. Folder Inventory

All spec files live in `docs/instructions/` at the repo root — a neutral,
host-agnostic location (not `.github/`, so discovery does not depend on the
host being GitHub). The repo-root `AGENTS.md` is the agent aggregator that
points at this folder; it follows the same style rules where they apply.

| File                                             | Role                                                            |
| ------------------------------------------------ | --------------------------------------------------------------- |
| `AGENT.md` (this file)                           | Meta-guide: style, terminology, precedence for this folder.     |
| `cas-core.md`                 | The canonical core library spec — every component contract, flows,   |
|                                            | concurrency, and the extension contract for extensions/clients.      |
| `backend-architecture.md`           | Server-side architecture: the `cask web` server, HTTP wiring,      |
|                                                  | middleware, config, lifecycle, deployment shapes.                  |
| `frontend-architecture.md`          | Browser-facing architecture: hypermedia rendering, nested        |
|                                                  | templates, htmx model, URL-as-state, embedding.                  |
| `coding-guidelines.md`              | Idiomatic Go, std-lib only, no CSS/JS, templates + htmx, docs.  |
| `library-design.md`                 | Lean-core budget, sentinel errors, no mutable globals, compat.  |
| `performance.md`                    | Lock-free reads, streaming, allocations, benchmarks, profiling. |
| `testing-strategy.md`               | The CAS laws + unit/property/fuzz/race/corruption/golden tests. |
| `operations.md`                     | Durability, recovery, observability, migration, backup.         |
| `consistency.md`                    | Broken/dangling detection, GC from roots, age-based pruning.    |
| `defaults.md`                       | Canonical defaults & behavior reference (all constants, grouped). |
| `versioning.md`                     | Library Git versioning: semver tags, Go module v2+ rules, release process. |
| `branch-naming.md`                  | Simple Git branch concept: main + short-lived typed branches, patterns, lifecycle. |
| `cli.md`                            | cmd/cask contract: subcommands, flags, output, exit codes, local ops plus the `web` viewer subcommand. |
| `object-versioning.md`              | Object-model semver: versioned type names, coexisting majors, migration. |
| `viewer-security.md`                | Viewer security requirements (authn/authz, sessions, audit).    |
| `viewer-design.md`                  | Viewer UI design (dashboard, templates + htmx, low-level views).|
| `api-design.md`                     | Shared HTTP API design conventions (viewer + example HTTP surfaces).             |
| `examples.md`                       | Example-program rules + the five proposed examples.             |
| `extensions.md`                     | Minimal requirements for future extensions/clients of the core; catalog of designed-but-deferred possible extensions. |

---

## 11. Editing & Maintenance Checklist

Before committing any change to a file in this folder:

- [x] Frontmatter present, `title` == H1, one-line `description`
- [ ] `version` bumped by one when the change is material (new/removed
      requirements, contract changes, restructure); unchanged for cosmetic
      edits (§3)
- [x] Structure follows §4; `---` between sections; checklist at the end
  where applicable
- [x] Terminology matches §6 exactly (no "debug UI", no "go-coding-guidelines",
      no "Repository in core")
- [ ] Normative language per §5 (MUST/SHALL/MAY used consistently)
- [x] Cross-references updated in ALL files that mention the changed term;
      `grep` over `docs/instructions/` and `.github/` for old terms returns
      nothing
- [x] New files added to the `AGENTS.md` aggregator's "Related specs" list
      and to the §10 inventory
- [ ] No contradictions with higher-precedence files (§8); conflicts resolved
      in the more specific document
- [ ] Diagrams valid mermaid; fences tagged; no stale ASCII misalignment
- [x] Every mermaid block balanced (` ```mermaid ` count == its ` ``` `
      closers per file), unless explicitly stated as an illustrative fragment
      (§9)
