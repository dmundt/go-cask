---
type: Agent Instructions
title: Agent Instructions — go-cask
description: Repo-root router for AI agents — the session rules, plus the pointer to the owning spec (docs/index.md → cas-core, coding-guidelines, api-design, rest). Restates no rule. Auto-read by any agent honoring AGENTS.md (GitHub Copilot, OpenAI Codex, Cursor, …).
version: v51
---

# Agent Instructions — go-cask (CASK: Content-Addressable Store Kit)

Router, not manual. Read `docs/index.md` first: path → owning spec, longest match. Normative design: `docs/specs/`. A path-derived rule is never repeated here.

## Rules — session

| Rule | Owner |
| --- | --- |
| Worktree, lane claim, gate, signed commits, squash auto-merge | `docs/specs/landing.md` |
| Branch type, issue number, base, lifecycle | `docs/specs/branch-naming.md` |
| Changelog entry, GitHub release notes | `docs/specs/versioning.md` §4 |
| Markdown: no raw HTML, tagged fences, balanced mermaid | `docs/specs/AGENT.md` §9, `docs/AGENT.md` §1.4 |
| Terminology; precedence (a user instruction outranks every file); spec upkeep | `docs/specs/AGENT.md` §6, §8, §10 |

## Rules — this file

- Ceiling 6 KiB, enforced: `go run ./cmd/buildtool markdown-integrity` (`docs/AGENT.md` §2.1).
- Telegram style: fragments, tables, backticked paths. No prose, rationale, restated rule, duplicated diagram, example or inventory; a path-derived rule belongs in its owner, reached through `docs/index.md`.
- Trees: `README.md` "Repository layout". Change playbook: `.agents/skills/cask-change/SKILL.md`.
- Over ceiling? Move the rule to its owner, link it. Never delete it, never raise the ceiling silently.
