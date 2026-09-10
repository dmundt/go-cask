---
type: Specification
title: Backend Architecture — go-cask
description: How the go-cask backend is put together — process and binary layout (cmd/cask thin main over internal/), the viewer server (started by `cask web`), middleware pipeline, storage backend selection, configuration, observability, and deployment shapes.
version: v16
---

# Backend Architecture — go-cask

How the `cas` library is composed into a runnable system (binary layout, HTTP layer, middleware, config, lifecycle, observability, deployment). Library internals are in `cas-core.md`; this is the process around them. **No network JSON API ships.** go-cask is a single-host kit: `cas` + CLI + the embedded viewer; `cask web` is the only HTTP surface. Serving a store to other machines is an app pattern shown by `examples/api`. Related: viewer-design, viewer-security, api-design, operations, coding-guidelines.

## 1. Purpose & scope

- The backend is all server-side code: binary, viewer HTTP layer, wiring of `cas` into handlers, config, lifecycle.
- Handlers are **thin** — all logic lives in the library; the backend composes it and adds HTTP concerns (authn/authz, CSRF, validation, streaming).
- One codebase serves all shapes (viewer via `cask web`, CLI, library embedding) — never separate forks. A process serving a store to other machines copies the `examples/api` pattern (§5); the product never ships that server.

## 2. Process & binary layout

- `cmd/cask` is the only binary and a **thin main**: all viewer logic lives in `internal/` (`web` handlers+templates over `/viewer/*`, `index` listing/meta helpers); `cask web` wires the internal packages. `internal/` MUST NOT be imported outside the module (Go-enforced).
- `cas/` is the public surface (embedded library + `gitlike/`); everything else is private. Non-`web` `cmd/cask` subcommands are a thin CLI over the same library — the library is the single source of behavior.
- No `internal/api`, no bearer-token `internal/auth`, no `client/` SDK — the network JSON API was removed to keep the kit single-host (§1); HTTP-exposure patterns live in `examples/api`.
- **No product → example imports:** `cas/`, `internal/`, `cmd/` MUST NOT import `examples/` (downstream consumers, never upstream deps).
- Forbidden edges: `cas/`·`internal/`·`cmd/` → `examples/`; `examples/` → `internal/`; example → example except the sanctioned `files → gitlike` shared-support dependency. `internal/index` and `examples/api/demo` import nothing from the module.

## 3. The viewer server (`cask web`)

- One `net/http` server, one mux (Go 1.22+ pattern routing); every route under `/viewer` — no second surface (api-design §2).
- Fixed middleware order: session auth → role → CSRF (mutations) → handler; login behind its own failure throttle (viewer-security, api-design §8).
- The viewer never talks to storage directly from handlers — it goes through `Backend`/`Store[T]`, so backend selection is config, not code.
- Handler set lives in `internal/web`, over the `cas` library and `internal/index`; `cmd/cask web` only wires them.

## 4. HTTP layer

- Every viewer route is `text/html` (pages + htmx fragments). The raw view buffers at most **256 KiB** for in-page hexdump (a bounded preview, not a streaming download — api-design §11 streaming applies to the API surface, not the hexdump UI).
- Errors are minimal HTML; 401/403 are empty bodies never disclosing existence.
- The product serves no OpenAPI; an HTTP surface needing a documented contract (`examples/api`) keeps it in a separate embedded `openapi.yaml` (api-design §13).

## 5. Storage backend selection

- Config selects the backend: `fs` (Git-like fan-out) or `memory` (tests/ephemeral) — cas-core §4.4–4.5.
- The viewer and CLI talk to the library **in-process only** — no remote backend, no client SDK. Serving to other machines is an app concern (copy the `examples/api` pattern; run as that app's server). The product ships no such server.
- **One store directory ↔ one writer process, grace for sweeps.** `cas` concurrency safety is per-process: any number of goroutines/HTTP clients may share one store within a process. Across OS processes, writes/reads are safe by construction (atomic rename, unique temps). A maintenance sweep racing another process's writes needs care: the `cask` CLI uses the grace model — writers and `web` run lock-free; `gc`/`prune`/`clean` take the exclusive `.cask.lock` (one sweep at a time) and reclaim only objects older than `--min-age` (default 1h); a forced `--min-age 0` sweep is the dangerous variant (cli §2, cas-core §6). Scale by serving more clients from one process or sharding store directories; embedding apps provide equivalent coordination.

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
  secure_cookies: false  # true over HTTPS (behind a proxy)
```

- **Startup:** validate config → construct the store (create dirs, validate fan-out bounds) → generate the viewer startup token (printed once, never stored in plaintext config) → start serving.
- **Shutdown:** graceful — `signal.NotifyContext`, stop accepting, drain in-flight, close the store; no mid-write corruption (atomic-rename contract).
- Config-file support (`-config`) deferred — flags only (cli §2).

## 7. Observability & audit

- `log/slog`: viewer mutations, slow operations, GC runs (operations §3), login failures.
- Audit per viewer-security: every admin action logged; tokens/secrets never logged.
- Metrics counters (objects/bytes/cache) via the viewer stats page and logs — no external metrics dependency.

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
- [x] Raw object views stream; no full buffering
- [x] Errors per api-design §5/§6; 401/403 empty bodies
- [x] Config per §6; startup/shutdown lifecycle implemented
- [x] slog + audit logging; metrics via the viewer stats page and logs
- [x] No network JSON API ships; `examples/api` is the documented pattern
