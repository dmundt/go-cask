---
title: Versioning — go-cask
description: How the go-cask library is versioned with Git — semantic versioning, Go module version rules (v2+ path suffix), tags, branches, changelog, and the release process; clearly distinct from HTTP API versioning and instruction-document versions.
version: v1
---

# Versioning — go-cask

> How the `cas` library (and its `gitlike` example package) is versioned and
> released through Git. The project is currently **pre-release** (no version
> tags exist yet); this document defines the rules from the first tag onward.
>
> Related: `.github/instructions/library-design.instructions.md` §5
> (compatibility policy — the semantic contract this versioning implements),
> `.github/instructions/defaults.instructions.md` §7 (Go baseline),
> `.github/instructions/AGENT.md` §3 (document versions — a different thing,
> see §6).

---

## 1. Versioning Model (Semantic Versioning)

Library versions are `MAJOR.MINOR.PATCH` (semver), applied as Git tags.

| Bump | Trigger                                                          |
| ---- | ---------------------------------------------------------------- |
| MAJOR | Any breaking change to the stable surface (cas-core §7.1) — removed/renamed identifiers, changed semantics, breaking default changes |
| MINOR | Additive features (new identifiers, new options, new backends) — backward compatible |
| PATCH | Bug fixes and behavior corrections within the same contract       |

- The stable surface and the compatibility rules come from
  `library-design.instructions.md` §1/§5 — this document only turns them
  into Git mechanics.
- **Pre-release policy:** while the library is new, breaking changes are
  allowed in `v0.x.y` minor bumps (Go convention). The project may either
  start at `v0.1.0` and reach `v1.0.0` when the stable surface is frozen, or
  go straight to `v1.0.0` on first release. **Decision: first public tag is
  `v1.0.0`** — the docs already commit to the stable surface; treat
  pre-1.0 as private development without tags.

---

## 2. Go Module Versioning Rules

- Module path: `github.com/dmundt/go-cask`; the core lives in the `cas/`
  subpackage (`github.com/dmundt/go-cask/cas`), the example in `examples/gitlike/`.
- **v0/v1**: no path suffix. Tags: `v1.0.0`, `v1.1.0`, …
- **v2 and later**: Go REQUIRES the major version in the module path —
  `github.com/dmundt/go-cask/v2` (module path changes, tags become
  `v2.0.0`, …). Layout decision: keep both majors in one repository by
  mirroring the library under `cas/v2/` (with `go.mod` declaring the `/v2`
  path), so `v1` and `v2` consumers coexist without a fork. The `gitlike`
  example follows the same major as the core it builds on.
- **Untagged commits**: consumers on an untagged commit get a Go
  **pseudo-version** (`v1.2.3-0.20260901120000-abcdef123456`) automatically —
  no action needed; tags are still the contract.
- `go.mod`: `go 1.27` toolchain; library baseline Go 1.21+ (defaults §7).
- Tags MUST be placed on the module root commit (Go resolves versions per
  module; a tag on the wrong commit breaks resolution).

---

## 3. Git Mechanics

- **Tags**: annotated tags (`git tag -a v1.0.0 -m "…"`), pushed with
  `git push --tags`. Tags are **immutable**: never move, delete, or
  re-release a version with different content. If a release is broken, ship
  `vX.Y.Z+1` (PATCH) — never re-tag.
- **Branches**: full naming patterns, examples, and lifecycle rules are in
  `.github/instructions/branch-naming.instructions.md`; the essentials:
  - `main` — default development branch; version tags land here.
  - `release/vX.Y` — created when a minor ships and still needs maintenance;
    PATCH releases for it are tagged on that branch.
  - `hotfix/…` — short-lived branches for urgent fixes, merged to `main`
    (and the open release branch).
- No mutable `latest` tags (that is a Docker registry pattern, not a library
  pattern — see §6).

---

## 4. Commit & Changelog Conventions

- **Commit messages**: Conventional Commits style —
  `feat:`, `fix:`, `docs:`, `refactor:`, `perf:`, `test:`, `chore:`.
  A breaking change MUST add a `BREAKING CHANGE:` footer → MAJOR bump.
  These commit types drive the version-bump decision in §5.
- **CHANGELOG.md**: keep-a-changelog style at the repo root:
  - `## [Unreleased]` section collects changes between releases;
  - on release, `Unreleased` becomes `## [vX.Y.Z] - <date>` and a new empty
    `Unreleased` is opened;
  - group by Added / Changed / Fixed / Removed; note breaking changes
    prominently.

---

## 5. Release Process (simple)

1. **Decide the bump** from the commits since the last tag (§4): any
   `BREAKING CHANGE` → MAJOR; new features → MINOR; fixes only → PATCH.
2. **Verify**: `gofmt -l .` clean, `go vet ./...`, `go test -race ./...`,
   and the performance benchstat gate (performance §5/§11) green on `main`.
3. **Update CHANGELOG.md** (move `Unreleased` → the new version).
4. **Tag**: `git tag -a vX.Y.Z -m "vX.Y.Z"` on the module root commit;
   push branch + tag.
5. **(v2+ only)** update the module path to `…/v2`, publish the `cas/v2/`
   subtree, tag `v2.X.Y`.
6. Create `release/vX.Y` only if the minor needs future maintenance.

---

## 6. What Is NOT Library Versioning

| Versioned thing                    | Version scheme                 | Who bumps it                     |
| ---------------------------------- | ------------------------------ | -------------------------------- |
| The Go library (`cas`/`gitlike`)   | semver Git tags (`v1.2.3`)     | maintainers per §5               |
| The HTTP CAS API (`/api/cas/v1`)   | URL prefix majors (`v1` → `v2`) | independent of library semver (api-design §12) |
| Instruction documents (frontmatter `version: vN`) | `v1`, `v2`, … document revisions | per AGENT.md §3, on material doc changes |

These three version spaces MUST NOT be conflated: a `v2` of the HTTP API does
not imply a `v2` library, and a doc at `version: v3` says nothing about the
library's release.

---

## 7. Checklist

- [ ] First public tag is `v1.0.0` on the module root commit
- [ ] Tags are annotated, immutable, and never re-tagged
- [ ] Bump decided from commits (breaking → MAJOR, feature → MINOR, fix →
      PATCH) with `BREAKING CHANGE` footers
- [ ] `gofmt`/`go vet`/`go test -race`/benchmark gate green before tagging
- [ ] CHANGELOG.md updated on every release; `Unreleased` section maintained
- [ ] v2+ uses the `/v2` module path suffix and the `cas/v2/` layout
- [ ] HTTP API majors and doc versions never conflated with library versions
