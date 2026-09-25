---
type: Specification
title: Backend Architecture — go-cask
description: How the go-cask backend is put together — process and binary layout (cmd/cask thin main over internal/), the viewer server (started by `cask web`), middleware pipeline, storage backend selection, configuration, observability, and deployment shapes.
version: v26
---

# Backend Architecture — go-cask

How the `cas` library is composed into a runnable system (binary layout, HTTP layer, middleware, config, lifecycle, observability, deployment). Library internals are in `cas-core.md`; this is the process around them. **No network JSON API ships.** go-cask is a single-host kit: `cas` + CLI + embedded viewer, `cask web` being the only HTTP surface. Serving a store to other machines is an app pattern shown by `examples/api`. Related: viewer-design, viewer-security, api-design, operations, coding-guidelines.

## 1. Purpose and scope

- The backend is all server-side code: binary, viewer HTTP layer, wiring of `cas` into handlers, config, lifecycle.
- Handlers are **thin** — all logic lives in the library; the backend composes it and adds HTTP concerns (authn/authz, CSRF, validation, streaming).
- One codebase serves all shapes (viewer via `cask web`, CLI, library embedding) — never separate forks. A process serving a store to other machines copies the `examples/api` pattern (§5); the product never ships that server.

## 2. Process and binary layout

- `cmd/cask` is the only binary and a **thin main**: all viewer logic lives in `internal/` (`web` handlers+templates over `/viewer/*`, `index` listing/meta helpers); `cask web` wires the internal packages. `internal/` MUST NOT be imported outside the module (Go-enforced).
- `internal/store` is the CLI's backend-selection seam: it opens the backend `-backend` names and reports that backend's `cas.Capabilities`, so a subcommand never reaches for a concrete backend type (cli §1).
- `cas/` is the public surface (embedded library + `gitlike/`); everything else is private. Non-`web` `cmd/cask` subcommands are a thin CLI over the same library — the library is the single source of behavior.
- No `internal/api`, no bearer-token `internal/auth`, no `client/` SDK — the network JSON API was removed to keep the kit single-host (§1); HTTP-exposure patterns live in `examples/api`.
- **No product → example imports:** `cas/`, `internal/`, `cmd/` MUST NOT import `examples/` (downstream consumers, never upstream deps).
- Forbidden edges: `cas/`·`internal/`·`cmd/` → `examples/`; `examples/` → `internal/`; example → example except the sanctioned `files → gitlike` shared-support dependency. `internal/index` and `examples/api/demo` import nothing from the module.

## 3. The viewer server (`cask web`)

- One `net/http` server, one mux (Go 1.22+ pattern routing); every route under `/viewer` — no second surface (api-design §2).
- The server sets `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout` and `IdleTimeout` (defaults §4), so a client that completes the header phase cannot hold the connection and its goroutine open by dribbling or stalling a body (viewer-security §13).
- Fixed middleware order: the viewer's own hardening (response headers and the request-body bound, in one place for every route) → session auth → role → CSRF (mutations) → handler; login sits behind its own failure throttle (viewer-security §13, api-design §8).
- The viewer never talks to storage directly from handlers — it goes through `Backend`/`Store[T]`, so backend selection is config, not code.
- Handlers live in `internal/web`, over the `cas` library and `internal/index`; `cmd/cask web` only wires them.

## 4. HTTP layer

- Every viewer route is `text/html` (pages + htmx fragments). The only
  presentation asset is the embedded, locally served
  `/viewer/static/viewer.css`; vendored htmx is the only script. The hexdump
  view buffers at most **256 bytes** (`internal/web/web.go`'s `previewLimit`)
  for in-page display and states the truncation on the page (a bounded preview,
  not a streaming download — api-design §11 streaming applies to the API
  surface, not the hexdump UI).
- Errors are minimal HTML; 401/403 are empty bodies never disclosing existence.
- A request body is bounded before it is parsed: the viewer caps it at 4 KiB in the outermost hardening middleware — so every route, including one added later, inherits the bound — refuses a longer declared body `413` without reading a byte of it, and parses forms with `ParseForm`, never `ParseMultipartForm`, so a multipart body is refused rather than buffered in memory and spilled to temp files (viewer-security §13, defaults §4).
- The product serves no OpenAPI; an HTTP surface needing a documented contract (`examples/api`) keeps it in a separate embedded `openapi.yaml` (api-design §13).

## 5. Storage backend selection

- Config selects the backend: `fs` (Git-like fan-out) or `memory` (tests/ephemeral) — cas-core §4.4–4.5.
- The `cask` CLI selects the backend per invocation with `-backend fs` (default) or `-backend packfs` (a loose tree plus append-only pack files, `cas/backend/packfs`). One shared internal constructor (`internal/store`) opens it and returns `cas.Capabilities` with it, so every subcommand is backend-agnostic: `put`/`get`/`list`/`meta`/`stats` use the minimal `Backend` contract, `verify` runs through `cas.Verify`/`cas.VerifyAll`, `gc`/`prune` use the backend's native `Prune` when it has one and `cas.Sweep` otherwise, `clean` uses `cas.Cleaner`. An operation a backend cannot perform fails with `cas.ErrUnsupported` naming operation and backend (cli §2).
- The viewer is the one exception: `internal/web.New` reads per-object physical metadata through the concrete `*fs.Backend`, so `cask web -backend packfs` is refused with the same `cas.ErrUnsupported` error rather than served from a directory other than `-store` named. The refusal is actionable (it names the remedy: open a loose store) because a packed store is a legitimate choice for every other subcommand, and a packed object has no file of its own whose size and modification time the viewer could report (cli §1, §2; viewer-design §1).
- Every CLI command opens and closes its store, so a backend holding a write handle open (packfs keeps its active pack file for appends and persists its index through it) releases it before the command returns; nothing depends on a separate flush step.
- Optional advisory bloom front ends MAY wrap a backend or a custom store implementation for hot-path member checks, but never replace the backend's authoritative `Exists`. The backend remains the only correctness authority; the bloom layer is a performance aid for front-end lookup reduction.
- The viewer and CLI talk to the library **in-process only** — no remote backend, no client SDK. Serving other machines is an app concern (copy the `examples/api` pattern; run as that app's server); the product ships no such server.
- **One store directory ↔ one writer process, grace for sweeps.** `cas` concurrency safety is per-process: any number of goroutines/HTTP clients may share one store in a process. Across processes, writes/reads are safe by construction (atomic rename, unique temps). A maintenance sweep racing writes needs care: the `cask` CLI uses the grace model — writers and `web` run lock-free, `gc`/`prune`/`clean` take the exclusive `.cask.lock` (one sweep at a time) and reclaim only objects older than `--min-age` (default 1h), and a forced `--min-age 0` sweep is the dangerous variant (cli §2, cas-core §6). Scale by serving more clients from one process or sharding store directories; embedding apps provide equivalent coordination.

## 6. Configuration

```yaml
storage:
  type: fs            # fs | memory
  path: ./objects
  fan_out: 2          # Git-like fan-out (cas-core §4.4)
  fan_levels: 1
viewer:
  bind: 127.0.0.1:8080
  roles: {}           # role=token pairs for viewer login
```

- **Startup:** validate config → construct the store (create dirs, validate fan-out bounds) → generate the viewer startup token, or take the operator's from `-token-file`/`CASK_VIEWER_TOKEN` (never logged at any level; the one-time notice goes to stdout — an interactive terminal, or a run passing `-show-token` — **for a loopback bind only**, cli §4, viewer-security §11; a non-loopback bind displays no token and prints the bind and the `https://` expectation instead of a login link, and the browser launch carries the token deep link only under the same conditions) → start serving.
- **Shutdown:** graceful — `signal.NotifyContext`, stop accepting, drain in-flight, close the store; no mid-write corruption (atomic-rename contract).
- Config-file support (`-config`) is deferred — flags only (cli §2).

## 7. Observability and audit

- `log/slog`: viewer audit lines (login failures, throttle and CSRF rejections, verify results) and `cask web` lifecycle errors; slow-operation logging is not implemented (operations §3).
- Audit per viewer-security: every admin action logged; tokens/secrets never logged.
- Metrics counters (objects/bytes/cache) and the viewer stats page they were to be exposed on are **not implemented**: `cas.Stats` reports object count and total bytes, the cache layer reports its own `CacheStats`, and the viewer's route table has no stats route (`internal/web/web.go`). `log/slog` is the observability surface (operations §3, extensions §3).

## 8. Deployment shapes

1. **Local admin** — `cask web` on the store's machine: viewer in-process over the library (default; loopback bind).
2. **CLI / embedding** — non-`web` subcommands and library consumers.
3. **App-served stores** — an embedding app MAY expose its store over HTTP by copying the `examples/api` pattern; that server is the app's own (auth/TLS/deployment are app concerns). The product ships no such server.

All product shapes share the config contract and the viewer security model; an app-served store is outside the product and follows api-design as an example surface.

## 9. Security

- The viewer enforces, in order: session auth (startup-token login behind its own throttle), role checks (viewer/operator/admin), CSRF on mutations, strict input validation (`sha256.Parse`, bounded params) — viewer-security + api-design §7–§9.
- 401/403 never disclose object existence (empty bodies); error messages never leak internals.

## 10. Checklist

- [x] One mux; every route under `/viewer`; no data-API surface
- [x] Middleware order fixed: sessions → role → CSRF → handler
- [x] Handlers thin; logic in the library; backend selection via config
- [x] CLI backend selection via `-backend` through one shared constructor, with capabilities; unsupported operations named
- [x] Raw object views stream; no full buffering
- [x] Errors per api-design §5/§6; 401/403 empty bodies
- [x] Config per §6; startup/shutdown lifecycle implemented
- [x] slog + audit logging
- [ ] metric counters and a viewer stats page — not implemented; `cas.Stats` and the cache layer's `CacheStats` are the only counters, and the viewer has no stats route (extensions §3)
- [x] No network JSON API ships; `examples/api` is the documented pattern
