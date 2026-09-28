---
type: Specification
title: Versioning — go-cask
description: How the go-cask library is versioned with Git — semantic versioning, Go module version rules (v2+ path suffix), tags, branches, changelog, release notes, and the release process; clearly distinct from HTTP API versioning and instruction-document versions.
version: v23
---

# Versioning — go-cask

Semver Git tags for `cas` and the `gitlike/` reference library. Stable surface frozen at `v1.0.0`;
current line **`v1.3.0`**; shipped `v0.1.0-alpha.1` … `v0.3.0`, `v1.0.0`–`v1.2.0`. Related:
`library-design.md` §1/§5 (compatibility policy), `defaults.md` §7 (Go baseline), `AGENT.md` §3 (doc
versions — different, §6).

## 1. Model (semantic versioning)

`MAJOR.MINOR.PATCH` (semver) as Git tags.

| Bump | Trigger |
|---|---|
| MAJOR | breaking change to the stable surface (cas-core §7.1): removed/renamed identifiers, changed semantics/defaults |
| MINOR | additive features (identifiers, options, backends); backward compatible |
| PATCH | bug fixes and behavior corrections, same contract |

**Ratified exceptions — the only `v1`-line breaking changes.**

| Ver | Change | Ratified because | Breaks |
|---|---|---|---|
| `v1.2.0` | `cas.Hash` interface → concrete value; `cas.HashRef`/`NewHashRef`/`Ref` deleted pre-release; JSON rendering → `cas/codec/json` (`jsoncodec.Hash` field type, gone again in `v1.3.0`); `cas` drops `encoding/json` | Weeks-old surface, negligible adoption; compile-time-only; format unchanged | `h == nil` → `h.IsZero()`, custom `Hash` implementations, `jsoncodec.Hash` reference fields |
| `v1.3.0` (unreleased when ratified) | **Digest change.** Address `cas.Digest`: raw digest bytes, zero = absent, lowercase-hex string; no core algorithm — client injects `cas.Hasher` (`cas/hash/sha256` = default). Gone: `cas.Hash`, `HashBytes`, `ParseHash`, `NewHash`, `CheckHash`, `cas.NewHasher`, `cas.SHA256`, `jsoncodec.Hash` field type (`sha256.NewHasher()` ships); `cas.New`/`gitlike.NewRepository` take the hasher. Same exception, runtime registry removed earlier this cycle (`RegisterHash`/`LookupHash`/`LookupStreamHash`/`HashFunc`, `algo` parameter of `cas.New`/`HashBytes`/`NewHasher`/`NewHash`/`gitlike.NewRepository`, CLI/API selectors) — compile-time-only, same seam | First-cycle grounds | **Stored bytes, on-disk layout:** type names stay `@1`; payloads `"sha256:hexdigest"` → bare hex; `<base>/sha256/…` → `<base>/…`. Prior-build reference object (tree, commit, tag) unreadable: `Get`/`Verify` → `ErrNotFound` (path moved), `List`/`Stats` still report it (cas-core §4.4); canonical-path copy → `ErrCorrupt` (strict hex rejects legacy prefix). Blob (no reference fields) decodes (cas-core §4.12). Deliberate loud break (operations §5): no migration tool, no `@2` type |
| `v1.3.0` (unreleased when ratified) | **gitlike codec/invariant change.** `gitlike.NewRepository(raw, hasher, codecs)` takes the caller's `gitlike.Codecs` (`Blob`/`Tree`/`Commit`/`Tag`): model names no wire format, `package gitlike` imports no codec; `gitlike.Commit.MarshalJSON`/`UnmarshalJSON` gone, required-tree rule now `Commit.Validate()`, core-enforced (`cas.Validator`, cas-core §4.7/§4.8) | Confined to the `gitlike` layer, NOT the stable `cas` surface (library-design §1); `cas` gains an optional contract (`Validator`) and stricter behavior on previously undefined paths (nil object, invariant-violating object written unchecked); fixes a latent codec-coupling bug (gob-backed repo accepted a tree-less commit) | `gitlike` callers; stored JSON unchanged, addresses stable |

- All three MUST carry a `BREAKING CHANGE:` footer and migration note (CHANGELOG + cas-core; gitlike cites §4.12).
- Three first-cycle exceptions, no more: further breaks need a MAJOR (`/v2` mechanics, §2).
- **Release of this cycle: `v1.3.0`** — one MINOR carrying all three.
- **Pre-release policy:** breaking changes allowed in `v0.x.y` minor bumps (Go convention). **Decision: `v0.1.0` first** — public tag `v0.1.0-alpha.1`, then `-alpha.N`/`-beta.N`/`-rc.N` and `v0.1.0`, then `v1.0.0` once cas-core §7.1 freezes. Pre-releases sort below their final release (`v0.1.0-alpha.1` < `v0.1.0`); annotated-tag mechanics (§3, §5).

## 2. Go module versioning rules

| Topic | Rule |
|---|---|
| Module path | `github.com/dmundt/go-cask`; core `cas/` (`.../go-cask/cas`); `gitlike/` (`.../go-cask/gitlike`) |
| v0/v1 | no path suffix; tags `v0.1.0-alpha.1`, `v0.1.0`, `v1.0.0`, … |
| v2+ | Go REQUIRES the major in the module path — `github.com/dmundt/go-cask/v2` (`v2.0.0`, …) |
| Both majors, one repo | mirror under `cas/v2/` (`go.mod` declares `/v2`); v1 and v2 consumers coexist, no fork; `gitlike` follows the core major |
| Untagged commits | Go **pseudo-version** `v1.2.3-0.<timestamp>-<commit>`, automatic; tags still the contract |
| `go.mod` | `go 1.27` toolchain; baseline Go 1.24+ (`omitzero` JSON tag floor) |
| Tag placement | tags MUST be on the module root commit (wrong-commit tag breaks resolution) |

## 3. Git mechanics

| Topic | Rule |
|---|---|
| Tags | annotated (`git tag -a v0.1.0-alpha.1 -m "v0.1.0-alpha.1"`), pushed with `git push --tags` |
| Immutability | never move, delete, or re-release a version with different content; broken release ships `vX.Y.Z+1` (PATCH), never a re-tag |
| Branches | `branch-naming.md` owns the rules; `main` = default dev branch, version tags land here; `release/vX.Y` = created when a minor ships and needs maintenance, PATCH releases tagged there; `hotfix/…` = short-lived, merged to `main` and the release branch |
| `latest` tags | none (a Docker pattern, not a library pattern) |

## 4. Commit and changelog conventions

- **Commits:** Conventional Commits — `feat:`, `fix:`, `docs:`, `refactor:`, `perf:`, `test:`, `chore:`; a breaking change MUST add a `BREAKING CHANGE:` footer → MAJOR; types drive the bump (§5).

**CHANGELOG.md** (keep-a-changelog, root):

| Rule | Detail |
|---|---|
| Scope | User-facing, not a development diary; notable changes only — consumers, CLI users, operators, viewer behavior/security |
| `## [Unreleased]` | One `Unreleased` section plus one per tagged release; collects changes between releases, becomes `## [vX.Y.Z] - <date>` on release, new empty `Unreleased` opens; breaking changes prominent |
| Grouping | `Added` / `Changed` / `Deprecated` / `Removed` / `Fixed` / `Security`, only where notable — no empty heading |
| Combining | Related changes forming one capability → one bullet |
| Wording | Outcome and user impact, not implementation history or commit list |
| Omit | Test-only work, coverage, routine CI or dependency maintenance, no-behavior-change refactors, formatting, release prep, temporary fixes |
| Timing | Update `CHANGELOG.md` before the commit for notable user-visible changes; none for internal/temporary ones |
| Release move | Before a release, move finalized entries from `Unreleased` into the versioned section; keep the compare-link format |

**GitHub release notes** mirror the changelog:

| Rule | Detail |
|---|---|
| Mirror | MUST reproduce the corresponding user-facing `CHANGELOG.md` section, same concise scope |
| Ignore | What the changelog ignored |
| Ending | A `Full Changelog:` link to the tag comparison |
| Older notes | Correct published notes still carrying temporary or trivial material |
| Tool | Render with `go run ./cmd/gate release`, not by hand (`scripts/AGENT.md`, "Releasing") |

## 5. Release process

| # | Step |
|---|---|
| 1 | Bump from commits since the last tag (§4): `BREAKING CHANGE` → MAJOR; features → MINOR; fixes → PATCH. Pre-releases use the version they precede (`v0.1.0-alpha.1`, `v0.1.0-beta.1`, …) |
| 2 | Verify on `main`: `gofmt -l .` clean, `go vet ./...`, `go test -race ./...`; benchmarks reviewed against performance §5/§11 (deliberately **no** CI or `benchstat` gate — performance §5; `benchmarks/data/baseline.txt` a committed, machine-specific dump refreshed by hand, not a threshold) |
| 3 | Update CHANGELOG.md (move `Unreleased` → the new version) |
| 4 | Tag `git tag -a vX.Y.Z -m "vX.Y.Z"` on the module root commit; push branch + tag |
| 5 | (v2+ only) module path → `…/v2`, publish `cas/v2/`, tag `v2.X.Y` |
| 6 | Create `release/vX.Y` only if the minor needs future maintenance |

- Pre-release tags (alpha/beta/rc): same process per tag, own CHANGELOG section.

## 6. v1.0.0 Definition of Done

Every item MUST be satisfied before the first stable release.

### 6.1 Stable surface freeze
- [x] cas-core §7.1 surface enumerated; renames/breaks closed
- [x] Address `cas.Digest`: raw digest bytes, one lowercase-hex text form; `Hash` (`algo:hexdigest`) gone
- [x] Fan-out file names: full-hash only; Git-remainder rejected
- [x] Hash algorithm: none in the core; clients inject a `cas.Hasher`, go-cask wire `sha256` (`cas/hash/sha256`); registry dropped earlier
- [x] GC concurrency: writers lock-free; sweeps exclusive + grace-gated (`--min-age 1h` default)
- [x] Lean-core export budget re-baselined to ~40 (library-design v10)
- [x] Library baseline Go 1.24 (toolchain 1.27; `omitzero` JSON tags)

### 6.2 Release mechanics
- [x] `v0.1.0-alpha.1`, `v0.1.0-alpha.2` tags exist
- [x] `v0.1.0` (first non-prerelease) before `v1.0.0`
- [x] CHANGELOG covers all changes since the last tag
- [x] `gofmt -l .` clean, `go vet ./...`, `go test -race ./...` green
- [x] Doc-integrity gate passes (mermaid balance, `.md` refs)

### 6.3 Spec compliance
- [x] All 20 instruction specs' acceptance checklists triaged (2026-09 audit); unimplemented items unticked, catalogued in extensions §3 (what exists, what does not)
- [x] Recorded decisions have provenance in owning specs, with version bumps

### 6.4 Examples
- [x] Four runnable examples (`files`, `artifacts`, `notes`, `api`) plus `gitlike`; `examples/viewer` = product viewer in `internal/web/`
- [x] Each example README.md: `cas core parts used`, code walkthrough, mermaid diagram

### 6.5 Viewer
- [x] Viewer is byte-layer, never imports `examples/` (coding-guidelines §9)
- [x] Sessions, CSRF, roles, rate limiting, audit logging implemented (viewer-security checklist)
- [x] `cask web` starts the viewer, prints URL + token, auto-opens browser (`--no-open` suppresses)
- [ ] GC template (`gc.html`) absent — no `gc.html`, no GC route; templates `login.html`, `objects.html`, `partials.html`; removal CLI-only (consistency §9, viewer-design §5, extensions §3)
- [x] raw-HTML fragments converted to templates

### 6.6 Extensions
- [x] Extension catalog (extensions.md §3) records deferrals with triggers
- [x] Cache/recipe helpers (`SmartCache`, `CacheMonitor`) stay inlined as per-example teaching code — shared home at second consumer

## 7. What is NOT library versioning

| Versioned thing | Scheme | Who bumps |
|---|---|---|
| The Go library (`cas`/`gitlike`) | semver Git tags (`v1.2.3`) | maintainers per §5 |
| An example JSON surface (`examples/api` pattern) | URL majors (`/api/cas/v1` → `/api/cas/v2`) | independent of library semver (api-design §12) |
| Instruction documents (frontmatter `version: vN`) | `v1`, `v2`, … revisions | per AGENT.md §3, on material doc changes |

- The three MUST NOT be conflated: an example JSON `v2` implies no `v2` library; `version: v3` says nothing about the library.

## 8. Checklist

- [x] First public tag: pre-release `v0.1.0-alpha.1` on the module root commit; `v1.0.0` once the surface freezes
- [x] Tags annotated, immutable, never re-tagged
- [x] Bump decided from commits (`BREAKING CHANGE` → MAJOR, feature → MINOR, fix → PATCH)
- [x] `gofmt`/`go vet`/`go test -race`/benchmark gate green before tagging
- [x] CHANGELOG.md updated on every release; `Unreleased` maintained
- [x] v2+: `/v2` module path suffix, `cas/v2/` layout
- [x] Example-surface majors, doc versions never conflated with library versions
