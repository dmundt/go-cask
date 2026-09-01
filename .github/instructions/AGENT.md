---
title: AGENT — go-cask Instruction Folder Guide
description: The meta-guide for .github/instructions/ — file naming, frontmatter, document structure, normative language, shared terminology, cross-referencing, precedence, and the maintenance checklist that keeps every instruction file consistent.
version: v2
---

# AGENT — go-cask Instruction Folder Guide

> This file governs **the other files in this folder**. Every agent (Copilot,
> other AI tooling) and every human maintainer editing any
> `.github/instructions/*.instructions.md` MUST follow it, so the folder stays
> a single, coherent specification set rather than a pile of docs.
>
> Scope: this folder contains the normative specs of the go-cask project
> (core architecture, coding style, security, APIs, viewer design, examples,
> performance, testing, operations). `.github/copilot-instructions.md` is the
> aggregator that points at them; it is outside this folder and follows the
> same style rules where they apply.

---

## 1. Purpose & Scope

- The folder is the **single source of truth** for how go-cask is designed,
  built, secured, tested, and operated.
- Every file states **requirements** (what MUST/SHALL be true) and **context**
  (why), not prose about the project.
- New files are added only when a real gap exists (see §5 of
  `examples.instructions.md` for the example-generation analogue); prefer
  extending an existing file over creating a new one.

---

## 2. File Naming

- Pattern: `<kebab-case-topic>.instructions.md`.
- Topics are domain nouns — the full set is: `api-design`,
  `backend-architecture`, `branch-naming`, `cas-api`, `cas-core`, `cli`,
  `coding-guidelines`, `consistency`, `defaults`, `examples`, `extensions`,
  `frontend-architecture`, `library-design`, `object-versioning`,
  `operations`, `performance`, `testing-strategy`, `versioning`,
  `viewer-api`, `viewer-design`, `viewer-security`.
- No redundant prefixes: the folder already says "instructions" — do not
  prefix topics with `go-` (the file is `coding-guidelines.instructions.md`,
  not `go-coding-guidelines.instructions.md`).
- One topic per file; `-api` / `-design` / `-security` suffixes disambiguate
  facets of the same domain (viewer).
- The aggregator lives one level up (`.github/copilot-instructions.md`);
  this meta-guide is the sole `AGENT.md`.

---

## 3. Frontmatter (required)

Every file MUST begin with YAML frontmatter, exactly three keys:

```yaml
---
title: <Topic> — go-cask
version: v1
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
   > Related: `.github/instructions/cas-core.instructions.md` (…),
   > `.github/instructions/coding-guidelines.instructions.md` (…).
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
| go-cask / CASK     | The project (Content Addressed Storage Kit).                           |
| `cas` package      | The generic core library (`cas/`, `package cas`).                      |
| `gitlike` package  | The example layer (`examples/gitlike/`) — Blob/Tree/Commit/Tag, Repository, Resolver. NOT part of `cas`. |
| the viewer         | The embedded technical browser UI (`viewer/`). **Not** "debug UI".     |
| viewer API         | The hypermedia surface under `/viewer/` (HTML).                        |
| CAS API            | The JSON data API under `/api/cas/v1/`.                                |
| `RawStore`         | The non-generic byte-storage interface; backends: `FSRawStore`, `MemoryRawStore`, … |
| `Store[T]`         | The generic typed store.                                               |
| `Hash`             | Content address `algo:hexdigest`; validated with `ParseHash`.          |
| fan-out            | The configurable directory layout (`FanOut`/`FanLevels`), Git-like default. |
| lock-free reads    | `Get`/`Exists`/`List`/`Stats` take no lock (atomic rename).            |
| IP-based rate limiting | Shared middleware: 2 req/s per IP, burst 20, 429 + `Retry-After` + `X-RateLimit-*`. |
| Swagger UI explorer | The optional in-browser API explorer (`GET /swagger/`), the single documented no-JS deviation. |
| CAS laws           | The invariants in `testing-strategy.instructions.md` §1.               |

Forbidden / deprecated:

- "debug UI" → **viewer**; "debug_ui" config key → **viewer**.
- "go-coding-guidelines" → **coding-guidelines** (the file was renamed).
- "Repository/Resolver in the core" → they are **gitlike example layer**.
- "sharded paths" → **fan-out** layouts.

---

## 7. Cross-Referencing & Related Specs

- Refer to sibling files by relative path in backticks:
  `.github/instructions/<file>.instructions.md` (or a short
  `<file>.instructions.md` name inside a `Related:` line).
- Reference sections by their number (`§4.4`, `R-14`, `§2`) — never by
  approximate prose.
- When a change affects a contract, update **all** files that reference it in
  one pass; a `grep` for the changed term across `.github/` must come back
  clean.
- The copilot aggregator's "Related specs" list MUST list every instruction
  file (add new files there when created).

---

## 8. Precedence & Conflict Resolution

When two files appear to conflict, this order decides (highest first):

1. **Security** — `viewer-security.instructions.md` is non-negotiable for
   anything touching the viewer; nothing may weaken it.
2. **Per-API specs** — `cas-api.instructions.md` / `viewer-api.instructions.md`
   define concrete routes and win over the shared conventions.
3. **Common conventions** — `api-design.instructions.md` (HTTP design),
   `coding-guidelines.instructions.md` (Go style), `library-design.
   instructions.md` (lean-core/errors).
4. **Architecture** — `cas-core.instructions.md` defines the library
   component contracts; `backend-architecture.instructions.md` and
   `frontend-architecture.instructions.md` compose them into the server and
   browser-facing system; `performance`/`testing-strategy`/`operations`
   refine them.
5. **Examples** — `examples.instructions.md` may demonstrate, never redefine.
6. **This file (AGENT.md)** governs the documents themselves.

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
- Numbers/lists: requirements numbered (`R-01…`, `P-01…`, `A-…`) only when
  cross-referenced; otherwise bullet lists.

---

## 10. Folder Inventory

| File                                             | Role                                                            |
| ------------------------------------------------ | --------------------------------------------------------------- |
| `AGENT.md` (this file)                           | Meta-guide: style, terminology, precedence for this folder.     |
| `cas-core.instructions.md`                 | The canonical core library spec — every component contract, flows,   |
|                                            | concurrency, and the extension contract for extensions/clients.      |
| `backend-architecture.instructions.md`           | Server-side architecture: the `cask web` server, HTTP wiring,      |
|                                                  | middleware, config, lifecycle, deployment shapes.                  |
| `frontend-architecture.instructions.md`          | Browser-facing architecture: hypermedia rendering, nested        |
|                                                  | templates, htmx model, URL-as-state, embedding.                  |
| `coding-guidelines.instructions.md`              | Idiomatic Go, std-lib only, no CSS/JS, templates + htmx, docs.  |
| `library-design.instructions.md`                 | Lean-core budget, sentinel errors, no mutable globals, compat.  |
| `performance.instructions.md`                    | Lock-free reads, streaming, allocations, benchmarks, profiling. |
| `testing-strategy.instructions.md`               | The CAS laws + unit/property/fuzz/race/corruption/golden tests. |
| `operations.instructions.md`                     | Durability, recovery, observability, migration, backup.         |
| `consistency.instructions.md`                    | Broken/dangling detection, GC from roots, age-based pruning.    |
| `defaults.instructions.md`                       | Canonical defaults & behavior reference (all constants, grouped). |
| `versioning.instructions.md`                     | Library Git versioning: semver tags, Go module v2+ rules, release process. |
| `branch-naming.instructions.md`                  | Simple Git branch concept: main + short-lived typed branches, patterns, lifecycle. |
| `cli.instructions.md`                            | cmd/cask contract: subcommands, flags, output, exit codes, local/remote modes, the `web` server subcommand. |
| `object-versioning.instructions.md`              | Object-model semver: versioned type names, coexisting majors, migration. |
| `viewer-security.instructions.md`                | Viewer security requirements (authn/authz, sessions, audit).    |
| `viewer-design.instructions.md`                  | Viewer UI design (dashboard, templates + htmx, low-level views).|
| `viewer-api.instructions.md`                     | Viewer + CAS API OpenAPI; two-surface separation.               |
| `cas-api.instructions.md`                        | Canonical CAS HTTP API spec (routes, requirements, rate limit). |
| `api-design.instructions.md`                     | Shared HTTP API design conventions (both surfaces).             |
| `examples.instructions.md`                       | Example-program rules + the five proposed examples.             |
| `extensions.instructions.md`                     | Minimal requirements for future extensions/clients of the core; catalog of designed-but-deferred possible extensions. |

---

## 11. Editing & Maintenance Checklist

Before committing any change to a file in this folder:

- [ ] Frontmatter present, `title` == H1, one-line `description`
- [ ] `version` bumped by one when the change is material (new/removed
      requirements, contract changes, restructure); unchanged for cosmetic
      edits (§3)
- [ ] Structure follows §4; `---` between sections; checklist at the end
  where applicable
- [ ] Terminology matches §6 exactly (no "debug UI", no "go-coding-guidelines",
      no "Repository in core")
- [ ] Normative language per §5 (MUST/SHALL/MAY used consistently)
- [ ] Cross-references updated in ALL files that mention the changed term;
      `grep` over `.github/` for old terms returns nothing
- [ ] New files added to the copilot aggregator's "Related specs" list and to
      the §10 inventory
- [ ] No contradictions with higher-precedence files (§8); conflicts resolved
      in the more specific document
- [ ] Diagrams valid mermaid; fences tagged; no stale ASCII misalignment
- [ ] Every mermaid block balanced (` ```mermaid ` count == its ` ``` `
      closers per file), unless explicitly stated as an illustrative fragment
      (§9)
