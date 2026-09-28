---
type: Agent Instructions
title: AGENT — go-cask (agent skills)
description: The guide for .agents/ — how a repository skill is discovered, how its SKILL.md is authored and budgeted, what belongs in user scope instead, when vendored material needs a NOTICE, and the validation checklist a new skill must pass.
version: v5
tags: [go-cask]
status: stable
---

# AGENT — go-cask (agent skills)

Governs every skill under `.agents/skills/`. Repo-root [`AGENTS.md`](/AGENTS.md) routes agents here; `docs/index.md` maps it by path. A skill loads as working instructions, not as a specification: it changes an agent's process, never the product's behavior. Markdown content [`docs/AGENT.md`](/docs/AGENT.md) §1.4, formatting §7. Related: [`docs/index.md`](/docs/index.md), [`docs/specs/AGENT.md`](/docs/specs/AGENT.md).

## Table of contents

- [Scope](#1-scope)
- [Discovery contract](#2-discovery-contract)
- [A skill is not a spec](#3-a-skill-is-not-a-spec)
- [The SKILL.md contract](#4-the-skillmd-contract)
- [Authoring rules](#5-authoring-rules)
- [Budget and trimming](#6-budget-and-trimming)
- [Vendored provenance and NOTICE](#7-vendored-provenance-and-notice)
- [Adding, changing, removing a skill](#8-adding-changing-removing-a-skill)
- [Checklist](#9-checklist)

## 1. Scope

Owns skills in `.agents/skills/`: discovery, format, content budget, provenance, removal. Owns no product contract: a skill MUST NOT state a rule a specification or an `AGENT.md` already states; on contradiction the higher-precedence file wins ([`docs/specs/AGENT.md`](/docs/specs/AGENT.md) §8).

## 2. Discovery contract

Client scans roots one level deep, reads the YAML frontmatter of each directory bundle.

| Rule | Value |
|---|---|
| Recognized shape | `<root>/<name>/SKILL.md` (directory bundle) or `<root>/<name>.md` (flat) |
| Not recognized | Nested `**/SKILL.md` below a root; a bundle without `SKILL.md` |
| Repository root | `.agents/skills` — project scope for agents reading this repository |
| Nesting | Exactly one level: no `skills/group/<name>/SKILL.md` |

A skill under `docs/`, `internal/`, `scripts/` or any package directory is never discovered: MUST live at `.agents/skills/<name>/SKILL.md`.

## 3. A skill is not a spec

| | Specs and `AGENT.md` files | Skills |
|---|---|---|
| Audience | Any contributor, human or agent | An agent at work in a session |
| Contract | Product and process requirements | Process only: order, pitfalls, evidence |
| Discovery | Read when the path maps to it | Loaded when the description matches the task |
| Authority | Requirements the code is measured against | Process an agent follows while changing it |
| Failure mode | Drift from the code | Bloat, duplication, stale pointers |

A skill earns its place only when it changes **how** work is done and cannot be derived from the spec set: a phase order, a per-surface failure mode, a validation gate, a delegation rule. Reference material a table-of-contents lookup already reaches MUST NOT be restated as a skill.

## 4. The SKILL.md contract

Frontmatter carries exactly two keys:

```yaml
---
name: {skill-name}
description: >
  What the skill does, then when to use it, then the literal phrases that should
  trigger it.
---
```

- `name` MUST equal bundle directory name, lowercase kebab-case.
- `description` required: the only text a client shows before loading the body, so it carries the triggers. Under 400 characters — a truncated catalog entry loses the phrase a user would have typed.
- Body Markdown: H1 naming the skill's job, then the procedure. Phase order beats topic order.

## 5. Authoring rules

1. Point at the authoritative file by path; never copy a rule in. A duplicated rule drifts, and the copy is what the agent follows.
2. Keep a shared law once, in the skill that needs it; the others reference the file that states it.
3. Write commands and paths exactly as the repository spells them — the WSL requirement for the verification gate, the literal closing line marking a green run.
4. Tables to compress enumerations: one row per change surface, with the failure mode that actually happens and the file to read.
5. State stop conditions: the changes that require asking before proceeding.
6. Raw HTML forbidden, as everywhere here. Prefer tables and language-tagged fences; line width within repository convention.
7. Never restate a communication skill's session style rules as technical requirements: a style skill governs how a session talks, not how the repository is built.
8. Personal communication preference does not ship here. It belongs in the contributor's own agent configuration: user-scope skills or a profile prompt patch. This directory ships practices any contributor should follow, and a taste preference is not one.

## 6. Budget and trimming

A loaded skill enters the context budget of every session that triggers it.

- Target 2 to 6 KB per skill; beyond that, split by change surface, never grow one file.
- Every section MUST answer "what does the agent do differently because of this". Delete a section that only explains.
- Prefer pointers over prose: a skill that mostly links is doing its job; one that mostly explains is a spec in the wrong directory.
- Re-read the skill when the files it points at materially change; move the paths in the same change.

## 7. Vendored provenance and NOTICE

A skill copied or adapted from outside this repository is vendored material and MUST carry a `NOTICE` in its bundle directory:

| Field | Content |
|---|---|
| Source | Upstream repository and the exact path inside it |
| Revision | The upstream commit hash, and the blob hash of the copied file |
| Copyright | The upstream copyright line |
| License | The upstream license, and confirmation that the copied path is covered by it |
| Local changes | What was adapted, stated minimally |

The skill body also names its upstream and license in a short provenance block, so attribution travels with the loaded text. Never edit vendored rules in place: re-vendor, then update revision and blob hash.

## 8. Adding, changing, removing a skill

1. Create `.agents/skills/<name>/SKILL.md` with the two-key frontmatter.
2. Add or update the `.agents/skills/` row in [`docs/index.md`](/docs/index.md) so a path match reaches the skill; name the skill in the [`README.md`](/README.md) "Repository layout" list when the set of skills changes.
3. Add a `NOTICE` when §7 applies.
4. On removal, delete the bundle and both mentions — a dangling pointer in the index is worse than no row.

Skills change no product behavior, so they get no `CHANGELOG.md` entry: agent tooling, not a shipped capability.

## 9. Checklist

Before committing a skill:
- [x] `name` equals the bundle directory name, lowercase kebab-case
- [x] Exactly two frontmatter keys; `description` present and under 400 characters
- [x] Bundle is `.agents/skills/<name>/SKILL.md`, one level deep
- [x] Every rule points at its authoritative file instead of restating it
- [x] Length is within the 2 to 6 KB target, or split by surface
- [x] Relative paths resolve from the skill directory
- [x] No raw HTML, no HTML/XML/SVG fences
- [x] Vendored material carries a `NOTICE` with revision, blob hash, copyright, license
- [x] `docs/index.md` row and the `AGENTS.md` layout line updated
- [x] No `CHANGELOG.md` entry (skills are not user-visible capability)
