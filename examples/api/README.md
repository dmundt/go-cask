# api — HTTP-exposure pattern: a store server over `cas`

**What it demonstrates.** A `cas` store exposed over HTTP: versioned prefix `/api/cas/v1`,
bearer-token roles, per-IP rate limiting, streaming upload/download, dedup, JSON errors,
OpenAPI self-doc. Public `cas` library + std-lib `net/http` only — no SDK, no `internal/`
(examples §2 rule 4). Acceptance: round-trip returns identical bytes; a viewer-role token gets
403 on `DELETE`; large payloads stream unbuffered; the OpenAPI document is served and matches
the routes.

## `cas` core parts used

| Component | Where |
|---|---|
| `fs.Backend` (`Put`/`Get`/`Exists`/`Delete`/`List`/`GC`/`Stats`) | all routes |
| `cas.Digest` / `sha256.Parse` | `{hash}` validation (→ 400), addresses |
| `sha256.NewHasher()` / `sha256.Name` (client-side; no registry) | `hash.go` — hash-on-write streaming, and the `"algorithm"` constant |
| `cas.Stats` | `/stats` (`object_count`, `total_size`, plus the constant `algorithm`) |

## What it extends

- **HTTP surface** — routes, bearer-token role middleware (401/403, no existence disclosure),
  std-lib per-IP token-bucket limiter (`ratelimit.go`: 429 + `Retry-After` + `X-RateLimit-*`,
  loopback exempt, trusted-proxy `X-Forwarded-For` only, lazy per-IP expiry + size guard).
- **Hash-on-write upload** — body Tee'd into a temp spool while hashing (`io.MultiWriter`); no
  in-memory buffering.
- **Dedup** — `Exists` before `Put`: identical bytes ⇒ identical digest ⇒ stored once,
  reported as `deduplicated`.
- **Single format** — no `algo` parameter: the store keys raw hex digests (the core names no
  algorithm), so list/meta/stats report `"algorithm": "sha256"` as the client's constant and
  `GET /objects/{hash}` returns `X-CAS-Algorithm: sha256`.
- **`envelopeType`** — best-effort type sniffing from the self-describing envelope for
  `/meta`.
- **`cas` untouched**; the server holds the store directly (typed serialization is app-layer).

## Relationship to the product

No HTTP data API or SDK ships (backend-architecture §1, §5): use the library in-process by
default, copy this server only when another process must reach the store, and treat it as your
app's own (auth, TLS, deployment are app concerns).

## Code walkthrough

- `server/server.go` — `server` + `Handler()`: routes wrapped by `requireRole` (auth), the mux
  by `rateLimit`. One handler per operation: `postObject` (store, dedup, streaming),
  `listObjects`, `getObject` (streams bytes + `X-CAS-*` headers), `deleteObject`, `objectMeta`,
  `verifyObject`, `gc`, `stats`, `openapi`. A `sizes` map (maintained at Put, pruned on
  delete/GC) supplies per-object sizes for list/meta/headers.
- `server/ratelimit.go` — `rateLimiter`: per-IP token buckets, lazy refill, idle expiry,
  max-entries guard.
- `server/hash.go` — `spoolAndHash` (hash-on-write via `sha256.NewHasher()` +
  `io.MultiWriter`), `envelopeType` (best-effort TLV header sniffing).
- `server/openapi.yaml` — the OpenAPI document, `//go:embed`-ed, served at
  `/api/cas/v1/openapi.yaml` (api-design §13: never an inline Go string).
- `server/main.go` — flags (`-store`, `-bind`, `-tokens`, rate-limit knobs), graceful shutdown.
- `demo/main.go` — plain `net/http` client (no SDK): PUTs a file, GETs it back, prints meta and
  stats.
- `server_test.go` — raw-HTTP tests: round-trip + dedup, 4 MiB streaming, role matrix, 429
  burst, meta/verify/list/stats, gc, openapi, 400 malformed hash.

```mermaid
flowchart TB
    REQ["HTTP request"] --> IP["rate limit (per-IP token bucket)"]
    IP -->|"429 + Retry-After + X-RateLimit-*"| R1["reject"]
    IP -->|"ok"| AUTH["bearer role auth"]
    AUTH -->|"401/403 (no existence disclosure)"| R2["reject"]
    AUTH -->|"ok"| RT["route handler"]
    RT -->|"POST /objects"| SP["hash-on-write temp spool"]
    RT -->|"GET /objects/{hash}"| ST["stream bytes + X-CAS-*"]
    RT -->|"meta / list / verify / gc / stats"| FS["fs.Backend"]
```

## How to run

```text
# terminal 1 — server (tokens: viewer=viewer, operator=operator, admin=admin)
go run ./examples/api/server -store ./objects -bind 127.0.0.1:8080

# terminal 2 — demo round-trip over plain HTTP
go run ./examples/api/demo -api http://127.0.0.1:8080 -token operator -file ./README.md

# explore
curl -H "Authorization: Bearer operator" http://127.0.0.1:8080/api/cas/v1/stats
curl -H "Authorization: Bearer viewer" http://127.0.0.1:8080/api/cas/v1/openapi.yaml

go test ./examples/api/...
```

Prints `stored <hex digest> deduplicated=…`, `fetched N bytes`, and meta/stats lines.
