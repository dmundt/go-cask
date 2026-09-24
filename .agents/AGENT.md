---
type: Agent Instructions
title: AGENT — go-cask (agent skills)
description: The guide for .agents/ — how a repository skill is discovered, how its SKILL.md is authored and budgeted, what belongs in user scope instead, when vendored material needs a NOTICE, and the validation checklist a new skill must pass.
version: v2
tags: [go-cask]
status: stable
---

# AGENT — go-cask (agent skills)

Governs every skill under `.agents/skills/`. The repo-root [`AGENTS.md`](/AGENTS.md)
routes agents to it and `docs/index.md` maps it by path. A skill is loaded as
working instructions, not as a specification: it changes an agent's process and
never the product's behavior. Follow
[`docs/AGENT.md`](/docs/AGENT.md) §1.4 for Markdown content and §7 for
formatting. Related: [`docs/index.md`](/docs/index.md),
[`docs/specs/AGENT.md`](/docs/specs/AGENT.md).

## Table of contents

- [Scope](#1-scope)
- [Discovery](#2-discovery-contract)
- [A skill is not a spec](#3-a-skill-is-not-a-spec)
- [The SKILL.md contract](#4-the-skillmd-contract)
- [Authoring rules](#5-authoring-rules)
- [Budget and trimming](#6-budget-and-trimming)
- [Vendored provenance and NOTICE](#7-vendored-provenance-and-notice)
- [Adding, changing, removing a skill](#8-adding-changing-removing-a-skill)
- [Checklist](#9-checklist)

## 1. Scope

This file owns the skills in `.agents/skills/`: their discovery, format,
content budget, provenance, and removal. It does not own any product contract:
skills MUST NOT state a rule that a specification or an `AGENT.md` already
states, and when a skill contradicts one of them the higher-precedence file
wins ([`docs/specs/AGENT.md`](/docs/specs/AGENT.md) §8).

## 2. Discovery contract

A client scans roots one level deep and reads the YAML frontmatter of each
directory bundle. The rules that bind a file's location:

| Rule | Value |
|---|---|
| Recognized shape | `<root>/<name>/SKILL.md` (a directory bundle) or `<root>/<name>.md` (flat) |
| Not recognized | Nested `**/SKILL.md` anywhere below a root; a bundle without `SKILL.md` |
| Repository root | `.agents/skills`, the project-scope root for agents that read this repository |
| Nesting | Exactly one level: no `skills/group/<name>/SKILL.md` |

Consequence: a skill placed under `docs/`, `internal/`, `scripts/`, or any
package directory is never discovered. It MUST live at
`.agents/skills/<name>/SKILL.md`.

## 3. A skill is not a spec

| | Specs and `AGENT.md` files | Skills |
|---|---|---|
| Audience | Any contributor, human or agent | An agent at work in a session |
| Contract | Product and process requirements | Process only: order, pitfalls, evidence |
| Discovery | Read when the path maps to it | Loaded when the description matches the task |
| Authority | Requirements the code is measured against | Process an agent follows while changing it |
| Failure mode | Drift from the code | Bloat, duplication, stale pointers |

A repository skill earns its place only when it changes **how** work is done and
cannot be derived from the spec set alone: a phase order, a per-surface failure
mode, a validation gate, a delegation rule. Reference material that a
table-of-contents lookup already reaches MUST NOT be restated as a skill.

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

- `name` MUST equal the bundle directory name and be lowercase kebab-case.
- `description` is required and is the only text a client shows before loading
  the body, so it carries the triggers. Keep it under 400 characters; a
  truncated catalog entry loses the phrase a user would have typed.
- The body is Markdown. Start with an H1 naming the skill's job, then the
  procedure. Phase order beats topic order: an agent reads top to bottom.

## 5. Authoring rules

1. Point at the authoritative file by path; never copy a rule into a skill. A
   duplicated rule drifts, and the copy is what the agent will follow.
2. Keep the shared laws here once, in the skill that needs them, and let the
   others reference the file that states them.
3. Write commands and paths exactly as the repository spells them, including the
   WSL requirement for the verification gate and the literal closing line that
   marks a green run.
4. Use tables to compress enumerations: one row per change surface, with the
   failure mode that actually happens and the file to read.
5. State stop conditions: the changes that require asking before proceeding.
6. Raw HTML is forbidden, as everywhere in this repository. Prefer tables and
   fenced code with a language tag; keep line width within the repository
   convention.
7. Do not restate the session style rules of a communication skill as technical
   requirements. A style skill governs how a session talks, not how the
   repository is built.
8. A personal communication preference — how a session talks, not how this
   repository is built — does not ship here. It belongs to the contributor's own
   agent configuration: user-scope skills or a profile prompt patch. This
   directory ships practices that any contributor of this repository should
   follow, and a taste preference is not one of them.

## 6. Budget and trimming

A loaded skill enters the context budget of every session that triggers it.

- Target 2 to 6 KB per skill. Beyond that, split by change surface instead of
  growing one file.
- Every section MUST answer "what does the agent do differently because of
  this". Delete a section that only explains.
- Prefer pointers over prose. A skill that is mostly links is doing its job; a
  skill that is mostly explanation is a spec in the wrong directory.
- Re-read the skill when the files it points at materially change, and update
  the paths in the same change that moves them.

## 7. Vendored provenance and NOTICE

A skill that is copied or adapted from outside this repository is vendored
material and MUST carry a `NOTICE` file in its bundle directory recording:

| Field | Content |
|---|---|
| Source | Upstream repository and the exact path inside it |
| Revision | The upstream commit hash, and the blob hash of the copied file |
| Copyright | The upstream copyright line |
| License | The upstream license, and confirmation that the copied path is covered by it |
| Local changes | What was adapted, stated minimally |

The skill body also names its upstream and license in a short provenance block,
so the attribution travels with the loaded text. Never edit vendored rules in
place: re-vendor, then update the revision and blob hash.

## 8. Adding, changing, removing a skill

1. Create `.agents/skills/<name>/SKILL.md` with the two-key frontmatter.
2. Add or update the `.agents/skills/` row in [`docs/index.md`](/docs/index.md)
   so a path match reaches the skill, and name the skill in the repo-layout
   block of [`AGENTS.md`](/AGENTS.md) when the set of skills changes.
3. Add a `NOTICE` when §7 applies.
4. On removal, delete the bundle and both mentions. A dangling pointer in the
   index is worse than no row.

Skills do not change product behavior, so they do not get a `CHANGELOG.md`
entry. They are agent tooling, not a shipped capability.

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
