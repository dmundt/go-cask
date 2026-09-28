---
type: Specification
title: API Design — go-cask
description: Shared conventions for every HTTP endpoint in go-cask — naming, methods, status codes, errors, authn/authz, rate limiting, validation, pagination, streaming, versioning, and OpenAPI documentation (in separate embedded .yaml files) — applied to the viewer surface and to example HTTP surfaces.
version: v16
---

# API Design — go-cask

- Shared conventions for every HTTP endpoint; a new endpoint MUST follow them unless its spec overrides.
- Related: viewer-security, performance, library-design, coding-guidelines.

## 1. Scope

- Viewer (`/viewer/*`, `text/html`, routes in `viewer-design.md`) = product's only shipped HTTP surface; `examples/api` (JSON/octet-stream) = example surface.

## 2. Surfaces, one style

| Surface | Prefix | Content types | Auth | Consumers |
|---|---|---|---|---|
| Viewer (product) | `/viewer/` | `text/html` (pages + fragments) | session cookie | browser only |
| Example JSON (`examples/api`) | app-chosen (`/api/cas/v1/` pattern) | `application/json`, `application/octet-stream` | bearer token | demo/tests |

- Prefix decides the contract; never mix prefixes or types.
- Same grammar (naming, errors, codes, middleware); only type and auth differ.

## 3. Naming and URL conventions

- Collections: plural resource nouns (`/objects`, `/stats`).
- Sub-resources nested one level: `/objects/{hash}/meta|raw|verify`.
- Actions = POST sub-resources (`/verify`, `/gc`); never GET side effects, never bare top-level verbs.
- Path segments lowercase, hyphenated when multi-word.
- Query params short/lowercase (`q`, `limit`, `offset`); defaults/bounds documented.
- Hash params always `{hash}`: `sha256:hexdigest` or bare lowercase hex, parsed with `sha256.Parse`; malformed → 400.
- Core `Digest` names no algorithm.

## 4. Methods and semantics

| Method | Use | Body | Success |
|---|---|---|---|
| GET | read; MUST be side-effect free | — | 200 |
| POST | create (server-computed) or action | yes | 201 (create) / 200 (action) |
| DELETE | delete; idempotent (missing = no-op) | — | 204 |

- No `PUT` in the example v1 — objects immutable (change = new hash); creates = POST, server-computed hash.
- Side-effecting actions (`verify`, `gc`, login) = POST; `verify` read-only in effect, POST because role-gated.
- `PUT`/`PATCH` reserved for a future mutable resource (full replace).

## 5. Status codes

| Status | Viewer | JSON surface |
|---|---|---|
| 200 | HTML page/fragment | JSON / octet-stream |
| 201 | — | created (`POST /objects`) |
| 204 | — | deleted / verified OK |
| 303 | login redirect | — |
| 400 | minimal error page | `{"error":"..."}` |
| 401 | **empty body** | `{"error":"unauthorized"}` |
| 403 | **empty body** | `{"error":"forbidden"}` |
| 404 | minimal error page | `{"error":"not found"}` |
| 413 | minimal error page (oversized body) | — |
| 429 | **empty body** + `Retry-After` | `{"error":"rate limited"}` + `Retry-After` |

- 401/403 never disclose internals or target existence (all surfaces).
- Create → 201 + hash; mutation with no useful body → 204.
- 429 = shared rate-limit middleware, pre-handler.
- `Retry-After` on every 429, whole seconds = enforced delay: middleware window (JSON), login block on `/viewer/login` (viewer-security §5).
- Rejected auth: empty body on any surface, browser form included — `POST /viewer/login` bad token → `401`, like a missing/expired session.
- Login page states the reason to a returning caller (viewer-design §3); the refusal never names token/account.
- Non-same-origin token login → 403, empty body, before any session; token validity never revealed (viewer-security §5.1; URLs §7).

## 6. Error contract

- JSON surfaces: every error is `{"error": "<concise message>"}` — actionable, no stack traces, internal paths, secrets, object bytes.
- Viewer error text = own prose: failures classified against the `cas` sentinels; the Go error goes to the audit line, not the response (viewer-design §3).
- Sentinels → statuses (per surface): `ErrNotFound`→404, `ErrDigestMismatch`→409/500, `ErrInvalidDigest`→400.
- Exception: `verify` returns `{"valid":true/false}` on 200, not an error; `ErrDigestMismatch` → error status only on mutation paths.

## 7. Authn/authz

- Viewer: session cookie, always `HttpOnly`, `SameSite=Strict`, `Secure`.
- Startup token **only** via `POST /viewer/login` or `GET /viewer/?token=`; both MUST be same-origin (viewer-security §5.1).
- Other endpoints require a valid session.
- JSON surfaces: `Authorization: Bearer <token>`, tokens per role.
- Roles: `viewer` (reads) → `operator` (+store, verify) → `admin` (+delete, GC, maintenance).
- Destructive actions live on a JSON surface (`examples/api`, §12), not the viewer; `admin` there reaches nothing `operator` does (viewer-security §8, consistency §9).
- CSRF: viewer mutation = POST + server-validated token in the request body or the `X-CSRF-Token` header; `?_csrf=` never accepted [URLs leak via logs, proxies, `Referer`] (viewer-security §5).
- Audit: every mutation logged; tokens/secrets never logged.
- Session-reflecting responses not cacheable; viewer sends `Cache-Control: no-store` always, names `Cookie` in `Vary` (viewer-security §10).

## 8. Middleware pipeline (shared)

- Fixed order: **rate limit → auth → CSRF → handler**.
- Viewer: session auth + CSRF on mutations + login throttle. JSON surface: bearer auth, no CSRF.
- IP middleware MAY wrap a JSON surface before auth (`examples/api`: 2 req/s per IP, burst 20, 429 + `Retry-After` + `X-RateLimit-*`, loopback exempt).
- Viewer login throttle: fixed 5 failures/IP/min with backoff (viewer-security); 429 carries the remaining block as `Retry-After`.
- Rate-limit config: `trusted_proxies` only for `X-Forwarded-For`.

## 9. Validation

- `{hash}`: `sha256.Parse` first (`sha256:hexdigest` or bare hex) → 400 on malformed.
- JSON query params (`examples/api`): out-of-range → 400, never clamped; `limit` 1–1000, `offset` ≥ 0.
- Viewer HTML object list exception (following `viewer-design` §5): limits 25/50/100/250, clamping not 400.
- Request bodies: strict decoding; unknown JSON fields rejected (`json.Decoder.DisallowUnknownFields` where sensible).
- A surface refuses a body over its bound with `413` before reading it; oversized or multipart bodies never buffered or spooled.
- Viewer bound 4 KiB, one middleware every route inherits (viewer-security §13, defaults §4); a body-parsing surface states its own bound.
- Never trust client input — header, query, body validated (viewer-security).

## 10. Pagination and filtering

- Offset pagination, no cursor: `?limit=<1..max>&offset=<0..>`, documented defaults.
- Envelope `{"total": <int>, "<items>": [...]}` (`<items>` = plural resource name); `total` semantics per endpoint.
- Filters = query params (`q` = viewer hash/type search); change the set, never item shape.
- Viewer renders HTML, not an envelope.
- Viewer object browser: `limit`/`offset` + filter/sort state; limits, defaults, fragment, invalid-query behavior per `viewer-design.md` §5.
- Table, count, pager = one response, so they cannot disagree.

## 11. Streaming and binary payloads

- Binary bodies `application/octet-stream` — **never base64 in JSON**.
- Binary metadata in `X-CAS-*` (`X-CAS-Algorithm`, `X-CAS-Size`); no `X-CAS-Type` — `meta` sniffs it best-effort.
- Large payloads stream (`io.Reader`/`io.ReadCloser`); handlers never buffer whole objects (performance P-05).

## 12. Versioning

- A JSON surface MAY carry its major in the URL prefix (`/api/cas/v1`): breaking change = new major; additive within.
- Viewer unversioned — htmx fragments evolve with the UI.
- Version in the URL, never headers.

## 13. Documentation and OpenAPI

- Endpoints MUST be documented in the OpenAPI document the surface serves (`examples/api`: `/api/cas/v1/openapi.yaml`).
- OpenAPI MUST live in separate files — `openapi.yaml` beside the serving code, embedded via `//go:embed` + `embed.FS`; never an inline Go string.
- Docs MUST match implemented routes exactly; CI regenerates/compares on route change.
- HTML viewer needs no OpenAPI (hypermedia, `viewer-design.md`).

## 14. Designing a new endpoint

1. Surface — HTML/htmx → `/viewer/`; JSON → example surface; never mixed (§2).
2. Resource — plural noun path, nesting, POST action sub-resource (§3).
3. Contract — method, body, response shape/type, statuses (200/201/204 + 400/401/403/404/429).
4. Errors — JSON `{"error":…}` or minimal HTML; 401/403 disclose no existence (§6).
5. Middleware — rate limit, auth, CSRF, role check; audit-log mutations (§7, §8).
6. Validate — `{hash}` via `sha256.Parse`; strict query/body (§9).
7. Document — the surface's OpenAPI (§13).
8. Test — `httptest`: status, roles, 429, streaming; fuzz input.

## 15. Checklist

- [x] Correct prefix; content type matches the surface
- [x] Naming per §3; `{hash}` parsed with `sha256.Parse`
- [x] Methods per §4 (GET side-effect free, POST create/action, DELETE idempotent)
- [x] Status codes/errors per §5/§6
- [x] Auth, CSRF, roles, rate limit, audit per §7/§8
- [x] Validation per §9; pagination envelope per §10
- [x] Binary streams as octet-stream + `X-CAS-*` (§11)
- [x] Versioned per §12; OpenAPI per §13
- [x] `httptest` coverage; docs regenerated
