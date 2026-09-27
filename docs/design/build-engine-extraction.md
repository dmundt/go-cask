---
type: Design
title: Build Engine Extraction — go-cask
description: Decision brief for moving the build engine out of internal/ to build/engine so a second repository can import it — findings, name, change list, staged landings, acceptance.
version: v1
---

# Build Engine Extraction — go-cask

Non-normative. Decision brief for
[#446](https://github.com/dmundt/go-cask/issues/446): why the build engine is not
consumable today, where it should live, and the staged landings that get it there.
Normative rules stay where they are — `docs/specs/versioning.md` for tags,
[`internal/build/AGENT.md`](../../internal/build/AGENT.md) for the engine's own
rules, [`scripts/AGENT.md`](../../scripts/AGENT.md) for the gate.

## 1. The finding

Three files promise the same property: `internal/build/core` is "the reusable
half", it "serves a second repository"
([`internal/build/README.md`](../../internal/build/README.md)), the split
"exists because the engine is meant to serve a second repository"
([`scripts/AGENT.md`](../../scripts/AGENT.md)), and importing it "cannot inherit
go-cask's opinions" ([`internal/build/core/README.md`](../../internal/build/core/README.md)).

Import fails. A consumer module whose own path is outside
`github.com/dmundt/go-cask`, with its own `go.mod`, a `require` and a local
`replace` pointing at the engine:

```text
main.go:6:2: use of internal package github.com/dmundt/go-cask/internal/build/core/verify not allowed
```

Go's internal rule reads the **import path**, not the filesystem: a package under
a path element `internal` is importable only by packages rooted at that element's
parent. `github.com/dmundt/go-cask/internal/build/core/...` therefore belongs to
go-cask alone, a `replace` does not change that, and pkg.go.dev does not serve an
`/internal/` path either. The engine's physical separation — its own module, its
own `go.mod`, standard library only, no `go.sum` — is real and worth keeping; what
is missing is an address anyone else may dial.

### 1.1 What already holds

- `internal/build/core` is a separate Go module with its own tests, fuzz targets
  and READMEs, and imports nothing beyond the standard library.
- Policy reaches it as a parameter almost everywhere: `layers.Layer`,
  `coverage.Policy`, `changes.Rule`, `website.Inventory`, `docs.File`, the gate
  ledger's name and retention, the lane and claim ref prefixes, the pinned tool
  version, the benchmark naming, the example selection, the footer contract.
- The engine is invisible to `go list ./...`, and go-cask's own table compensates:
  the "build engine module" step and the second `go mod tidy` name its directory
  explicitly.

### 1.2 What blocks it

1. **The address.** Module path `github.com/dmundt/go-cask/internal/build/core`
   (`internal/build/core/go.mod`). `internal/build/README.md` states that moving
   the engine to its own repository "changes only that line"; it changes 61 Go
   occurrences in 27 files (47 of them import statements), 87 path strings in 17
   Markdown files, both `go.mod` files, and the generated
   [`package-graph.md`](package-graph.md).
2. **Two go-cask answers inside the engine**, against "engine ships no policy":
   `deps.ForbiddenPath = "cas/codec"` (`core/deps/deps.go:23`), which
   `IsForbidden` reads and which a caller in another module cannot change; and
   `release.CompareBaseURL = "https://github.com/dmundt/go-cask/compare"`
   (`core/release/release.go:25`), which writes go-cask's URL into every release
   note the engine renders.
3. **Host-repository prose in a user-visible finding**: `core/website/website.go`
   (lines 102 and 160) prints `website/AGENT.md, "Examples and links"` in the
   message a reader sees. Doc comments naming go-cask's own files are cheaper but
   are the same habit.
4. **Only the engine is reusable, not the system.** `cmd/buildtool verify`'s step
   list carries go-cask steps and paths — the receipt helper, the cross-platform
   targets, `origin/main` as the benchmark, the `.gocache` skip — so a second
   repository writes its own `main` and its own table either way.
5. **Nothing enforces the property.** No test fails when a host-repository
   literal returns to the engine; it is caught by review, or not at all.
6. **No consumption mechanics.** The engine is consumed by `require` plus a local
   `replace` at `v0.0.0`. It has no tag, no versioning rule of its own and no
   compatibility statement.

## 2. Decision

### 2.1 Name and layout

The engine moves out of `internal/` to a directory whose path matches its module
path, because a subdirectory module is only resolvable and taggable when the two
agree.

**Recommended: `build/engine`, module `github.com/dmundt/go-cask/build/engine`.**

| Option | Module path | Why, or why not |
|---|---|---|
| `build/engine` | `github.com/dmundt/go-cask/build/engine` | The name the documentation already uses ("build engine"), and the directory that groups future build tooling. `build/` is free: `.gitignore` holds `/bin/`, `/site/` and `.gocache/`, and this repository has no output-directory convention to collide with. |
| `engine` | `github.com/dmundt/go-cask/engine` | Shortest import, and the reusable artifact sits at the top level beside `cas/` and `gitlike/` — which is how this repository already advertises its reusable libraries. Costs the word "build", so "engine" reads ambiguously outside a build context. |
| `buildkit` | `github.com/dmundt/go-cask/buildkit` | A distinct brand with no `build/` collision. Invents vocabulary that no existing document uses. |
| own repository | `github.com/dmundt/caskbuild` | The end state if the engine is ever a product of its own. Deferred: it splits one gate across two repositories and needs a history filter, and nothing consumes the engine yet. |

Both in-repository options cost the same edits; the choice is naming, not
mechanics. The recommendation keeps the vocabulary stable and can still be
renamed later while nothing imports it.

### 2.2 What stays, what moves

- **`build/engine/`** — the module: the 18 packages, `go.mod`, the module README,
  and a new `AGENT.md` that travels with it (standard library only, ships no
  policy, every package carries a versioned README, fuzz whatever parses
  repository or tool input).
- **`internal/build/policy/`** — unchanged location. go-cask's tables are private
  answers, and `internal/` is what keeps them unimportable; the engine becoming
  public does not make the policy public.
- **`internal/build/{README,AGENT}.md`** — keep the subtree map, the
  where-a-new-check-goes table and the gate rules, and point at the engine's own
  AGENT.md for the module's rules.
- **`cmd/buildtool/`** — unchanged location and unchanged role: it wires the
  engine to the policy tables.

## 3. Landing L1 — the move

Mechanical, no behavior change.

1. `git mv internal/build/core build/engine`.
2. `build/engine/go.mod`: module path `github.com/dmundt/go-cask/build/engine`.
3. Root `go.mod`: the `require` and the `replace` for the same path.
4. The 47 import statements and the remaining references: 13 files in
   `cmd/buildtool`, 11 in `internal/build/policy`, and three inside the engine
   itself — `depgraph`, `versioning` and `versioning`'s fuzz test import the
   engine by its module path.
5. `policy.Verify().EngineDir` becomes `build/engine`.
6. Path documentation: `docs/index.md`, `docs/AGENT.md`, `docs/specs/AGENT.md`,
   `docs/specs/library-design.md`, `docs/specs/testing-strategy.md`,
   `docs/design/index.md`, `scripts/README.md`, `scripts/AGENT.md`,
   `cmd/buildtool/README.md`, `internal/build/README.md`,
   `internal/build/AGENT.md`, `internal/build/shell/README.md`,
   `internal/build/core/README.md`, `website/AGENT.md` and
   `website/changelog.md`. Every changed versioned file bumps its frontmatter
   `version:` (`docs/AGENT.md` §5). The engine's 18 package READMEs link their
   siblings relatively and need no change; only the module README, which becomes
   `build/engine/README.md`, names the path. The path in `CHANGELOG.md`'s
   released entries stays as written: a released section is a record, not a live
   reference.
7. Regenerate [`docs/design/package-graph.md`](package-graph.md) — the committed
   graph does list the engine's packages, so the move changes it.
8. `build/engine/AGENT.md` new; `internal/build/AGENT.md` reduced to the rules
   that belong to the policy half.
9. One `Added` entry in `CHANGELOG.md` naming the engine module path as
   importable: the capability is the point of the change. If review judges it
   purely internal, the entry drops and nothing else changes.

## 4. Landing L2 — the two leaks

- `deps`: take the forbidden prefix as a parameter —
  `IsForbidden(prefix, path)`, `CheckCodecDeps(prefix, guards, deps)` — with
  go-cask's `"cas/codec"` living in `policy`. The engine's own tests move to a
  neutral prefix and gain one that pins the boundary (`casx/codec` does not
  match).
- `release`: the compare base becomes a caller-supplied value on the notes
  builders, with go-cask's URL in `policy`. `CompareURL(from, to)` keeps its
  shape but takes the base.
- `website`: the finding text stops naming `website/AGENT.md`; the caller
  supplies the remedy string, the way `deps.CodecGuard.Remedy` already does.

## 5. Landing L3 — the property guard

The property is currently kept by review. Landing L3 makes it mechanical, in the
engine so it travels with the module:

- a test that the engine imports only the standard library and its own module
  path, so a stray dependency fails before `go mod tidy` does;
- a test that the engine's package sources carry no host-repository literal —
  `dmundt`, `go-cask`, `cas/`, `gitlike/`, `cmd/` — outside doc comments that
  name the module's own path.

go-cask's side keeps the trap closed: the nested-module gate step and the second
`go mod tidy` are table entries, and `internal/build/policy`'s test that every
engine fuzz target is smoke-fuzzed stays as it is.

## 6. Deferred

- **The step list as data.** If a second repository wants `buildtool verify`
  rather than its own `main`, the gate's steps become an engine table (name,
  documentation-scope flag, argv, check name) and `cmd/buildtool` becomes the
  interpreter. That is a larger change with a real design question — which steps
  a gate may drop — and no consumer asks for it yet.
- **A separate engine repository.** The module path above is what makes that a
  tag-and-move later rather than a rewrite; the history filter and the
  cross-repository lane are the work.
- **The tag scheme is normative.** A subdirectory module is tagged
  `build/engine/vX.Y.Z`, and a future breaking change needs a `/v2` element in
  both the module path and the directory. `docs/specs/versioning.md` gains that
  row when L1 lands, not in this brief.

## 7. Acceptance

L1 is done when all of these hold:

1. A throwaway consumer module, with its own `go.mod` whose module path is
   outside `github.com/dmundt/go-cask`, compiles an import of
   `github.com/dmundt/go-cask/build/engine/verify` through a `require` plus a
   `replace`. This is the probe that fails today.
2. `build/engine` has no non-standard-library import, and `go mod tidy -diff` is
   clean in it.
3. No `internal` element appears anywhere in the engine's module path.
4. `go run ./cmd/buildtool verify` is green with `EngineDir` updated, so the
   engine is still built, vetted and raced by the gate.
5. `gofmt -l` covers both trees, and the engine's suite runs under `-race`.

L2 and L3 each need their own acceptance line in their landing, pinned by a test
in the engine and a policy test on go-cask's side.

## 8. Risks

- **Path churn in documentation.** Twelve Markdown files, each with a frontmatter
  version bump, plus the generated package graph. Mechanical, but the version
  rule makes it the largest part of the diff; a missed bump fails the gate rather
  than shipping.
- **Two AGENT.md files where there was one.** The engine's rules must actually
  move, or the reusable half loses the instruction that keeps it reusable the
  moment it travels.
- **The layer matrix does not see the engine**, before or after. `policy`'s
  imports of the engine resolve outside the root module path and stay
  unclassified, exactly as today; the matrix row for `internal/**` still covers
  `policy` itself.
- **Nothing consumes the engine yet.** The acceptance probe is a throwaway
  module, so the first real consumer may still find friction; the L3 guard is
  what keeps the boundary from decaying in the meantime.
