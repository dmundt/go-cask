---
name: cask-change
description: >
  Change playbook for the go-cask (CASK) repository: editing cas/, a backend
  under cas/backend/, codecs, the cmd/cask CLI, internal/web, gitlike/,
  examples/, or scripts/. Use when a change must satisfy the repo's spec set
  and verification gate. Triggers: "change cas", "add a backend", "add a
  codec", "touch the viewer", "extend cask", "before I push go-cask".
---

# go-cask change playbook

Do the phases in order. Each phase names the artifact it must produce.

## Phase 1 — Map the change

1. Read [`docs/index.md`](/docs/index.md): match the file you will edit against
   its first column, longest match wins, and read the rule file that row links.
2. Read the governing `AGENT.md` for that subtree (`cas/AGENT.md`,
   `docs/specs/AGENT.md`, `scripts/AGENT.md`, `internal/web/README.md`, ...).
3. Read the specific section, not the whole spec. Specs are large by design;
   the index row tells you the section.

Artifact: the exact rule file and section you are working under, named.

## Phase 2 — Laws every layer shares

These hold everywhere in the repository. A change that breaks one of them is
wrong regardless of which spec covers the file.

1. **No `any`, no reflection, in the public API.** Exported signatures carry
   concrete types or type parameters. Cross-type work goes through `cas/repo`
   (`Registry`, `RegisterStore[T]`, `LookupStore[T]`, `Walk`, `Reachable`).
2. **`context.Context` first on every exported function**, and `%w` on every
   wrapped error. Never swallow cancellation.
3. **Immutability.** Never mutate a stored object: re-`Put` and take the new
   digest.
4. **The core names no algorithm.** The client injects a `cas.Hasher`;
   go-cask's own clients wire `cas/hash/sha256`. Do not bake an algorithm,
   format, or codec into `cas/` itself.
5. **Boundaries stay boring.** Byte layer non-generic; backends storage-only;
   manifest/helper logic in `cas/pack`; object models layered above the core.
   A package that is hard to classify is a design defect, not a naming problem.
6. **One store base, one store.** Never nest a store inside another's base or a
   parent of it, and never keep app scratch `*.tmp` under a base. `List`/`Stats`
   report any digest-named file, `Clean` reclaims any `*.tmp`, at any depth.
7. **Viewer is hypermedia only:** `html/template` plus htmx, one scoped
   stylesheet, no custom JavaScript, no inline handlers. Security-sensitive
   viewer work follows [`docs/specs/viewer-security.md`](/docs/specs/viewer-security.md)
   even when the diff looks cosmetic.

## Phase 3 — Per-surface failure modes

| You are changing | Failure mode that actually happens | Read |
|---|---|---|
| `cas/*.go` | Export creep beyond the lean budget; new `any`; unexported-invariant expressed in a codec-specific JSON method instead of `Validate()` | [`library-design.md`](/docs/specs/library-design.md), [`cas-core.md`](/docs/specs/cas-core.md) |
| `cas/backend/*` | Backend grows domain logic; `context` or `%w` dropped; assumed idempotent `Put`; locked reads in a backend that should read lock-free | [`cas-core.md`](/docs/specs/cas-core.md) §4.3-4.5, §4.14 |
| `cas/codec/*` | Wrapper not cascadeable; decompressing codec without a `MaxDecodedBytes` bound | [`defaults.md`](/docs/specs/defaults.md), [`extensions.md`](/docs/specs/extensions.md) |
| `cas/cache/*` | Lazy-load contract of `CachedObject[T]` broken; metrics counters left unupdated | [`cas-core.md`](/docs/specs/cas-core.md) §4.10 |
| `cas/repo/`, `cas/refs/` | Root set for GC not extended, so a new reference kind gets collected | [`consistency.md`](/docs/specs/consistency.md) §4 |
| `cmd/cask`, `internal/store/` | New flag without the contract's exit code, output format, or backend-kind parsing | [`cli.md`](/docs/specs/cli.md), [`backend-architecture.md`](/docs/specs/backend-architecture.md) |
| `internal/web/` | Template drift, CSRF/session/audit gap across the middleware pipeline | [`viewer-security.md`](/docs/specs/viewer-security.md), [`frontend-architecture.md`](/docs/specs/frontend-architecture.md) |
| `gitlike/`, `examples/` | Adding application types to `cas/`; extending `gitlike/` instead of copying the pattern into a new package | [`examples.md`](/docs/specs/examples.md) §2 |
| `cas/*_test.go`, coverage tiers | Law proven by example test instead of property/fuzz/race test | [`testing-strategy.md`](/docs/specs/testing-strategy.md) |
| hot read paths | Allocation added to a read path; benchmark claim without a profile | [`performance.md`](/docs/specs/performance.md) |

## Phase 4 — Evidence before commit

1. `CHANGELOG.md` updated when the change is user-visible (library consumer, CLI
   user, operator, viewer behavior, security). Internal refactors, tests, CI,
   and formatting do not get entries. End user-facing entries in one bullet.
2. The full gate passes: `./scripts/verify.sh`, ending in `verification passed`.
   On Windows run it under WSL with a Linux Go toolchain, per
   [`scripts/AGENT.md`](/scripts/AGENT.md) — never reproduce the gate's steps in
   PowerShell, and never read a partial run as green.
3. Documentation that names the thing you changed is updated in the same change:
   `docs/index.md` rows, `docs/specs/index.md` rows, and the `version:` field of
   every instruction file you materially edit.
4. Report which spec section authorized the change and which gate run you ended
   on. "Tests pass" is not evidence; the literal closing line is.

## Phase 5 — Stop conditions

Ask before proceeding when the change would: add a dependency (needs the
coding-guidelines §3 exception process), change the on-disk envelope or fan-out
layout (migration, [`operations.md`](/docs/specs/operations.md) §5), alter the
public exported surface (compatibility, [`library-design.md`](/docs/specs/library-design.md)),
or relax a viewer-security requirement.

## For deeper reading

- [`AGENTS.md`](/AGENTS.md) — repo aggregator: architecture, design principles, extension guide.
- [`docs/index.md`](/docs/index.md) — path to rule file, read this first.
- [`docs/specs/AGENT.md`](/docs/specs/AGENT.md) — how the spec set is maintained.
- [`scripts/AGENT.md`](/scripts/AGENT.md) — running the gate, including on Windows.
