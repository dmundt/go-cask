# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Released: `v0.1.0-alpha.1` … `v0.3.0`, `v1.0.0`–`v1.3.0`. The stable
`cas` surface is frozen; the `v1.x` line carries the three ratified
first-cycle exceptions recorded in `versioning.md` §1.

## [Unreleased]

### Documentation

- Reworked the `cas` subtree READMEs to a consistent package style: short
  package summaries, direct implementation links, and explicit policy notes.
- Added the package-local [cas/AGENT.md](cas/AGENT.md) guide with documentation
  rules, default policy wording, and README-link conventions for the `cas`
  subtree.
- Kept the root and layer docs aligned on the same policy: the `cas` core stays
  generic and algorithm-agnostic, while the project recommends `SHA-256` + JSON
  for durable storage and `SHA-512/256` as a fast secure alternative.
- Clarified the compatibility role of `cas/codec/gob`, documenting it as Go-only
  and opt-in rather than the default or canonical long-term CAS format.
- Documented MD5 and SHA-1 as legacy or migration-only choices, not new CAS
  defaults.
- Added direct README links down to the concrete subpackages (`fs`, `mem`,
  `lru`, `prefetch`, `json`, `gob`, `sha256`, `sha512_256`) and back to the
  parent package docs.
- Renamed the shared backend config shim to [cas/backend/options.go](cas/backend/options.go)
  to match the actual option-based API and clarified in the backend docs that
  each backend defines its own `With...` functions over the shared
  `backend.Option` contract.
- Added the `cas/hash/sha512_256` package and updated the benchmark scale probes
  to compare `sha256` and `sha512_256` in the same benchmark family.

## [v1.3.0] - 2026-09-10

This release is a MINOR that carries the library's recorded first-cycle breaking
changes (versioning §1). The breaks below were ratified
individually — dropping the runtime algorithm registry, the digest change on the
first-cycle grounds (`cas.Hash` → the hash-agnostic `cas.Digest` +
client-injected `cas.Hasher`, which the registry removal belongs to), and the
gitlike change because it is confined to the `gitlike` reference layer, which is
NOT part of the stable `cas` surface (`library-design` §1). They are the last:
any further breaking change takes the ordinary MAJOR route with the `/v2` module
mechanics.

**BREAKING: the core is now hash-agnostic — `cas.Hash` is gone, replaced by
`cas.Digest` (raw digest bytes), and the client owns the algorithm.** The core
names no algorithm, implements none, and cannot tell one digest width from
another: it stores whatever digest the injected `cas.Hasher` returns. This is
the OCI/Docker split (the storage layer keys blobs by an opaque digest; the
algorithm lives with the client), combined with Git's model for a repository
(one object format per store). Go-cask's own clients wire the shipped
`cas/hash/sha256` hasher.

**Stored reference payloads changed and are not migrated**: a reference field is
now one lowercase-hex string (`"ab12…"`) instead of `"sha256:ab12…"`, and the
layout lost its algorithm directory (`<base>/aa/<hex>`, not
`<base>/sha256/aa/<hex>`). Object type names stay `@1` (no new major), so an
reference-bearing object stored before this change (every tree, commit and
tag) cannot be read by this build: `Get`/`Verify` return `ErrNotFound` for
the old addresses (the path moved) while `List`/`Stats` still report them,
and one copied to its canonical path fails to decode with `ErrCorrupt`, because
`Digest.UnmarshalText` is strict and rejects the legacy `sha256:` prefix. An
object with no reference fields — a blob — still decodes (cas-core §4.12). There is no migration tool: a store written by `v1.2.0`
must be re-written by the old build if its objects are still needed.
**The store directory is exclusively its own**: `List`/`Stats` report any
digest-named file beneath the base at any depth and `Clean` reclaims any `*.tmp`
beneath it, so a base must not contain another store (an old
`<base>/<algo>/…` tree included) or an app's scratch temp files. Several stores
under one root are `fs.New(filepath.Join(root, name))` — there is deliberately no
`fs.WithNamespace` option (cas-core §4.4, extensions §3).

**Also breaking: `gitlike` names no codec, and object invariants moved into the
core.** `gitlike.NewRepository` now takes the caller's codec set
(`gitlike.Codecs{...}`), so the package imports no codec package at all and a
repository works over any format — JSON, gob, gzip-wrapped, or a
caller-supplied codec. Object invariants are no longer expressed as codec
methods: a type declares `Validate() error` (`cas.Validator`) and the store
calls it before encoding on `Put` and after decoding on `Get`, so the invariant
holds under every codec. This fixes a latent bug: `gitlike.Commit`'s
required-tree rule lived in `MarshalJSON`/`UnmarshalJSON`, which silently
stopped applying the moment a client picked a non-JSON codec. Stored JSON
payloads are unchanged, so addresses are stable *within* this model.

### Added

- **`cas.Digest`** — the content address as raw digest bytes (zero value = the
  absent reference), with `NewDigest`, `ParseDigest`, `CheckDigest`, `IsZero`,
  `Equal`, `Bytes`, `String`, `Prefix` and `MarshalText`/`UnmarshalText`.
- **`cas.Digest.Prefix(n)`** — the short display form: the first `n` hex
  characters (the viewer uses `Prefix(8)`). It is total — absent and `n <= 0`
  give `""`, a digest whose hex form is shorter than `n` is returned whole, and
  nothing panics or errors — so the core owns the one short-form helper the
  clients used to duplicate.
- **`cas.Hasher`** — the client-supplied algorithm seam
  (`Digest(io.Reader) (Digest, error)` + `Validate(Digest) error`), injected into
  `cas.New`; the core names no algorithm.
- **`cas.Validator`** — the optional object-invariant contract
  (`Validate() error`), enforced by `Store.Put`/`PutDedup` (before encoding) and
  `Store.Get` (after decoding).
- **`cas/hash/sha256`** — the shipped client hasher (`New`, `NewHasher`, `Of`,
  `Parse`, `Format`, `Name`, `Size`), in its own package so `cas` never imports
  an algorithm.
- **`gitlike.Codecs`** — the injected per-type codec set
  (`NewRepository(raw, hasher, codecs)`), so the reference model names no wire
  format and `package gitlike` imports no codec package (CI-enforced).
- **`Commit.Validate`/`Tree.Validate`/`TreeEntry.Validate`/`Tag.Validate`** — the
  per-type invariants, now enforced by the core instead of by JSON methods.
- **Ten runnable `Example` functions** (executable documentation with pinned
  output), including `ExampleWalkGraph` and `ExampleCodec`.

### Changed

- **`cas.Digest` replaces `cas.Hash`.** A `Digest` is `[]byte` holding the raw
  digest; the zero value is the absent reference. `NewDigest`, `ParseDigest`
  (non-empty lowercase hex), `CheckDigest`, `IsZero`, `Equal`, `Bytes`,
  `String` (hex, no algorithm prefix), and `MarshalText`/`UnmarshalText`
  (so `encoding/json` — and any codec that honors `encoding.TextMarshaler` —
  stores a reference as one hex string with no per-type JSON code).
- **The client's `Hasher` is injected**: `cas.Hasher` is
  `Digest(io.Reader) (Digest, error)` plus `Validate(Digest) error`, and
  `cas.New(raw, codec, hasher)` takes it (no error return). Every key argument
  is guarded by `CheckDigest` + `hasher.Validate`, so a wrong-width key is still
  rejected at the store boundary.
- **`cas/hash/sha256` is the shipped client hasher**: `New()`, `NewHasher()`,
  `Of`, `Parse` (accepts `"sha256:hex"` or bare hex), `Format` (renders
  `"sha256:hex"`), `Name`, `Size`. The `cas` package imports nothing from it.
- **The byte layer is keyed by digest only**: `Backend.List(ctx)` lost its
  algorithm filter, `cas.Stats` keeps only `ObjectCount`/`TotalSize` (a
  per-algorithm breakdown is impossible — the core cannot know which algorithm
  produced a key), and `fs.Backend.Verify(ctx, d, hasher)` recomputes through
  the injected hasher.
- **The filesystem layout lost the algorithm directory**:
  `<base>/<fan-out dirs>/<full hex digest>` instead of `<base>/<algo>/…`.
- **`gitlike.NewRepository(raw, hasher)`** became
  **`gitlike.NewRepository(raw, hasher, codecs)`**, and its reference fields are
  `cas.Digest`. The injected `gitlike.Codecs{Blob, Tree, Commit, Tag}` set means
  `package gitlike` imports no codec package and the object model is
  format-agnostic; call sites wire the JSON codecs explicitly
  (`gitlike.Codecs{Blob: jsoncodec.New[*gitlike.Blob](), …}`), since no
  convenience package hides the choice.
- **`cas.Validator` is the invariant contract**: a type with `Validate() error`
  is checked by `Store.Put`/`Store.PutDedup` before encoding (failing as
  `cas: put: <err>`) and by `Store.Get` after decoding (failing as
  `ErrCorrupt`). `GetRaw` never validates. A nil object is rejected on `Put`,
  and a payload that decodes to a nil object is `ErrCorrupt`.
- `gitlike.Commit` implements `Validate()` (a commit must name a tree) instead
  of relying on JSON methods, so the rule holds under any codec.
- Sentinels: `ErrInvalidDigest` and `ErrDigestMismatch` replace `ErrInvalidHash`
  and `ErrHashMismatch`; `ErrUnknownAlgorithm` is gone.
- The viewer reports the addressing model instead of a per-algorithm table, and
  its objects page lost the algorithm filter; the CLI drops `put -algo` and
  `list --algo`, prints `sha256:hexdigest` (via `sha256.Format`), and reports
  `"algorithm": "sha256"` as the client's constant. `examples/api` lost its
  `algo` parameter and its `algorithm_counts` stats field.

### Removed

- **`cas.Hash`, `cas.ParseHash`, `cas.NewHash`, `cas.HashBytes`,
  `cas.NewHasher`, `cas.CheckHash`, `cas.SHA256`** — the algorithm-agnostic core
  has no address type that carries an algorithm.
- **The JSON codec's hash field type `jsoncodec.Hash`** (`NewHash`, `Hash()`,
  `MarshalJSON`, `UnmarshalJSON`): a `cas.Digest` field renders itself, so the
  codec needs no hash type and `gitlike` imports no codec package.
- **`cas.RegisterHash`, `cas.LookupHash`, `cas.LookupStreamHash` and the
  `cas.HashFunc` type** — there is no algorithm registry, no mutexed map, no
  init-order coupling, and no one-shot/streaming duality. (These were removed
  earlier in the v1.3.0 cycle; they are listed here because the whole change
  ships together.) **Migration:** a `RegisterHash`/`HashFunc` call site, the
  CLI's `put -algo`/`list --algo` and the API's `POST ?algo=` are gone because
  the algorithm is no longer a runtime choice at all — it is the injected
  `cas.Hasher`, so those call sites move to `cas.New(raw, codec, sha256.New())`
  (see the digest change above; the fixed-algorithm API that briefly replaced
  the registry in this cycle was itself removed by it).
- **`gitlike.Commit.MarshalJSON`/`gitlike.Commit.UnmarshalJSON`** — the
  required-tree rule is now `Commit.Validate()`, enforced by the core, so it
  survives a codec change instead of disappearing with the JSON codec.
- **`sha256.Short`** — replaced by `cas.Digest.Prefix(n)`, so the short form is
  defined once, in the core. The whole `cas/hash/sha256` package is new in this
  cycle and had not been released yet, so nothing that ever shipped loses an
  API; `gitlike`'s unexported `shortDigest` and the viewer's wrapper are gone
  the same way, and `examples/files`'s `short()` (which actually rendered the
  full `sha256:hexdigest` form) is renamed `printable()`.
- **`examples/artifacts/hasher.go`** — the `sha256double` custom-algorithm seam.
  The example keeps its custom `Codec[T]` (gzip) seam and now stores under
  `sha256`.
- The `go 1.24` floor stays: object fields still use `omitzero`, and an older
  standard library would emit `""` instead of omitting an absent reference.

### Internal

- **`gitlike`'s codec-agnosticism is now enforced, not assumed.** A CI gate
  fails the build if `go list -deps ./gitlike` contains a codec package (the
  codec is injected through `gitlike.Codecs`), because grepping the directory is
  misleading: the package's *production* graph contains no codec at all (`cas`,
  `cas/cache/lru` and stdlib only — `TestRepositoryWithAnotherCodec` runs the
  whole model over gob), while `_test.go` files must inject one, and the shipped
  JSON codec is the right one there because the documented wire bytes are JSON
  and that is what the address pins assert. `cas-core` §4.12 and
  `gitlike/README.md` state the rule and what the `json:"…"` tags really are:
  hints for codecs that honor them, carrying the wire field names *and* which
  references are optional (`omitzero` on `TreeEntry.Hash`/`Commit.Parent`).
  Those tags stay — optionality is a model fact with no codec-neutral spelling
  in Go, and inferring it would change `Tag.Target`'s absent shape (new bytes,
  new addresses, i.e. a MAJOR).

- **Identifiers that hold a digest are now named `…Digest`/`…digest`** where the
  old name was a leftover from the removed `cas.Hash`: `hashPath` → `digestPath`
  (fs), `shortHash` → `shortDigest` and `hashWithType` → `digestWithType`
  (viewer + `gitlike`), `parseHash`/`parseHashLines` → `parseDigest`/
  `parseDigestLines` (viewer), `objectRow.Hash` → `Digest`,
  `hashWithType` → `digestWithType`, `test.HashData` → `test.DigestData`,
  `parseHashParam` → `parseDigestParam` (example API), `auditRow.hash` →
  `digest`, plus the prose/comments that called a digest a hash. No exported
  identifier changed, no stored byte changed, and no behavior changed.
- Deliberately **not** renamed: `cas.Hasher` (the algorithm seam),
  `cas/hash/sha256`, `NewHasher` (stdlib `hash.Hash`), "hash-on-write", the
  `{hash}` route/CLI params and `"hash"` JSON keys/UI labels (the user-facing
  word), and `gitlike.TreeEntry.Hash` — its `json:"hash,omitzero"` tag is a
  **stored payload key**, so renaming the tag would re-address every tree while
  commits still point at the old digests; the Go field rename (tag unchanged,
  so no data change) is scheduled with the v2 module move.
- **`internal/web` has two dead helpers**: the `shortDigest` FuncMap entry is
  registered but no template calls it (templates use the precomputed
  `objectRow.Short` field), and `digestWithType` has no caller at all — the
  `docs/specs/viewer-design.md` §4 helper list (`humanSize`, `byteSize`, …)
  still describes a FuncMap the viewer does not build. Left as-is (renamed only)
  rather than wired up or deleted, pending a decision.
- `docs/specs/AGENT.md` §6 now carries the **`hash` vs `digest`** glossary row
  that makes the surviving `hash` names intentional rather than debt, plus the
  **`examples/` vs `Example` functions** row (runnable programs vs executable
  godoc docs) so the ten Example functions are not mistaken for duplicates of
  the examples tree.
- **The `gitlike` Examples are consumer-facing now**: the file moved to
  `package gitlike_test` (external) and each Example spells out its own
  `gitlike.Codecs{...}` set instead of calling the test-only `jsonCodecs()`
  helper — a rendered Example that names a private helper is not copy-pasteable,
  which defeats its purpose. Two Examples were added for the most-copied doc
  flows: `ExampleWalkGraph` (blob → tree → commit → tag → `ResolveTag`/
  `ResolveCommit`/`ResolveTree`/`ResolveBlob` → `WalkGraph`) and `ExampleCodec`
  (a gzip `Codec[T]` wrapper over both the memory and fs backends, pinning that
  the address is backend-independent).
- **Writing `ExampleWalkGraph` found a runtime bug in the documented usage
  snippet**: `AGENTS.md` resolved a *tag* digest with `Resolver.ResolveCommit`,
  which fails (`tag@1` != `commit@1`) and then dereferenced the nil result. The
  compile-only check used in the previous cycle could not catch it, because the
  snippet ignores errors with `_`. AGENTS.md now walks `ResolveTag(tagHash)` →
  `Target` → `Tree` → entry `Hash`, and states why.

### Fixed

Pre-tag audit of the cycle above; every item was reproduced with a test before
being fixed and now has one.

- **`fs.Backend` panicked on a key shorter than the layout.** `digestPath` sliced
  the digest's hex form with no bound, so a key with fewer than
  `FanOut × FanLevels` hex chars — legal for a *client* hasher, since the core
  names no algorithm — crashed `Put`/`Get`/`Exists`/`Delete`/`Size` with
  `slice bounds out of range`. v1.2.0 clamped it; the clamp was dropped on the
  (now false) assumption of a fixed 32-byte digest. Every key-taking method now
  runs `checkKey` (present + long enough for the layout) and reports
  `ErrInvalidDigest`, and `GC`/`Prune` skip names the layout cannot address, so a
  stray short digest-named file can no longer crash `cask gc`/`prune`.
- **`Store.Put(nil)` panicked when `T` was an interface type.** `isNilValue`
  inspected only non-invalid reflections, so a nil interface value fell through
  to "not nil" and the core dereferenced it; it now treats `reflect.Invalid` as
  absent.
- **`Store.Put` accepted an unversioned `Type()`.** The envelope reader appends
  `@1` to a legacy unversioned name, so the write succeeded and every read then
  failed the type check (`"legacy@1" != "legacy"`) — a write-only object. `Put`
  now rejects a name without `@`, alongside the empty-name case.
- **`gitlike.WalkGraph` revisited shared subgraphs exponentially and could not
  terminate on a crafted store.** It now carries a visited set and an explicit
  stack like `cas.Walker[T]`: a 12-level diamond costs 13 visits instead of
  8191, and two hand-written trees that reference each other terminate.
- **Short digests panicked the display helpers.** `sha256.Short` and `gitlike`'s
  `shortDigest` sliced `[:8]`; that logic now lives in the core's total
  `cas.Digest.Prefix` (below), which those two helpers were replaced by.
- **`mem.Backend.Put` ignored cancellation during the read** — a canceled `Put`
  still buffered and stored the whole object. It now reads through a
  context-checking reader, matching `fs`.
- **`memory.CachedStore` error handling.** `PreloadRecursive` aborted on the
  first reference it could not decode (a commit's tree is another store's type),
  so a `Preloader` never reached a parent commit; foreign-type and dangling
  references are now skipped. `Warmup` swallowed every error including
  `context.Canceled`; it still tolerates missing objects and now reports the
  rest.
- **`cas.Walker[T].Walk` failed on an absent reference.** A zero `Digest` in
  `References()` (documented as "no reference") was looked up and returned
  `ErrInvalidDigest`, failing the whole walk; it is now skipped.
- **`cask list` failed on a stray digest-named file.** `List` reports such a file
  but `Size` on it returns `ErrNotFound`, which aborted the command; the entry is
  now skipped with a stderr warning (`ErrInvalidDigest` likewise).
- **`internal/web` did not audit-log `verify`** while `delete`/`gc` did,
  contradicting `viewer-security` ("all admin actions audit-logged"); every admin
  fragment now logs its outcome. The two unreferenced viewer helpers
  (`digestWithType`, `parseDigestOrNil`) were deleted, and `cask web`'s usage
  line now lists the `-no-open` flag it defines.
- **Documentation corrections** (details in the Docs paragraph below): the
  release notes overstated the break (blobs still decode), the `cas-core` claim
  that the wrong resolver is a *compile-time* error was wrong (it is a runtime
  `ErrUnknownType`), the `viewer-design`/`frontend-architecture` helper, template
  and htmx lists described a viewer that does not exist, `object-versioning`
  described a `RegisterType` registry that does not exist, `consistency` claimed
  the viewer exposes prune, `docs/index.md` pointed at three non-existent files,
  `AGENT.md` named the removed `cas.NewHash` and the wrong frontmatter contract,
  and the coverage gate omitted `cas/hash/sha256`.

### Docs

`cas-core.md` v40→v50 (the `Digest`/`Hasher` model throughout: invariants,
diagrams, §4.1–4.12, data flows, concurrency, §7.1 surface, §7.2 recipes,
§8 decisions; then the `Validator` contract, the codec-injected
`gitlike.Repository` and its migration note; then the one-base exclusivity rule
and the "several stores under one root" recipe in §4.4; then the diagram pass,
which adds `Validator`/`Codecs` and corrects stale classes and member
signatures; then `digestPath`/`shortDigest`; then the Resolver type-safety
correction in §4.12 — the wrong resolver for a digest is a runtime
`ErrUnknownType`, not a compile-time error — and the pre-tag hardening in
§4.4/§4.5/§4.8/§4.9/§4.10/§4.12), `library-design.md` v19→v25 (`cas.Validator`
in the exported surface; the third ratified exception in §5; the released-cycle
wording), `coding-guidelines.md` v13→v16, `defaults.md` v16→v20 (the byte-layer
allocation target and its measured numbers; the short-hash row names `Prefix`), `examples.md` v15→v17,
`extensions.md` v6→v9 (the rejected `WithNamespace` decision in §3),
`operations.md` v6→v10 (the legacy store's actual failure symptoms in §5),
`testing-strategy.md` v12→v18 (the invariant law; `digestPath` in the
round-trip law; the fuzz-corpus and coverage claims; the `Prefix` cases), `versioning.md` v13→v18
(the third exception, the `v1.3.0` release, the released-state intro, the
registry exception's migration note, and the benchstat-gate correction),
`viewer-design.md` v10→v14 (§4/§5 rewritten against the real templates, ids and
htmx attributes; the short form is `Digest.Prefix(8)`), `frontend-architecture.md` v4→v5 (same corrections; it carried
the same fictional htmx map), `docs/index.md` v7→v10 (three path rows pointed at
files that do not exist), `AGENTS.md` v15→v21, `docs/AGENT.md` v8→v9
(`cas.NewHash` → `cas.NewDigest`), `docs/design/AGENT.md` v2→v3 (the frontmatter
contract), `AGENT.md` v14→v21 (the `hash` vs `digest` row, the `examples/` vs
`Example` row, the real four-key frontmatter contract),
`object-versioning.md` v4→v6 (no runtime registry — the envelope carries the
type), `consistency.md` v9→v11 (the viewer exposes verify/delete/GC, not prune),
`api-design.md` v5→v6, `backend-architecture.md` v15→v16, `cli.md` v13→v16
(`-no-open`; `list` tolerance), `performance.md` v13→v15,
`benchmarks/README.md` v7→v8, `docs/design/viewer-brief.md` v4→v5,
`README.md` (an Upgrading section and the seven-concept class diagram),
`CONTRIBUTING.md`, and the example/`gitlike` READMEs.

Every Mermaid diagram in the repo (13 blocks across 8 files) was re-checked
against the code: `Validator` added to the layer/overview/typed-layer figures,
`Codecs` added to the `gitlike` figures (and `Repository`'s stale `+hasher`
field removed — the hasher is held by the stores, not the repository),
`Commit`/`Tree`/`Tag`/`TreeEntry` show `+Validate() error`,
`json.Codec`/`gob.Codec` replace the informal `JsonCodec`/`GobCodec` names, and
the cached/envelope/store member signatures now match the code (`Load` returns
`(T, error)`, `Envelope` has fields `Type`/`Data`, `Backend` lists `Stats`).
Orientation (`flowchart TB`, `direction LR`/`TB`) is unchanged, and all 13
blocks were verified to parse with the Mermaid parser.

## [v1.2.0] - 2026-09-10

Code audit of the `v1.1.0` tree: a full read of `cas`, the backends, caches,
codecs, CLI, viewer, the `gitlike` reference model and the examples, with fixes
for the defects it found, plus a hash-type consolidation described below. The
on-disk format is unchanged — existing stores stay readable and writable, and
every stored object keeps its address.

### Added

- **`jsoncodec.Hash`** — the JSON codec's hash *field type*
  (`cas/codec/json`), the only place that renders and validates a hash as text:
  `NewHash(cas.Hash) Hash`, `Hash() cas.Hash`, `IsZero()`, `MarshalJSON`
  (present → `"algo:hexdigest"`, absent → `""`), `UnmarshalJSON` (`""`/`null` →
  absent, else `ParseHash`). Object types declare this type for reference
  fields; the byte layer stays format-free.
- **`cas.CheckHash`** — the guard the store and every backend apply to a hash
  argument: an absent (zero) address returns `ErrInvalidHash` instead of being
  used as a store key.
- **`Validate() error` on the `gitlike` object types** (`TreeEntry`, `Tree`,
  `Commit`, `Tag`) for objects built in code: a tree entry and a tag need a
  name, a commit needs a tree, an absent reference is valid where absence is
  legal. Advisory — `Store.Put` marshals, it does not validate — except the
  tree-less commit, which `Commit.MarshalJSON` still rejects at write time.

### Changed

- **BREAKING: `cas.Hash` is a concrete value type, not an interface, and it
  carries no JSON code.** `Hash` was an interface over an unexported
  implementation, which meant object fields could never be decoded by
  `encoding/json` (it cannot allocate into an interface field) and "no hash" had
  two spellings: a nil `Hash` in the byte layer and an absent wrapper in object
  fields. `Hash` is now a struct with unexported fields and one meaning for
  absence:

  ```go
  type Hash struct{ /* algorithm + digest */ }   // zero value = absent

  func (h Hash) IsZero() bool                    // the one "no hash"
  ```

  Serialization moved out of the core into the codec that owns a wire format.
  `cas/codec/json` defines the field type object types use:

  ```go
  type Hash struct{ /* wraps cas.Hash */ }        // jsoncodec.Hash
  func NewHash(h cas.Hash) Hash                   // wrap for a field or literal
  func (x Hash) Hash() cas.Hash                   // unwrap for the byte layer
  func (x Hash) MarshalJSON() ([]byte, error)     // present → "algo:hexdigest", absent → ""
  func (x *Hash) UnmarshalJSON([]byte) error      // ""/null → absent, else ParseHash
  ```

  `cas` no longer imports `encoding/json` at all: the byte layer knows nothing
  about any wire format, and only the codec that defines one renders and
  validates hashes as text. Object types declare `jsoncodec.Hash` fields
  (`omitzero` where absence is optional), so they still contain no JSON code
  themselves.
  **Migration from v1.1.x:**
  - `var h cas.Hash` / `cas.Hash{}` now means *absent* instead of nil — replace
    `h == nil` / `h != nil` with `h.IsZero()` / `!h.IsZero()`, and `x.Equal(nil)`
    with `!x.IsZero()` (an absent address compares equal to nothing).
  - `cas.ParseHash`, `NewHash` and `HashBytes` return the zero `Hash` on error
    (unchanged in shape: still `(Hash, error)`); check the error as before.
  - Reference *fields* change type to the codec's field type:
    `Ref cas.Hash` → `Ref jsoncodec.Hash`, literals `Ref: h` →
    `Ref: jsoncodec.NewHash(h)`, slices → `jsoncodec.NewHash` per element. Reads
    that need the byte-layer type (`ResolveTree(ctx, c.Tree)`,
    `x.Equal(y)`) unwrap with `.Hash()`; `IsZero()`, `omitzero`/`omitempty`
    tags and `References()` skipping behave as before.
  - Hand-written `Hash` implementations are no longer possible — build addresses
    with `NewHash`/`ParseHash`/`HashBytes` in a registered `HashFunc`. This
    closes the hole that let an unvalidated address (e.g. a hostile algorithm
    name) reach a backend, so `fs`'s `safeAlgo` path sanitizer is gone.
  - The absent wrapper type `cas.HashRef` — added in this same cycle and never
    released — is deleted; `cas.Hash` covers both
    roles.
  - `Validate()`-style checks now read `Tree.IsZero()` on the field type.
- **Stored bytes are unchanged.** `json` output for a present reference is the
  same `"algo:hexdigest"` string, an absent optional reference is still omitted
  (`omitzero`), and an absent always-present field still encodes as `""` — so no
  object is re-addressed and no migration is required. Pinned by
  `TestStoredAddressesPinned`, `TestNoteJSONPayloadPinned` and
  `TestManifestJSONPayloadPinned`.
- **All hash JSON code is in one place: the JSON codec.** Repository-wide, hash
  serialization went from nine methods in five packages to the one field type
  above — `jsoncodec.Hash`'s marshaler/unmarshaler in `cas/codec/json` — plus
  `gitlike.Commit`'s two guards for its one mandatory-field rule (write refuses a
  tree-less commit; decode rejects a missing, empty, or null tree). `gitlike`'s
  `TreeEntry` and `Tag` carry no JSON methods at all, and the hand-written
  marshallers *and* unmarshallers are gone from `internal/test/types.go`
  (`Node`), `examples/notes/types.go` (`Note`), `examples/artifacts/main.go`
  (`Manifest`), `cas/cache/prefetch/prefetch_test.go` and
  `cas/cache/mem/cached_test.go` (`testObject`), along with the now-unused
  `hashStrings`/`parseHashes`/`HashRefs` helpers.
- **Library baseline raised to Go 1.24** (`go.mod`, `library-design.md`,
  `defaults.md`, `coding-guidelines.md`, `versioning.md`, `AGENTS.md`,
  `README.md`). Optional hash fields rely on the `omitzero` JSON tag option,
  which an older standard library silently ignores — that would change the
  stored bytes, so the floor is now enforced by `go.mod` (a consumer on Go
  1.22/1.23 gets a clear build error instead of a different wire format).
- Docs and agent instructions updated for the unified type: `cas-core.md`
  (v35→v39: §4.2 the concrete `Hash` and the single meaning of absence, §4.6 the
  JSON codec's `jsoncodec.Hash` field type, §4.12 gitlike serialization and
  advisory `Validate()`, §7.1 surface adds `CheckHash`, §7.2 object-type recipe),
  `library-design.md` v18, `coding-guidelines.md` v13, `defaults.md` v16,
  `versioning.md` v12 (records this as a one-off ratified breaking change inside
  v1 — later breaking changes still need a MAJOR with the `/v2` mirror),
  `AGENTS.md` v15 (object-type recipe step and a type-checked usage example),
  `gitlike/README.md`, `examples/notes/README.md`,
  `examples/artifacts/README.md`. A godoc `ExampleHash` in `cas/codec/json`
  shows the field pattern from `go doc`.

### Fixed

- **Viewer: the maintenance GC form was unusable.** `templates/gc.html` shipped
  no CSRF field, so every submit was rejected with 403 even though the handler
  passed a token; the form now carries it.
- **Viewer: a template error left a half-written 200.** `render` executes into a
  buffer and answers 500 on failure instead of streaming a partial page.
- **Viewer: an empty token could authenticate.** `resolveToken` rejects an empty
  submission and compares tokens in constant time; `cask web -tokens` skips
  empty token pairs (`-tokens "admin="` no longer creates a `""` key).
- **Viewer: login-throttle TOCTOU and missing backoff.** The budget check and
  the attempt record now share one critical section, an exhausted budget blocks
  for an exponentially growing backoff (capped at 30 min), and stale per-IP
  state is swept so a rotating caller cannot grow the map without bound.
- **Viewer/CLI: type sniffing failed for large objects.** The TLV header is
  parsed without requiring the payload, so objects larger than the 256 KiB
  preview limit report their type; the object page shows the real size from the
  backend, and the raw view marks a truncated preview.
- **CLI:** the global `-store` is honored by `cask web` (it was dropped, so the
  viewer served `./objects`); `cask meta` reports the real object size instead
  of the bounded read length; `cask list -limit`/`-offset` reject out-of-range
  values (exit 2) instead of silently clamping; `put` implements the documented
  `-json` output.
- **`lru.Cache`:** `Clear`/`Evict`/`EvictKey` drop the recency bookkeeping, so
  cleared entries no longer retain objects or cause phantom evictions.
- **`CacheMetrics.Loads`** was exported but never incremented; `CachedObject.Load`
  now counts its store fetch.
- **`cas`:** `Put` rejects an object whose `Type()` is empty (it previously wrote
  an envelope that `Get` could never read); payload-decode errors keep their
  cause (`%w`); `Walker.Walk` uses an explicit stack plus a visited set, so a
  cyclic or deeply nested graph terminates instead of exhausting the goroutine
  stack; `RegisterHash` validates the algorithm name (`^[a-z0-9]+$`) and drops a
  stale streaming hasher, keeping `HashBytes`/`Store`/`Verify` in agreement.
- **fs backend:** `Clean` reclaims the `<hex>.tmp.<n>` collision fallbacks it
  missed, returns walk/removal errors, and tolerates a missing base; `Put`
  observes cancellation while streaming and treats an existing regular file as
  idempotent success (Windows rename-over-open); `Get` retries briefly while a
  concurrent rename makes the file unopenable; `hashPath` maps a non-conforming
  algorithm name to a safe in-root element.
- **mem backend:** `WithMaxSize` bounds buffering, so an oversized `Put` is
  rejected without allocating past the cap.
- **gitlike:** `Commit.MarshalJSON` returns an error for a nil tree instead of
  panicking on the nil `cas.Hash` interface.
- **examples:** `examples/artifacts` no longer panics on `-store <dir>` with no
  command; `examples/api/server` guards its size map with a mutex and verifies
  objects by streaming the hash (it previously hashed only the first 1 MiB, so
  every intact object above that was reported invalid).
- **benchmarks:** the FS `Put` benchmark varies 8 bytes of content per
  iteration instead of 2, so its hashes no longer repeat every 65536 iterations.

### Changed

- Docs re-aligned with the code: `cas-core.md` (v34→v35: `RegisterHash`
  validation, walker visited set, `CacheMetrics.Loads` semantics, fs
  rename/`Clean`/listing scope, mem buffering), `cli.md` (v12→v13: `gc`/`prune`
  grace default is 1h — the text said 24h while the code and `defaults.md` said
  1h — plus the exact `-json` shapes), `backend-architecture.md` (v14→v15) and
  `versioning.md` (v9→v10: grace default), `cas/store.go`'s stale pre-TLV
  on-disk-format comment, and `cas/backend.go`'s integrity note (bytes are not
  re-hashed on read unless `Verify` runs).
- `internal/index.Paginate` computes its window without overflow; callers
  validate the bounds first.
- Tests added: backend cancellation and size bounds, fs concurrency (`-race`),
  walker cycle/shared subgraph, hash `Equal` across algorithms, legacy envelope
  round-trip, cache `Loads` and LRU bookkeeping, and viewer CSRF/throttle/
  large-object coverage.

## [v1.1.0] - 2026-09-09

Minor release: an **additive viewer feature** plus docs/tests/CI work. No change
to the public `cas` core API, its semantics, or the on-disk format.

### Added

- **Viewer direct-token login.** `GET /viewer/?token=<startup-or-role-token>`
  establishes the same session cookie as `POST /viewer/login` (the `cask web`
  "open viewer" deep link). It is throttled and audit-logged like a form login,
  is never logged/echoed, and its response sends `Referrer-Policy:
  no-referrer` so the token cannot leak via `Referer`. When unauthenticated,
  `/viewer/` now redirects (303) to `/viewer/login`; data endpoints still
  return 401/403 empty. Specs (`viewer-security.md` v5→v7, `viewer-design.md`
  v9→v10) updated to authorize and document this behavior.
- **Scale economics probe** `BenchmarkScaleStoreEconomics`
  (`benchmarks/scale_bench_test.go`): reports the FS on-disk layout cost at N
  (object files, dirs, leaf-dir spread, bytes) at `(2,1)` vs `(4,1)`.

### Changed

- `docs/specs/performance.md` (v12→v13): the on-demand and `CASK_SCALE_OBJECTS`
  run commands now point at `./benchmarks/` (the suite moved there), fixing
  commands that previously targeted `./cas/`.

## [v1.0.2] - 2026-09-09

Patch release: documentation and CI additions on top of the frozen `v1.0.0`
surface. No change to the public `cas` API, semantics, or the on-disk format.

### Docs & tests

- Added runnable, `// Output`-verified godoc `Example` functions for the `cas`
  core and `gitlike` (package-level and per-symbol: `ExampleHashBytes`,
  `ExampleRepository`), so `go test` keeps the documented examples correct.
- Improved the `MarshalJSON`/`UnmarshalJSON` doc comments on the gitlike
  object types (they note they implement `json.Marshaler`/`json.Unmarshaler`
  and document nil behavior).
- Reverted `docs/specs/index.md` and `docs/design/index.md` to their
  pre-condensation originals.

### CI

- Added a nightly workflow (`.github/workflows/nightly.yml`) that runs each
  fuzz target for 60 s and the regular benchmark suite — the long-running
  checks the testing-strategy spec expects beyond the CI smoke.

## [v1.0.1] - 2026-09-09

Patch release: documentation and test-only additions on top of the frozen
`v1.0.0` surface. No change to the public `cas` API, semantics, or the on-disk
format.

### Docs & tests

- Expanded the package doc comments for `cas/cache/{mem,lru,prefetch}` and
  `cas/codec/{json,gob}` (godoc), and added a runnable, `// Output`-verified
  `Example` per package so `go test` keeps the documented examples correct.
- Fixed two remaining references to the old `docs/instructions` spec path
  (`cas/errors.go`, `docs/specs/AGENT.md`).

## [v1.0.0] - 2026-09-09

**First stable release.** The `cas` stable surface (cas-core §7.1) is frozen:
semver is now `v1.x.y`, and within a major version only additive, non-breaking
changes are allowed (library-design §5). This release reaches the versioning.md
§6 Definition-of-Done: every spec's acceptance checklist is fully ticked, the
CI gates (race, ≥90% coverage, fuzz smoke, doc-integrity) hold, and the four
runnable examples plus the `gitlike/` shared reference library and the embedded
viewer are in place.

### Added

- Fuzz targets `FuzzPathRoundTrip` and `FuzzVerify` (fs backend) and
  `FuzzCodecRoundTrip` (JSON codec, restricted to valid UTF-8). The CI fuzz
  smoke now runs each target against its real package — previously three of
  the four runs targeted `./cas/` where the targets did not exist, so they
  were vacuous.

## [v0.3.0] - 2026-09-09

Layout, documentation, and stale-code release. No change to the generic `cas`
core API or the on-disk format.

### Changed

- `gitlike` moved out of `examples/` to the module root: imported as
  `github.com/dmundt/go-cask/gitlike`, and declared a **reference / copy-source**
  shared library — importable for convenience but NOT part of the stable `cas`
  surface (cas-core §7.1); apps with their own object model copy the pattern and
  never extend gitlike. Its "example" framing was removed across the docs and
  comments.
- `benchmark/` renamed to `benchmarks/` (test package path
  `github.com/dmundt/go-cask/benchmarks`); referring docs/comments aligned.
- Docs/code stale claims fixed: `sha1` no longer described as a built-in hash
  (only `sha256` ships; others via `RegisterHash`); removed nonexistent
  `repo.go`/`manifest.go` and old JSON-envelope references from the examples.

### Fixed

- Type sniffing from stored bytes now reads the **TLV** envelope instead of
  the pre-TLV JSON envelope: `internal/index.EnvelopeType` (used by the viewer
  and `cask meta`) and the `examples/api` server's `envelopeType` now call
  `cas.EnvelopeFromBytes`. A legacy unversioned type name reads back as `@1`.
  Removed an obsolete local JSON envelope struct and orphaned `Deserialize`
  comments in `gitlike/types.go`.

## [v0.2.0] - 2026-09-09

Documentation and editorial release. **No public API or on-disk-format change.**

### Changed

- Condensed every file under `docs/` (27 specs/design docs + the three folder
  `AGENT.md` meta-guides) and all repo READMEs (`README.md`, the `benchmark`
  guide, and the `examples/*` READMEs) while preserving 100% of requirements,
  contracts, invariants, and compatibility notes; mermaid/fence blocks kept
  balanced. Instruction-doc frontmatter versions bumped by one per file
  (`benchmark/README.md` v4→v5); `cas-core` now v31.
- Swept stale pre-refactor identifiers from the example READMEs and a few
  source doc-comments: `FSRawStore`→`fs.Backend`, `RawStore`→`cas.Backend`,
  `JSONCodec[T]`/`GobCodec[T]`→`json.New[T]()`/`gob.New[T]()`,
  `StoreStats`→`cas.Stats`, `Encode`/`Decode`→`Marshal`/`Unmarshal`. Comment-
  only; no behavior change (4 `.go` files, `gofmt`/`go vet` clean).

## [v0.1.1] - 2026-09-09

The `cas.Backend` contract gained a `Stats` method returning the shared
`cas.Stats` summary (renamed from the fs-local `StoreStats`); the memory
backend implements it, and both backends carry a compile-time interface
assertion. Public API and on-disk format are otherwise unchanged.

### Performance

- Core hot-path allocation reduction (on-disk format and public API
  unchanged): `marshalEnvelope` now writes the TLV envelope into a single
  pre-sized allocation (no growing `bytes.Buffer`, no final copy), and the
  internal envelope parser returns the payload as a zero-copy slice of the
  read buffer instead of allocating `type`/`payload` copies
  (`cas/envelope.go`). `Store.Get` uses the zero-copy parser; the public
  `EnvelopeFromBytes` still returns an independent payload copy. The memory
  backend `Put` stores its `io.ReadAll` buffer directly instead of making a
  second copy. Measured (64 B object): `Store.Put` 13→11 allocs, `Store.Get`
  13→10 allocs, B/op and ns/op down (e.g. `Store.Get` 1 KiB 3.7 µs→2.4 µs).

### Added

- `Backend.Stats` is now part of the `cas.Backend` interface, returning the
  shared `cas.Stats` type (`cas/stats.go`) so the fs and memory backends report
  the same summary interchangeably. The memory backend (`cas/backend/mem`)
  gained `Stats(ctx)`, recomputing per-algorithm counts, total bytes, and
  object count from its object map (no desynchronized counter). Both backends
  carry a compile-time `var _ cas.Backend` assertion so a dropped method breaks
  their own package's build. Spec/docs updated to the six-method backend
  contract.

### Changed

- `cas.StoreStats` renamed to `cas.Stats` (`cas/storestats.go` →
  `cas/stats.go`); it stays in `package cas` (it is the `Backend.Stats`
  return type, so it cannot live in `cas/backend` without an import cycle).
- Both backends carry a compile-time `var _ cas.Backend = (*Backend)(nil)`
  assertion so a dropped method breaks the backend's own package build.
- Added unit coverage for `cas.Stats.String()`.

## [v0.1.0] - 2026-09-08

This release restructures the public API and the on-disk format ahead of
`v1.0.0`. **It is a breaking release**: the byte-layer storage contract was
renamed (its former name in the `cas` root package no longer exists) and
re-homed under a pluggable `cas/backend` package, codecs moved to `cas/codec/*`
subpackages with their serialization methods renamed to the standard
`Marshal`/`Unmarshal` idiom, the caching layer was split into `cas/cache/*`
subpackages, and stored objects switched to a versioned TLV envelope. No
migration path is provided — data written by earlier alphas is incompatible.

### Added

- `cas/backend` (`cas/backend/config.go`): the generic `type Option func(any)`
  so each backend owns its own config and `With*` helpers; backend config and
  option plumbing moved out of the `cas` root package.
- `cas/backend/fs` (package `fs`): type `Backend`, `fs.New(base, opts...)`,
  the `WithFanOut`/`WithFanLevels`/`WithDirSync` options, and `StoreStats` —
  the filesystem backend.
- `cas/backend/mem` (package `memory`): type `Backend`, `mem.New(opts...)`,
  and `mem.WithMaxSize(n)` to cap total stored bytes (`0` = unbounded) — the
  in-memory backend.
- `cas/codec/json` (package `json`) and `cas/codec/gob` (package `gob`):
  `json.New[T]()` and `gob.New[T]()`, each exposing `Marshal`/`Unmarshal`; a
  stdlib `encoding/gob` binary codec joins the relocated JSON one.
- The versioned **TLV envelope** as the stored-object format
  (`[version u8][uvarint typeLen][type][uvarint payloadLen][payload]`, in
  `cas/envelope.go`, cas-core §8 decision 1) with the public accessor
  `cas.EnvelopeFromBytes`; a missing `@major` reads as `@1` and the leading
  version byte future-proofs the format.
- `cas/cache/mem` (package `memory`: `CachedStore`, `CachedObject`,
  `New(store)`), `cas/cache/lru` (package `lru`: `New(store, maxSize)` LRU
  eviction over a `memory.CachedStore`), and `cas/cache/prefetch` (package
  `prefetch`: `NewSmartCache`) — the caching layer split into three packages,
  with `SmartCache` factored out of the mem cache.
- A top-level `benchmark/` directory (`bench_test.go` + `scale_bench_test.go`)
  hosting the suite moved out of `cas/`; the on-demand scale probes honor
  `CASK_SCALE_OBJECTS`.
- `internal/test/` shared test types/fixtures reused across packages.
- `docs/index.md` (the path→spec rule index, read-first per AGENTS.md) plus
  per-directory `index.md` and `AGENT.md` governance files under `docs/`,
  `docs/design/`, and `docs/perf/`.
- All docs frontmatter converted to OKF format; a constructor-naming rule
  (`New()` vs `NewType()`) documented in `docs/AGENT.md`.
- CI now gates each `cas/backend/*`, `cas/cache/*`, and `cas/codec/*` package
  individually at ≥ 90% coverage and runs the doc-integrity gate against the
  new `docs/specs/` home.

### Changed

- **The byte-layer storage contract is now the `Backend` interface** (package
  `cas`, `cas/backend.go`), with concrete implementations living in the
  `cas/backend/*` subpackages — a breaking rename of the core interface.
- **`Codec[T]` serialization methods renamed to the standard
  `Marshal`/`Unmarshal` names** used across the `encoding/*` packages — a
  breaking API change.
- **Stored-object serialization switched to the versioned TLV envelope** — a
  breaking change to on-disk bytes and therefore to the content hashes.
- Backend constructors/options and codecs moved out of the `cas` root package;
  callers now use `fs.New`/`mem.New`/`json.New[T]`/`gob.New[T]` with the
  `cas/backend` `Option` type (see Added).
- `Store[T]`'s marshal/read paths rebuilt around the codec as the single
  serialization authority plus the TLV envelope; reads verify the stored type
  name against the decoded value's type.
- The caching layer moved from the `cas` root package into `cas/cache/*`
  (memory/LRU/prefetch split) with `SmartCache` promoted from `examples/notes`
  to `cas/cache/prefetch`.
- Examples (`files`, `notes`, `artifacts`, `gitlike`), `cmd/cask`, and the
  viewer updated to the new API; codec wrappers now compose `json.New[T]`
  with `Marshal`/`Unmarshal`.
- Tests reorganized per package (cached/LRU/smartcache split; `cas` corner and
  external tests distributed to their owning packages) and coverage lifted
  across the tree (mem backend ~97%, fs backend ~90%, `cas` ~95%, `cas/codec`
  ~92%); shared test types centralized in `internal/test/`.
- `examples/files`'s `main` refactored for testability with error-path and
  command-execution tests added.

### Removed

- The pre-TLV `type\npayload` serialization and the earlier JSON envelope,
  both superseded by the versioned TLV envelope.
- The in-`cas` byte-layer backend and codec files/constructors that the
  `cas/backend/*` and `cas/codec/*` packages replace.
- Stray generated coverage artifacts (`cachecover`, `cas/cache/mem/cover.out`).

### Fixed

- Full benchmark suites that were dropped when benchmarks moved to
  `benchmark/` were restored.
- `examples/files`'s `cat` now resolves blobs to raw bytes and other types to
  a description.
- Doc-integrity path updated to the `docs/specs/` home and all docs swept to
  the current API after the refactors; README Mermaid diagram parse error
  fixed; `envelope.go` consistency fixes and TLV doc alignment.

## [v0.1.0-alpha.2] - 2026-09-03

### Added

- Core-overview diagram of the `cas` interfaces (byte / typed / caching
  layers and their dependencies) — canonical copy in `cas-core` §3.3, with
  the same diagram embedded in the README; `design/core-overview.md` points
  to the spec (cas-core v13, v14).

### Changed

- Audit decisions (core lib, 2026-09): prune now defaults to --min-age 24h (was 1h) with the forced --min-age 0 warning and a required root argument, matching gc/cli/consistency; FSRawStore.Size takes context.Context first (every I/O method does); new optional WithDirSync FSOption fsyncs the parent directory after the publish rename (best-effort, no-op on Windows, operations §1); lean-core budget re-baselined to ≤ ~1600 LOC / ≤ ~40 exports with the full stable surface enumerated (library-design v10); library baseline declared Go 1.27 (defaults v10, versioning v5, AGENTS v10, README). Checklist ticks: cli/prune grace item, operations fsync item, library-design budget item (cli v10, operations v5 unchanged).
- `cas` `FSRawStore.Put`: temp files now use **unique per-writer names**
  (created with `O_CREATE|O_EXCL`; a numeric suffix is appended only when
  another process holds `<path>.tmp`) instead of a deterministic
  `<path>.tmp` — concurrent writers of the same hash never share a temp
  inode, so cross-process same-hash writes cannot corrupt each other; on
  POSIX the atomic rename gives last-wins, on Windows a racing Put may
  transiently error but never corrupts (cas-core v18, operations v4).
- `cask`: **grace-based maintenance sweeps, writers lock-free**. Writers
  (`put`) and the viewer (`web`) never lock — object writes are safe across
  processes by construction (unique temps + atomic rename). Maintenance
  sweeps (`gc`, `prune`, `clean`) take the store's exclusive cross-process
  lock (`.cask.lock`, holding the PID) so two sweeps never overlap, and
  reclaim only objects older than `--min-age` (default 24h), so a concurrent
  writer's fresh objects survive. A forced `--min-age 0` sweep is the
  documented dangerous variant (prints a warning; only safe with no other
  writer). `gc` gained the `--min-age` flag (was immediate)
  (cli spec v9, cas-core v19, backend-architecture v10, consistency v6).
- Concurrency model documented: cas is **multi-client safe within one
  process** (lock-free reads, per-process mutexes, atomic writes — cas-core
  §6); across processes, reads and same-hash `Put`s are safe by construction
  while maintenance sweeps must be grace-gated against live writers
  (cas-core v19, backend-architecture v10, consistency v6).
- Naming: the acronym expansion is **Content Addressable Store (Kit)** and is
  written ALL-CAPS (`CAS`, `CASK`) everywhere — lowercase `cas` only as the
  Go package — replacing the former "Content Addressed Storage (Kit)" wording
  in README, specs, package comments, and example READMEs; the
  "Cas core parts used" README headings are now "`cas` core parts used"
  (AGENT.md v6).
- `examples/files`: new `audit` command — a derived per-object state report
  (verified / orphaned / corrupt / unverified) built from `List` +
  reachability-from-HEAD marking + per-object `Verify`, with a
  `-no-verify` fast-orphan mode; states are scan results, never stored
  (examples spec v8).
- `cas-core` §3.1 layer diagram converted from a fragile ASCII box to a
  Mermaid flowchart (subgraphs per layer + dependency edges) (cas-core
  v15).
- `cas` typed layer: reads renamed for symmetry — `Store.GetTyped` is now
  `Store.Get` (concrete `T`), and the cached layer's loaded read is
  `CachedStore.Get`/`LRUCache.Get` with the lazy proxy accessor renamed
  `Get` → `Proxy`. `Put`/`Get`/`Delete` now read as a natural trio across
  layers (cas-core v14, library-design v7, testing-strategy v8,
  performance v7).

## [v0.1.0-alpha.1] - 2026-09-03

### Added

- **`cas` core library** — the generic core of a content-addressable store:
  - byte layer: `Hash`/`ParseHash`/`NewHash`/`RegisterHash`/`NewHasher`/
    `HashBytes` (sha1 + sha256), `RawStore` contract, `FSRawStore`
    (fan-out layouts, atomic temp→`Sync()`→rename writes, lock-free reads,
    `Stats`/`Verify`/`GC`/`Prune`), `MemoryRawStore`, sentinel errors;
  - typed layer: `Object[T]`, `Codec[T]`/`JSONCodec[T]`, `Store[T]`
    (one-pass hashing, `PutDedup`, self-describing envelope with
    type-verifying reads), `Walker[T]`;
  - caching: `CachedObject[T]`, `CachedStore[T]`, in-tree `LRUCache[T]`;
  - **`examples/gitlike`** — the reference object model (`Blob`/`Tree`/
  `Commit`/`Tag`, `Repository`, `Resolver`/`ResolvedObject`, `WalkGraph`,
  `CachedRepository`, `Preloader`).
- **`cmd/cask`** — the single entry point: CLI store operations (`put`,
  `get`, `cat`, `list`, `meta`, `stats`, `verify`, `gc`, `prune`) over the
  library in-process, the `web` subcommand (the embedded viewer), and
  `version`.
- **`internal/`** — the viewer implementation: `web` (sessions, CSRF,
  roles, htmx templates), `storage` (filesystem store service), `index`
  (pagination/envelope-type helpers).
- **The viewer** — dashboard, object list/detail with lazy hexdump,
  references (best-effort via gitlike), graph, stats, GC — secure by
  default (`cask web`), startup-token login, empty-body 401/403.
- **Examples** — `files` (gitlike miniature), `artifacts` (gzip codec +
  custom hash + cache + GC), `notes` (own types + lazy loading +
  prefetch), `api` (HTTP-exposure pattern: store server + plain-HTTP demo), each with a
  rule-8 README.
- CI: gofmt/tidy/vet, `-race` + per-package coverage gate (cas, gitlike
  ≥ 90%), fuzz smoke (4 targets), doc integrity, import boundaries, benchmark
  allocs gate.

- cas: `FSRawStore.Size` (per-object size) and `Clean` (orphan `*.tmp`
  sweep).

### Changed

- Audit decisions (core lib, 2026-09): prune now defaults to --min-age 24h (was 1h) with the forced --min-age 0 warning and a required root argument, matching gc/cli/consistency; FSRawStore.Size takes context.Context first (every I/O method does); new optional WithDirSync FSOption fsyncs the parent directory after the publish rename (best-effort, no-op on Windows, operations §1); lean-core budget re-baselined to ≤ ~1600 LOC / ≤ ~40 exports with the full stable surface enumerated (library-design v10); library baseline declared Go 1.27 (defaults v10, versioning v5, AGENTS v10, README). Checklist ticks: cli/prune grace item, operations fsync item, library-design budget item (cli v10, operations v5 unchanged).
- Layout: `internal/` for all implementation detail (Go-enforced privacy);
  the viewer lives in `internal/web/`; `cas/` stays at the repo root.
- The server became `cask web` — the embedded viewer only, no JSON API
  surface; `cmd/caskd` removed.
- OpenAPI documents MUST live in separate embedded `.yaml` files
  (api-design §13) — JSON example surfaces only (`examples/api`); the
  viewer needs none.
- Every example ships a `README.md` covering the cas core used, what it
  extends, a code walkthrough, and a Mermaid diagram (examples §2 rule 8).
- Examples renamed to short single-word names (`files`, `artifacts`,
  `notes`, `api`; proposed `viewer`).
- GitHub Actions bumped to Node-24 majors (`checkout@v5`, `setup-go@v6`);
  module caching disabled (std-lib-only module has no `go.sum`).

- `internal/storage` removed: `cmd/cask` and the viewer use `cas.FSRawStore`
  directly (the service layer had become a passthrough).
- CLI: the `cat` alias is gone — `get` without `-o` prints to stdout
  (cli spec v7).

- `cas` typed layer: `Store[T Object[T]]` — the constraint makes every
  handled value an object at compile time (no type assertions remain); `Put`
  takes the concrete `T`; new sentinel `ErrCorrupt` for payloads the codec
  cannot decode (cas-core v11).

- `cas` typed layer: `Store[T].Get` retired — reads return the concrete `T`
  via `GetTyped` (type-verified) or the bytes via `GetRaw`; `Walker` visits
  now take `func(T) error`; the cached layer returns concrete `T` from
  `Load`/`GetTyped` (cas-core v12, coding-guidelines v6).
- `cas` typed layer: the `Codec[T]` is now the single serialization
  authority — `Store.Put` builds the envelope from `codec.Encode` + the
  object's `Type()`; `Object[T]` shrank to `{Type, References}`
  (`Serialize`/`Deserialize` removed from the contract; example serializers
  become vestigial and are removed in a follow-up). cas-core v10.

### Fixed

- CI steps that assumed a `go.sum` and a single-package `./cas/...`
  (dependency-free module + `cas/extra`); coverage gating per package;
  doc-integrity false positive for pattern literals.
- `sha256-double` (examples spec) renamed to `sha256double` — the name must
  obey the hash-string validation pattern (defaults §2).
- Fuzz-discovered `encoding/json` lossy invalid-UTF-8 round-trip — the
  codec fuzz target constrains input; regression corpus committed.
- Stale `examples/cas-api/client` reference in backend-architecture §5.

### Removed

- `cas/extra` — `SmartCache`/`CacheMonitor` inlined into `examples/notes` and
  `examples/artifacts` (example recipes, not core; cas-core v9).
- `client/` — the public CAS API client SDK; CLI remote mode (`-api`/
  `-token`) goes with it.
- `internal/api` + `internal/auth` — the CAS JSON API handlers, bearer-token
  auth, and the IP rate limiter.
- The CAS HTTP API surface: the `cas-api` / `viewer-api` specs, `/api/cas/v1`
  routes, the viewer OpenAPI doc + `/swagger/` (never implemented), and the
  `-viewer`/`-rate`/`-burst` flags (`cask web` now IS the viewer).
- Viewer references/graph + `internal/storage.Raw()`: the viewer is a
  byte-layer tool and no longer imports `examples/gitlike` (dependency rule,
  coding-guidelines §9).
- `cas.NewStoreWithHasher` — undocumented, unconsumed constructor; custom
  hash algorithms use the documented `RegisterHash` + `NewStore` recipe
  (cas-core §4.2).
- `cmd/caskd` (absorbed into `cmd/cask web`).
- The top-level `viewer/` directory (viewer code moved to `internal/web/`).
