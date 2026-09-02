---
title: Go Coding Guidelines — go-cask
description: Idiomatic Go, standard-library-only, no CSS/JS, html/template + htmx, raw HTML, doc-comment rules, Go 1.27 and the latest generics.
version: v3
---

# Go Coding Guidelines — go-cask

> Applies to **all Go code** in this repository (`cas/`, `internal/`,
> `cmd/`).
> Complements `.github/instructions/cas-core.instructions.md` (what to
> build) and `.github/instructions/viewer-security.instructions.md` (how the
> viewer must be secured). Where this file conflicts with an older sketch in
> another document, this file wins.
>
> Summary of the rules: idiomatic Go, standard library only (no external Go
> packages unless truly necessary), **no CSS, no JavaScript**, server-side
> rendering with Go `html/template` plus **htmx** for interactivity, prefer raw
> HTML, document every exported type and function, target **Go 1.27**, and use
> the latest Go generics where they genuinely help.

---

## 1. Go Version & Toolchain

- Target: **Go 1.27** — this is the installed local toolchain. The `go.mod`
  MUST declare `go 1.27`.
- Language features available and allowed in 1.27: generics (1.18+), type sets
  / `~` unions (1.18+), `comparable`, `slices`/`maps`/`cmp` (1.21+), `range`
  over integers (1.22+), `iter` / range-over-func (1.23+), generic type
  aliases (1.24+), and the **Go 1.27 language additions** — generic methods,
  generalized function type inference, and field-selector keys in struct
  literals (details in §8).
- Go ≥ 1.21 toolchains are self-managing: `GOTOOLCHAIN=auto` (default) uses
  the toolchain declared by `go.mod`. CI MUST pin the same version so builds
  are reproducible.
- Do not use language features from a *newer* toolchain than 1.27 — the
  declared version is the contract.
- Reference: [Go 1.27 release notes](https://go.dev/doc/go1.27).

---

## 2. Idiomatic Go

- **Formatting is non-negotiable:** `gofmt` before every commit; keep import
  grouping with `goimports` conventions (std, then third-party, then local).
- **Naming:** mixedCaps identifiers; exported identifiers start uppercase;
  initialisms keep their case (`ID`, `URL`, `API`, `HTTP`); avoid package-name
  stutter (`cas.Store`, never `cas.CasStore`); short names for short scopes.
- **Errors:**
  - Every error is handled or explicitly ignored (`_ =` with a comment why).
  - Wrap with `%w` (`fmt.Errorf("...: %w", err)`); unwrap with `errors.Is` /
    `errors.As`.
  - Use sentinel errors for expected conditions; never string-match errors.
  - Never `panic` in library code — panic only in `main` for unrecoverable
    setup failures.
- **Context:** `context.Context` MUST be the first parameter of any function
  that does I/O or can be cancelled. Never store a `context.Context` in a
  struct; derive and pass it down.
- **Interfaces:** prefer small interfaces defined at the consumer side;
  "accept interfaces, return concrete types".
- **Zero values:** make zero values useful; use `NewX` constructors only when
  setup is non-trivial (e.g. `NewFSRawStore` must create directories).
- **Tests:** table-driven tests with the std `testing` package; `t.Run` for
  subtests; `t.Parallel()` where safe.

---

## 3. Standard Library Only

Use the standard library by default:

| Need                       | Std-lib answer                                                    |
| -------------------------- | ----------------------------------------------------------------- |
| HTTP server & routing      | `net/http` (Go 1.22+ pattern routing: `mux.HandleFunc("GET /x/{id}")`) |
| JSON                       | `encoding/json`; new code may use `encoding/json/v2` + `encoding/json/jsontext` (new in 1.27) |
| HTML rendering             | `html/template` (auto-escaping) — never `text/template` for HTML  |
| Hashing / signatures       | `crypto/sha256`, `crypto/sha1`, `crypto/md5`; stream via `io.TeeReader`; `crypto/mldsa` (new in 1.27) |
| UUIDs                      | `uuid` (new in 1.27) — do not add `github.com/google/uuid`        |
| Concurrency                | `sync`, `sync/atomic`, `context`                                  |
| CLI                        | `flag` (or `os.Args` for trivial tools)                           |
| Testing / benchmarks       | `testing`, `net/http/httptest`, `testing/fstest`                  |
| Data structures & strings  | `slices`, `maps`, `cmp`, `container/list`, `container/heap`; `strings.CutLast` / `bytes.CutLast` (new in 1.27) |

Go 1.27 std-lib additions worth using: `encoding/json/v2` and
`encoding/json/jsontext` (structured JSON encode/decode with `Options`, token /
value streams), the new `uuid` package, `strings.CutLast` / `bytes.CutLast`,
and `crypto/mldsa` (post-quantum signatures). Before reaching for an external
package, check the [Go 1.27 release notes](https://go.dev/doc/go1.27) — the
standard library may already provide it.

External packages SHALL NOT be added unless **necessary** — i.e. no
feature-equivalent standard-library solution exists. Any external Go dependency
MUST be:

1. justified in the commit/PR message, and
2. vendored (`go mod vendor`) so builds never depend on the network.

Consequences for this repo:

- The LRU cache sketched in `cas-core.instructions.md` with
  `github.com/hashicorp/golang-lru/v2` SHALL be implemented in-tree with std-lib
  primitives (`container/list` + `sync.Mutex`, or a `sync.Map`-backed
  approximation) — this implements cas-core §8 decision 3.
- Extra hash algorithms (e.g. blake3) are only added via `RegisterHash` if
  genuinely required; the std-lib algorithms (`sha256`, `sha1`) are the default.
- The single allowed frontend exception is **htmx** (§5). No other frontend
  library is permitted.

---

## 4. No CSS, No JavaScript

- SHALL NOT add any CSS: no `.css` files, no `<style>` elements, no inline
  `style="..."` attributes in templates.
- SHALL NOT add any JavaScript: no `.js` files, no hand-written `<script>`
  elements, no client-side logic of any kind.
- The only script allowed anywhere in the viewer is **htmx** (one pinned,
  vendored file, or a CDN URL with an integrity attribute). htmx's script is
  not "our" JS — it is the framework that provides interactivity.
- Interactivity is expressed **only** through htmx attributes (`hx-get`,
  `hx-post`, `hx-target`, `hx-swap`, `hx-trigger`, ...) that request HTML
  fragments from the backend. There is no client-side state.
- Rationale: minimal attack surface and auditability (per
  `viewer-security.instructions.md`), no build pipeline, no secrets in the
  browser, and a viewer that works with JavaScript disabled except for the
  htmx enhancement itself.

---

## 5. Server-Side Rendering: Go Templates + htmx

- All HTML SHALL be rendered by `html/template`. Its contextual auto-escaping
  is the XSS boundary; never write raw HTML to the response outside a template.
- Layout pattern: template composition via `{{define "base"}}` /
  `{{template "content" .}}`, or `template.ParseFS` over an `embed.FS`.
  Templates live in `internal/web/templates/` (or next to their handlers) and
  are embedded into the binary.
- **Use the latest template capabilities** available in 1.27:
  - `template.ParseFS` over `embed.FS` for embedding (no runtime file I/O),
  - composition via `{{define}}` / `{{template}}` / `{{block}}`,
  - `{{else if}}` chains and `break` / `continue` inside `{{range}}` (1.22+),
  - whitespace control with `{{- ... -}}`,
  - pipelines, and a registered `template.FuncMap` for view helpers (registered
    before parsing; keep function names lowercase and side-effect free).
- The [Go 1.27 release notes](https://go.dev/doc/go1.27) contain **no changes
  to `text/template` / `html/template`** — the API is stable. "Latest
  templates" therefore means using the full modern feature set above with the
  1.27 toolchain, not adopting a different template engine.
- One template per view plus small reusable partials; keep templates
  declarative and readable.
- **Minimal logic in templates:** `{{if}}`, `{{range}}`, `{{with}}`, and
  pipeline functions only. All computation happens in Go; pass simple,
  pre-shaped data structures.
- htmx endpoints return **HTML fragments** (not JSON) for partial updates;
  full pages are returned for navigation. Forms use standard
  `method="POST"` with CSRF protection (see
  `viewer-security.instructions.md`).
- Every mutation goes through the backend; the browser never talks to storage
  directly (architecture rule from the security spec).

---

## 6. Prefer Raw HTML

- "Raw HTML" means hand-written, plain, semantic markup in templates — no
  client-side rendering frameworks, no JS-generated DOM, no HTML assembled by
  string concatenation in Go.
- Never build HTML in Go code (no `fmt.Sprintf("<td>%s</td>", ...)`).
  Dynamic output is always a template.
- Prefer semantic elements (`<main>`, `<nav>`, `<table>`, `<form>`, `<label>`,
  ...) over `<div>` soup; accessibility is required (labels for inputs, `alt`
  text for images, logical heading order).
- Keep attributes static and obvious; a template that needs heavy logic is a
  sign the Go side should pre-compute the data.

---

## 7. Document Exported Types & Functions

- Every exported identifier (package, type, function, method, constant,
  variable) MUST have a doc comment that begins with its name:

  ```go
  // Store is a generic, type-safe content-addressed store for objects of type T.
  type Store[T any] struct { ... }

  // Put stores obj and returns its content address.
  func (s *Store[T]) Put(ctx context.Context, obj Object[T]) (Hash, error) { ... }
  ```

- Every package SHALL have a package comment (`// Package cas implements ...`).
- Comments document contracts, not code: preconditions, ownership (e.g.
  "the caller MUST Close the returned io.ReadCloser"), concurrency safety,
  and error behavior.
- Add runnable `Example` functions in `_test.go` files for non-obvious public
  API; they are documentation that cannot rot.
- `go doc` output must read cleanly; avoid comments that merely restate the
  code.

---

## 8. Use the Latest Go Generics Where Possible

- This repo's core is generic by design — `Store[T]`, `Codec[T]`,
  `Object[T]`, and the cache wrappers (see
  `cas-core.instructions.md`). Generics replace `any` + reflection and
  move type errors to compile time.
- Rules:
  - No `any` / `interface{}` in the exported API (architectural rule; the one
    documented internal exception is `Store[T].Get`).
  - Constrain type parameters with interfaces — including type sets / `~`
    unions — instead of accepting unconstrained `T` where semantics require
    methods or operations.
  - Use `comparable` for map keys and equality; use ordering constraints only
    where ordering is actually needed.
  - Prefer std `slices` / `maps` / `cmp` helpers over hand-rolled loops where
    they read better (1.21+).
  - Prefer `range` over slices/maps (1.22+) and `range` over functions
    (`iter`, 1.23+) for clean iteration.
  - Generic type aliases (1.24+) are allowed when an alias clarifies the API.
- **Go 1.27 language additions** (see
  [release notes](https://go.dev/doc/go1.27)):
  - **Generic methods** — a method may declare its own type parameters:
    `func (s *Store[T]) Map[R any](ctx context.Context, h Hash, f func(Object[T]) (R, error)) (R, error)`.
    Use them to add typed helpers to this repo's generic types (e.g. typed
    resolution on `Store[T]`). Caveats: methods of interfaces may not declare
    type parameters, and interface methods cannot be implemented by generic
    methods — so `Object[T]`'s methods remain non-generic.
  - **Generalized function type inference** — type inference now applies in
    all contexts where a generic function is assigned to (or converted to) a
    matching function type. Prefer writing generic functions whose type
    arguments can be inferred from their arguments.
  - **Field-selector keys in struct literals** — keys may be any valid field
    selector for the struct type (e.g. `Config{Server.Port: 8080}`), not just
    top-level field names. Use it only where it reads clearly.
- **Use generic types and generic functions where needed** — where they remove
  duplication or replace `any`/reflection — and no further.
- **Do not over-generalize.** If a generic abstraction serves a single use or
  adds indirection without removing duplication, write the concrete code.
  Generics serve readability and type safety — not cleverness.

---

## 9. Project Structure & Conventions

- Layout: `cas/` (public core library, `package cas`), `internal/`
  (implementation detail: `web` — the viewer —, `storage`, `index`; NOT
  importable outside the module), `cmd/` (thin `main` packages only),
  `examples/`.
- `internal/` is the home of every implementation detail: viewer handlers,
  middleware, config wiring. It is private by construction — Go rejects
  imports of `internal/` from outside the module, so `cas/` is the only
  public package (plus the `examples/gitlike/` example layer).
- Viewer HTTP middleware (authn, sessions, CSRF, login throttle) lives in
  `internal/web` (viewer-security). An example surface MAY add its own
  IP-based rate limiter (std-lib token bucket per caller IP, 429 +
  `Retry-After` + `X-RateLimit-*`, loopback exempt — see `examples/api` and
  api-design §8).
- `go.mod` at the repo root declaring `go 1.27`; module path matches the
  repository.
- No blank imports except the `embed` pattern; no init-based magic except
  object/hash registration per the architecture doc.
- Tests: core `cas/` paths require tests for every exported function;
  handlers use `net/http/httptest`; template FS fixtures use
  `testing/fstest`.
- Verification before commit:

  ```text
  gofmt -l .
  go vet ./...
  go test ./...
  go build ./...
  ```

---

## 10. Frontend Boundary (`internal/web/`)

- The viewer backend (`internal/web/`) serves `html/template` pages and htmx
  fragments over `net/http`.
- No build step, no npm, no static asset pipeline: templates are embedded with
  `embed.FS`; htmx is one pinned file (vendored locally preferred; a CDN URL
  is acceptable only with an integrity attribute).
- The viewer MUST comply with
  `.github/instructions/viewer-security.instructions.md` (secure by default,
  authn/authz, session management, CSRF, audit logging). The no-CSS/no-JS rule
  is part of keeping the viewer minimal and auditable.

---

## 11. Pre-Commit Checklist

- [ ] `gofmt -l .` is clean; `go vet` and `go test` pass
- [ ] `go.mod` declares `go 1.27`; zero external Go dependencies, or each one
      justified and vendored
- [ ] No CSS, no hand-written JS, no `<style>`/`<script>` in templates — htmx
      only
- [ ] HTML rendered exclusively via `html/template` using the latest template
      feature set (`ParseFS`, composition, `break`/`continue` in `{{range}}`,
      `FuncMap`); no HTML string concatenation in Go
- [ ] Every exported identifier documented (name-first doc comments)
- [ ] Generic types/functions used where needed (incl. 1.27 generic methods);
      nothing over-engineered, no features newer than 1.27
- [ ] `context.Context` first, errors wrapped with `%w`, no panics in library
      code
- [ ] Viewer changes re-checked against
      `.github/instructions/viewer-security.instructions.md`
