---
type: Agent Instructions
title: Agent Instructions — go-cask
description: Repo-root router for AI agents — session rules, plus pointer to owning spec (docs/index.md → cas-core, coding-guidelines, api-design, rest). Restates no rule. Auto-read by any agent honoring AGENTS.md (GitHub Copilot, OpenAI Codex, Cursor, …).
version: v50
---

# Agent Instructions — go-cask (CASK: Content-Addressable Store Kit)

Router, not manual. Read `docs/index.md` first: path → owning spec, longest match. Normative design: `docs/specs/`.

## Rules — session

| Rule | Owner |
| --- | --- |
| Worktree, lane claim, gate, signed commits, squash auto-merge | `docs/specs/landing.md` |
| Branch type, issue number, base, lifecycle | `docs/specs/branch-naming.md` |
| Changelog entry, GitHub release notes | `docs/specs/versioning.md` §4 |
| Markdown: no raw HTML, tagged fences, balanced mermaid | `docs/specs/AGENT.md` §9, `docs/AGENT.md` §1.4 |
| Terminology, precedence (user instruction first), spec upkeep | `docs/specs/AGENT.md` §6, §8, §10 |
| Core invariants: digest-addressed, immutable, dedup, no `any` | `docs/specs/cas-core.md` §2, `docs/specs/library-design.md` §4 |
| Citizen classes, import matrix | `docs/specs/library-design.md` §1.1 |
| Extension recipes: backend, object type, codec, cache, algorithm | `docs/specs/cas-core.md` §7.2 |
| Defaults: hash, codec, fan-out, permissions | `docs/specs/defaults.md` §2 |
| Viewer | `docs/specs/viewer-security.md`, `docs/specs/viewer-design.md` |
| Website | `website/AGENT.md` |
| Landing helpers, gate internals, WSL, releases, the Go build language | `scripts/AGENT.md`, `internal/build/AGENT.md` |
| CI, branch protection, required checks, gate receipts | `.github/AGENT.md` |
| Clients that never read this file (Copilot) | `.github/copilot-instructions.md` |
| Core orientation, design history | `docs/design/core-overview.md`, `docs/design/design-history.md` |

## Rules — this file

- Ceiling 6 KiB, enforced: `go run ./cmd/buildtool markdown-integrity` (`docs/AGENT.md` §2.1).
- Telegram style: fragments, tables, backticked paths. No prose, rationale, restated rule, duplicated diagram, example or inventory.
- Trees: `README.md` "Repository layout". Change playbook: `.agents/skills/cask-change/SKILL.md`.
- Over ceiling? Move rule to owner, link it. Never delete it, never raise ceiling silently.
