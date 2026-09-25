---
type: Specification
title: Go Coding Guidelines — go-cask
description: Idiomatic Go with a minimal dependency policy, scoped viewer CSS, no viewer script beyond vendored htmx, html/template + htmx, raw HTML, doc-comment rules, Go 1.24+ baseline (generics, enhanced routing, `omitzero`) and the latest generics (toolchain 1.27).
version: v26
---

# Go Coding Guidelines — go-cask

Applies to all Go code (`cas/`, `internal/`, `cmd/`), complementing `cas-core.md` (what to build) and `viewer-security.md` (how the viewer MUST be secured); on conflict with an older sketch elsewhere, this file wins. Rules: idiomatic Go; minimal dependencies; scoped viewer CSS; server-side `html/template` + **htmx**; no viewer script beyond vendored htmx; prefer raw HTML; document every exported identifier; latest Go generics where they help.

## 1. Go version and toolchain

- Library baseline Go 1.24+. `go.mod` declares `go 1.24` with `toolchain go1.27.1` — the self-managing toolchain auto-downloads 1.27.1 for CI; consumers on 1.24+ can build. The 1.24 floor is required by the `omitzero` JSON tag option (cas-core §4.6): an older standard library ignores it, changing stored bytes.
- Baseline language (1.24+): generics/type sets (`~` unions)/`comparable` (1.18+), `slices`/`maps`/`cmp` (1.21+), range-over-int (1.22+), `iter`/range-over-func (1.23+), generic type aliases and `encoding/json` `omitzero`-style zero hooks (1.24+), plus later additions.
- `GOTOOLCHAIN=auto` (default) uses the `go.mod`-declared toolchain; CI MUST pin the same version for reproducibility.
- Do NOT use features from a newer toolchain than the declared `go` directive — the declaration is the contract.

## 2. Idiomatic Go

- `gofmt` before every commit; `goimports` grouping (std, third-party, local).
- Naming: mixedCaps; exported uppercase; initialisms keep case (`ID`, `URL`, `API`, `HTTP`); no package-name stutter (`cas.Store`, never `cas.CasStore`); short names for short scopes.
- Constructors mirror how many primary types the package exposes: plain `New()` for the package's primary type even when it exposes others (`fs.New`, `backmem.New`, `json.New[T]`, `gob.NewRaw[T]()`, `binary.New(...)`, `binary.NewRaw(...)`, `lru.New`, `sha256.New`) or the primary `Store` (`cas.New`); `NewType()`/`NewXyz()` for several important types or a non-primary type (`cas.NewDigest`, `cas.NewWalker`). `prefetch.NewSmartCache` is the exception the frozen surface fixes (cas-core §7.1): it is a `NewType` name in a single-type package, and renaming it to `New` is a breaking change that waits for a major version. Use `New*` only for real (non-trivial) setup; prefer a useful zero value otherwise.
- Codec wrappers share one stack pattern: inner codec first, outer transform second — no ad hoc variants. Every wrapping codec MUST take the inner codec as `next` in its constructor (`next`-first chaining is the default pattern; the direct/raw constructors are the exception).
- Errors: handle or explicitly ignore (`_ =` + comment why). Wrap with `%w`; unwrap with `errors.Is`/`errors.As`. Sentinel errors for expected conditions; never string-match. Never `panic` in library code — only in `main` for unrecoverable setup.
- `context.Context` MUST be the first parameter of any I/O-capable/cancellable function; never store it in a struct — derive and pass down.
- Prefer small consumer-side interfaces; "accept interfaces, return concrete types."
- Useful zero values; `NewX` only for non-trivial setup (e.g. `fs.New` must create directories).
- Tests: table-driven, `testing`, `t.Run` subtests, `t.Parallel()` where safe.

## 3. Minimal dependency policy

| Need | Std-lib answer |
|---|---|
| HTTP | `net/http` (1.22+ pattern routing `mux.HandleFunc("GET /x/{id}")`) |
| JSON | `encoding/json`; new code MAY use `encoding/json/v2` + `jsontext` (1.27) |
| HTML | `html/template` (auto-escaping) — never `text/template` for HTML |
| Hashing/signatures | `crypto/sha256` via an injected `cas.Hasher` (`cas/hash/sha256`); `crypto/sha1`/`md5` only where a protocol requires them; `io.MultiWriter`/`io.TeeReader` for hash-on-write; `crypto/mldsa` (1.27) |
| UUIDs | `uuid` (1.27) — never `github.com/google/uuid` |
| Concurrency | `sync`, `sync/atomic`, `context` |
| CLI | `flag` (or `os.Args` for trivial tools) |
| Testing/bench | `testing`, `net/http/httptest`, `testing/fstest` |
| Data/strings | `slices`, `maps`, `cmp`, `container/list`, `container/heap`; `strings.CutLast`/`bytes.CutLast` (1.27) |

Check Go 1.27 release notes before adding an external package. External packages SHALL NOT be added unless **necessary** (no feature-equivalent std-lib solution). The approved exception is `golang.org/x/sys`, used only by `cas/bloom/persistent` for portable mmap flushing where the standard library has no equivalent. Any new external dependency MUST be justified in the commit/PR and added to `go.mod`/`go.sum`; vendoring is optional unless an offline build requires it.
Consequences: the LRU cache SHALL be in-tree std-lib (`container/list`+`sync.Mutex` or `sync.Map`-backed) — cas-core §8 decision 3; hashing goes through the injected `cas.Hasher` seam (the shipped `cas/hash/sha256` is the client-side default; the core names no algorithm and has no registry, cas-core §4.2); the only frontend exception is **htmx** (§5).

## 4. Scoped viewer CSS, and no viewer script

- Outside `internal/web/`, SHALL NOT add CSS (no `.css`, no style elements, no inline `style` attributes).
- The viewer MAY ship exactly one stylesheet, `internal/web/viewer.css`: embedded, served
  only as `/viewer/static/viewer.css`, class-based selectors rooted at the viewer
  shell, no external imports/assets/fonts, no user-controlled values; no other
  viewer CSS file or style source is permitted. Presentation layer only, never a
  source of application state or behavior.
- The viewer MUST NOT use inline `style` attributes or CSS-generated content
  for information or controls required to understand or operate the UI.
- SHALL NOT add JavaScript. Vendored htmx is the only script the viewer serves.
- Presentation-only affordances otherwise needing a script MUST be expressed in
  CSS or plain HTML — the object inspector resizes via the CSS `resize` property
  bounded by `min-width`/`max-width`; the full digest is a readonly field the
  operator selects and copies, not a clipboard control.
- Other interactivity only via htmx attributes
  (`hx-get`/`hx-post`/`hx-target`/`hx-swap`/`hx-trigger`…) requesting HTML
  fragments; no other client-side state.
- Rationale: minimal attack surface and auditability (viewer-security), no
  build pipeline, no browser secrets, server-owned application state.

## 5. Server-side rendering: templates + htmx

- All HTML SHALL be `html/template` — contextual auto-escaping is the XSS boundary; never write raw HTML outside a template.
- Composition via `{{define "base"}}`/`{{template "content" .}}` or `template.ParseFS` over `embed.FS`; templates in `internal/web/templates/`, embedded in the binary.
- Use latest template features (1.27): `ParseFS` over `embed.FS`; `{{define}}`/`{{template}}`/`{{block}}`; `{{else if}}` chains and `break`/`continue` in `{{range}}` (1.22+); `{{- -}}` whitespace control; pipelines and a registered `template.FuncMap` (registered before parsing; names lowercase, side-effect free).
- Go 1.27 adds no `text/template`/`html/template` API — "latest" means the full feature set above, not another engine.
- One template per view + small reusable partials; logic limited to `{{if}}`/`{{range}}`/`{{with}}`/pipeline funcs; all computation in Go, pass pre-shaped data.
- htmx endpoints return HTML fragments (not JSON); full pages for navigation. Forms use standard `method="POST"` with CSRF (viewer-security). Every mutation goes through the backend; the browser never talks to storage directly.

## 6. Prefer raw HTML

- "Raw HTML" = hand-written semantic markup in templates — no frameworks, no JS-generated DOM.
- Never build HTML in Go by string concatenation — dynamic output is always a template.
- Prefer semantic elements (main, navigation, table, form, label) over generic containers; accessibility needs labels, alternative text, logical heading order. Heavy template logic means the Go side should pre-compute.

## 7. Document exported types and functions

- Every exported identifier MUST have a name-first doc comment; every package SHALL have a package comment (`// Package cas implements …`).
- Comments document contracts (preconditions, ownership e.g. "caller MUST Close", concurrency safety, error behavior), not code. `go doc` must read cleanly.
- Add runnable `Example` functions in `_test.go` for non-obvious public API (docs that can't rot).

## 8. Latest Go generics where possible

- Core is generic by design (`Store[T]`, `Codec[T]`, `Object[T]`, caches) — replaces `any`+reflection, moves type errors to compile time.
- No `any`/`interface{}` in the exported API. Constrain type parameters with interfaces (incl. type sets/`~` unions) where the semantics require methods/operations; `comparable` for map keys/equality; ordering constraints only where needed. Prefer std `slices`/`maps`/`cmp`; `range` over slices/maps (1.22+) and functions (`iter`, 1.23+). Generic type aliases (1.24+) when they clarify the API.
- **1.27 additions:** generic methods (a method MAY declare its own type parameters) — interface methods MAY NOT declare type parameters and can't be implemented by generic methods, so `Object[T]`'s methods stay non-generic; generalized function type inference (prefer inferable generic functions); field-selector keys in struct literals (`Config{Server.Port: 8080}`) where clear.
- Use generics only where they remove duplication or replace `any`/reflection — never over-generalize: for a single use, or indirection with no removed duplication, write concrete code.

## 9. Project structure and conventions

- Layout: `cas/` (public core, `package cas`), `internal/` (`web`, `index`; not importable outside the module), `cmd/` (thin `main` only), `examples/`.
- **No product → example imports:** `cas/`, `internal/`, `cmd/` MUST NOT import `examples/` (downstream consumers, never upstream deps). `cas/` is the only public package (plus `gitlike/`).
- Viewer middleware (authn, sessions, CSRF, login throttle) lives in `internal/web`. An example surface MAY add an IP rate limiter (std-lib token bucket, 429 + `Retry-After` + `X-RateLimit-*`, loopback exempt).
- `go.mod` at root declaring `go 1.24` + `toolchain go1.27.1`; module path matches the repo. No blank imports except `embed`; no init magic (the core has no registry or global state).
- Tests: every exported `cas/` function tested; handlers use `httptest`; template FS fixtures use `testing/fstest`.
- Verify before commit: `gofmt -l .`; `go vet ./...`; `go test ./...`; `go build ./...`.

## 10. Frontend boundary (`internal/web/`)

- Serves `html/template` pages and htmx fragments over `net/http`. No build step/npm/static pipeline — templates embedded via `embed.FS`, htmx one pinned vendored file (CDN only with integrity attribute).
- MUST comply with `viewer-security.md` (secure by default, authn/authz, sessions, CSRF, audit logging); no CSS/no JS keeps the viewer minimal and auditable.

## 11. Pre-commit checklist

- [x] `gofmt -l .` clean; `go vet` and `go test` pass
- [x] `go.mod` declares `go 1.24` + `toolchain go1.27.1`; no external dependency beyond the approved exception (§3: `golang.org/x/sys` for `cas/bloom/persistent`), each justified in the PR; vendoring is optional unless an offline build requires it
- [x] No CSS, no hand-written JS, no style or script elements — htmx only
- [x] HTML via `html/template` only, using the latest feature set (`ParseFS`, composition, `break`/`continue` in `{{range}}`, `FuncMap`); no HTML string concatenation in Go
- [x] Every exported identifier documented (name-first doc comments)
- [x] Generics (incl. generic methods) where needed; nothing over-engineered; no features newer than the declared `go` directive
- [x] `context.Context` first; errors wrapped `%w`; no panics in library code
- [x] Viewer changes re-checked against `viewer-security.md`
