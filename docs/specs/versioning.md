---
type: Specification
title: Versioning — go-cask
description: How the go-cask library is versioned with Git — semantic versioning, Go module version rules (v2+ path suffix), tags, branches, changelog, and the release process; clearly distinct from HTTP API versioning and instruction-document versions.
version: v17
---

# Versioning — go-cask

How the `cas` library (and the `gitlike/` reference library) is versioned and released through Git. The project is **pre-release**: the first public tag is `v0.1.0-alpha.1`, heading toward `v1.0.0` once the stable surface is frozen. Related: `library-design.md` §5 (the compatibility policy this implements), `defaults.md` §7 (Go baseline), `AGENT.md` §3 (document versions — a different thing, §6).

## 1. Model (semantic versioning)

Library versions are `MAJOR.MINOR.PATCH` (semver), applied as Git tags.

| Bump | Trigger |
|---|---|
| MAJOR | Any breaking change to the stable surface (cas-core §7.1) — removed/renamed identifiers, changed semantics, breaking default changes |
| MINOR | Additive features (new identifiers, new options, new backends) — backward compatible |
| PATCH | Bug fixes and behavior corrections within the same contract |

- The stable surface and compatibility rules come from `library-design.md` §1/§5 — this doc only turns them into Git mechanics.
- **Ratified exception (v1.2.0):** one breaking change shipped inside the `v1` line — `cas.Hash` went from an interface to a concrete value type, deleting `cas.HashRef`/`NewHashRef`/`Ref` before any of them were released, and hash JSON rendering moved out of the core into the JSON codec (`cas/codec/json`'s `jsoncodec.Hash` field type), so `cas` no longer imports `encoding/json`. It was accepted because the surface was weeks old with negligible adoption, the break is compile-time only (`h == nil` → `h.IsZero()`, custom `Hash` implementations, `jsoncodec.Hash` reference fields), and the on-disk format is unchanged. It MUST carry a `BREAKING CHANGE:` footer and a migration note (CHANGELOG + cas-core). It is the first of the three recorded first-cycle exceptions; beyond them, every breaking change needs a MAJOR with the `/v2` mechanics in §2.
- **Ratified exception (unreleased, after `v1.2.0`): the digest change.** The address type is now `cas.Digest` — raw digest bytes whose zero value is the absent reference, rendered as one lowercase-hex string — and the core names no hash algorithm: the client injects a `cas.Hasher` (`cas/hash/sha256` ships go-cask's default). `cas.Hash`, `HashBytes`, `ParseHash`, `NewHash`, `CheckHash`, `NewHasher`, `SHA256` and the JSON codec's `jsoncodec.Hash` field type are gone, and `cas.New`/`gitlike.NewRepository` take the hasher. It was accepted on the same first-cycle grounds, but unlike the earlier one it **changes stored bytes and the on-disk layout**: object type names stay `@1`, yet reference payloads went from `"sha256:hexdigest"` to bare hex, and `<base>/sha256/…` became `<base>/…`. So an object written by the previous build cannot be read by the new one at all — `Get`/`Verify` return `ErrNotFound` for the old digests (the path moved) while `List`/`Stats` still report them (cas-core §4.4) — and an object copied to its canonical path then fails to decode with `ErrCorrupt`, because strict hex parsing rejects the legacy prefix. A deliberate loud break (operations §5) with no migration tool and no `@2` type. It MUST carry a `BREAKING CHANGE:` footer and a migration note (CHANGELOG + cas-core). The same exception covers the removal of the runtime algorithm registry that immediately preceded it in this cycle (`RegisterHash`/`LookupHash`/`LookupStreamHash`/`HashFunc`, the `algo` parameter of `cas.New`/`HashBytes`/`NewHasher`/`NewHash`/`gitlike.NewRepository`, and the CLI/API algorithm selectors): a compile-time-only break on the same seam, accepted on the same grounds. Together with the `v1.2.0` concrete-`Hash` change these are the library's recorded breaking changes.
- **Ratified exception (unreleased, after `v1.2.0`): the gitlike codec/invariant change.** `gitlike.NewRepository(raw, hasher, codecs)` now takes the caller's `gitlike.Codecs` set (`Blob`/`Tree`/`Commit`/`Tag`), so the reference model names no wire format and `package gitlike` imports no codec package; `gitlike.Commit.MarshalJSON`/`UnmarshalJSON` are gone, their required-tree rule now expressed as `Commit.Validate()` and enforced by the core (`cas.Validator`, cas-core §4.7/§4.8). It was accepted because the break is **confined to the `gitlike` reference layer**, which is explicitly NOT part of the stable `cas` surface (library-design §1) — `cas` itself only gains an optional contract (`Validator`) plus stricter behavior on paths that were previously undefined (a nil object, an invariant-violating object written unchecked) — and because the rule it fixes was a latent codec-coupling bug (a gob-backed repository accepted and returned a tree-less commit). Stored JSON payloads are unchanged, so addresses are stable within this model. It MUST carry a `BREAKING CHANGE:` footer and a migration note (CHANGELOG + cas-core §4.12). This is the third and final recorded first-cycle exception: **any further breaking change follows the ordinary rule again** — a MAJOR with the `/v2` mechanics in §2.
- **Release of this cycle: `v1.3.0`.** Both unreleased breaking changes (the digest change and the gitlike codec/invariant change) ship together as one MINOR that carries the ratified exceptions above, with the `BREAKING CHANGE` footers and migration notes they require.
- **Pre-release policy:** breaking changes are allowed in `v0.x.y` minor bumps (Go convention). The project may start at `v0.1.0` and reach `v1.0.0` when the stable surface is frozen, or go straight to `v1.0.0`. **Decision: start at `v0.1.0`** — first public tag is the pre-release `v0.1.0-alpha.1`, then further pre-releases (`-alpha.N`, `-beta.N`, `-rc.N`) and `v0.1.0`, then `v1.0.0` when cas-core §7.1 is frozen. Pre-release tags sort below their final release (`v0.1.0-alpha.1` < `v0.1.0`) and use the same annotated-tag mechanics (§3, §5).

## 2. Go module versioning rules

- Module path: `github.com/dmundt/go-cask`; core in `cas/` subpackage (`.../go-cask/cas`); the `gitlike/` reference library (`.../go-cask/gitlike`).
- **v0/v1:** no path suffix. Tags `v0.1.0-alpha.1`, `v0.1.0`, `v1.0.0`, …
- **v2+:** Go REQUIRES the major in the module path — `github.com/dmundt/go-cask/v2` (tags become `v2.0.0`, …). Layout: keep both majors in one repo by mirroring the library under `cas/v2/` (its `go.mod` declares the `/v2` path), so v1 and v2 consumers coexist without a fork. `gitlike` follows the same major as the core it builds on.
- **Untagged commits:** consumers get a Go **pseudo-version** (`v1.2.3-0.<timestamp>-<commit>`) automatically — no action needed; tags are still the contract.
- `go.mod`: `go 1.27` toolchain; library baseline Go 1.24+ (the `omitzero` JSON tag floor).
- Tags MUST be on the module root commit (a wrong-commit tag breaks resolution).

## 3. Git mechanics

- **Tags:** annotated (`git tag -a v0.1.0-alpha.1 -m "v0.1.0-alpha.1"`), pushed with `git push --tags`. Immutable — never move, delete, or re-release a version with different content; if a release is broken, ship `vX.Y.Z+1` (PATCH), never re-tag.
- **Branches:** full rules in `branch-naming.md`; essentials: `main` = default dev branch, version tags land here; `release/vX.Y` = created when a minor ships and still needs maintenance, PATCH releases tagged there; `hotfix/…` = short-lived, merged to `main` (and the open release branch).
- No mutable `latest` tags (a Docker pattern, not a library pattern).

## 4. Commit & changelog conventions

- **Commits:** Conventional Commits — `feat:`, `fix:`, `docs:`, `refactor:`, `perf:`, `test:`, `chore:`. A breaking change MUST add a `BREAKING CHANGE:` footer → MAJOR. These types drive the bump decision (§5).
- **CHANGELOG.md** (keep-a-changelog, repo root): `## [Unreleased]` collects changes between releases; on release it becomes `## [vX.Y.Z] - <date>` and a new empty `Unreleased` is opened; group by Added / Changed / Fixed / Removed; note breaking changes prominently.

## 5. Release process

1. Decide the bump from commits since the last tag (§4): any `BREAKING CHANGE` → MAJOR; new features → MINOR; fixes only → PATCH. Pre-releases use the version of the release they precede (`v0.1.0-alpha.1`, `v0.1.0-beta.1`, …).
2. Verify on `main`: `gofmt -l .` clean, `go vet ./...`, `go test -race ./...`, and the performance benchstat gate green (performance §5/§11).
3. Update CHANGELOG.md (move `Unreleased` → the new version).
4. Tag `git tag -a vX.Y.Z -m "vX.Y.Z"` on the module root commit; push branch + tag.
5. (v2+ only) update the module path to `…/v2`, publish the `cas/v2/` subtree, tag `v2.X.Y`.
6. Create `release/vX.Y` only if the minor needs future maintenance.

Pre-release tags (alpha/beta/rc) follow the same process — annotated, immutable, on the module root commit — each with its own CHANGELOG section. The process runs per tag, not per final version only.

## 6. v1.0.0 Definition of Done

Every item MUST be satisfied before the first stable release.

### 6.1 Stable surface freeze
- [x] cas-core §7.1 stable surface enumerated; renames/breaking changes closed
- [x] Address naming: `cas.Digest` — raw digest bytes, one lowercase-hex text form; the earlier `Hash` (`algo:hexdigest`) name is gone
- [x] Fan-out file-name style: full-hash names only; Git-remainder option rejected
- [x] Hash algorithm: none named by the core — the client injects a `cas.Hasher`, and go-cask's own clients wire `sha256` (`cas/hash/sha256`); the runtime registry was dropped earlier
- [x] GC concurrency: writers lock-free, maintenance sweeps exclusive + grace-gated (`--min-age 1h` default)
- [x] Lean-core export budget re-baselined to ~40 (library-design v10)
- [x] Library baseline declared Go 1.24 (toolchain 1.27; `omitzero` JSON tags)

### 6.2 Release mechanics
- [x] `v0.1.0-alpha.1` and `v0.1.0-alpha.2` tags exist
- [x] `v0.1.0` (first non-prerelease) tagged before `v1.0.0`
- [x] CHANGELOG captures all changes since the last tag
- [x] `gofmt -l .` clean, `go vet ./...`, `go test -race ./...` green
- [x] Doc-integrity gate passes (mermaid balance, `.md` refs)

### 6.3 Spec compliance
- [x] All 20 instruction specs' acceptance checklists fully ticked (all audit items triaged 2026-09)
- [x] All recorded decisions have provenance in their owning specs with version bumps

### 6.4 Examples
- [x] Four runnable examples exist (`files`, `artifacts`, `notes`, `api`) plus `gitlike`; `examples/viewer` covered by the product viewer in `internal/web/`
- [x] Each example has a README.md with `cas core parts used`, code walkthrough, and mermaid diagram

### 6.5 Viewer
- [x] Viewer is a byte-layer tool, never imports `examples/` (coding-guidelines §9)
- [x] Sessions, CSRF, role checks, rate limiting, audit logging implemented (viewer-security checklist)
- [x] `cask web` starts the viewer, prints URL + token, auto-opens browser (`--no-open` suppresses)
- [x] GC template exists (`gc.html`); raw-HTML fragments converted to templates

### 6.6 Extensions
- [x] Extension catalog (extensions.md §3) records deferral decisions with triggers
- [x] Cache/recipe helpers (`SmartCache`, `CacheMonitor`) stay inlined as per-example teaching code — shared home only when a second consumer exists

## 7. What is NOT library versioning

| Versioned thing | Scheme | Who bumps |
|---|---|---|
| The Go library (`cas`/`gitlike`) | semver Git tags (`v1.2.3`) | maintainers per §5 |
| An example JSON surface (`examples/api` pattern) | URL prefix majors (`/api/cas/v1` → `/api/cas/v2`) | independent of library semver (api-design §12) |
| Instruction documents (frontmatter `version: vN`) | `v1`, `v2`, … document revisions | per AGENT.md §3, on material doc changes |

These three MUST NOT be conflated: an example JSON `v2` does not imply a `v2` library; a doc at `version: v3` says nothing about the library release.

## 8. Checklist

- [x] First public tag is the pre-release `v0.1.0-alpha.1` on the module root commit; `v1.0.0` follows once the stable surface is frozen
- [x] Tags are annotated, immutable, never re-tagged
- [x] Bump decided from commits (breaking → MAJOR, feature → MINOR, fix → PATCH) with `BREAKING CHANGE` footers
- [x] `gofmt`/`go vet`/`go test -race`/benchmark gate green before tagging
- [x] CHANGELOG.md updated on every release; `Unreleased` maintained
- [x] v2+ uses the `/v2` module path suffix and the `cas/v2/` layout
- [x] Example-surface majors and doc versions never conflated with library versions
