---
type: Specification
title: API Design — go-cask
description: Shared conventions for every HTTP endpoint in go-cask — naming, methods, status codes, errors, authn/authz, rate limiting, validation, pagination, streaming, versioning, and OpenAPI documentation (in separate embedded .yaml files) — applied to the viewer surface and to example HTTP surfaces.
version: v6
---

# API Design — go-cask

Common API design conventions for every HTTP endpoint. The product's only HTTP surface is the viewer (`/viewer/*`, HTML; routes in `viewer-design.md`); `examples/api` demonstrates a JSON surface for app authors. A new endpoint MUST follow these conventions unless its own spec overrides them. Related: viewer-security, performance, library-design, coding-guidelines.

## 1. Scope

Applies to every endpoint: the viewer (`/viewer/*`, `text/html`) and any example HTTP surface (`examples/api`, JSON/octet-stream). Governs naming, methods, status codes, errors, authn/authz, rate limiting, validation, pagination, streaming, versioning, OpenAPI docs.

## 2. Surfaces, one style

| Surface | Prefix | Content types | Auth | Consumers |
|---|---|---|---|---|
| Viewer (product) | `/viewer/` | `text/html` (pages + fragments) | session cookie | browser only |
| Example JSON (`examples/api`) | app-chosen (pattern `/api/cas/v1/`) | `application/json`, `application/octet-stream` | bearer token | demo/tests |

- A route's prefix decides its contract; never mix prefixes or content types across surfaces. Same grammar (naming, errors, codes, middleware) on all — only content type and auth differ. The viewer is the only shipped surface.

## 3. Naming & URL conventions

- Plural resource nouns for collections (`/objects`, `/stats`).
- Sub-resources by nesting: `/objects/{hash}/meta|raw|verify` (one level).
- Actions are POST sub-resources (`/verify`, `/gc`) — never GET with side effects, never bare verbs at top level.
- Path segments lowercase, hyphen-separated when multi-word. Query params short/lowercase (`q`, `limit`, `offset`), documented defaults/bounds.
- Hash params always named `{hash}`, accepted as the printable `sha256:hexdigest` form or bare lowercase hex and parsed with the client's `sha256.Parse` (malformed → 400). The core's `Digest` carries no algorithm name.

## 4. Methods & semantics

| Method | Use | Body | Success |
|---|---|---|---|
| GET | read; MUST be side-effect free | — | 200 |
| POST | create (server-computed identity) or action | yes | 201 (create) / 200 (action) |
| DELETE | delete; idempotent (missing = no-op) | — | 204 |

- No `PUT` in the example v1 — objects are immutable (changed object = new hash); creates use POST with server-computed hash. Side-effecting actions (`verify`, `gc`, login) are POST; `verify` is read-only in effect but POST because it runs a role-gated check. `PUT`/`PATCH` reserved for a future mutable resource (full-replace semantics if added).

## 5. Status codes

| Status | Viewer | JSON surface |
|---|---|---|
| 200 | HTML page/fragment | JSON body / octet-stream |
| 201 | — | created (`POST /objects`) |
| 204 | — | deleted / verified OK |
| 303 | login redirect | — |
| 400 | minimal error page | `{"error":"..."}` |
| 401 | **empty body** | `{"error":"unauthorized"}` |
| 403 | **empty body** | `{"error":"forbidden"}` |
| 404 | minimal error page | `{"error":"not found"}` |
| 429 | minimal error page | `{"error":"rate limited"}` + `Retry-After` |

- 401/403 never disclose whether the target exists (all surfaces). Successful mutations with no useful body → 204; creates → 201 + the hash. 429 produced by the shared rate-limit middleware before any handler.

## 6. Error contract

- JSON surfaces: every error is `{"error": "<concise message>"}` — no stack traces, internal paths, secrets, or object bytes.
- Viewer: minimal HTML pages/fragments; 401/403 empty bodies.
- Messages actionable but never disclose internals/existence in 401/403.
- Sentinel errors → statuses (per-surface): `ErrNotFound`→404, `ErrDigestMismatch`→409/500, `ErrInvalidDigest`→400. Exception: `verify` is a query returning `{"valid":true/false}` on 200 (not an error); `ErrDigestMismatch` maps to the error status only on mutation paths.

## 7. Authn/authz

- Viewer: session cookie (`HttpOnly`, `SameSite=Strict`, `Secure` over HTTPS); the startup token is accepted **only** by `POST /viewer/login`; every other endpoint requires a valid session.
- Example JSON surfaces: `Authorization: Bearer <token>`, configured per-role tokens.
- Roles (all surfaces): `viewer` (reads) → `operator` (+store, verify) → `admin` (+delete, GC, maintenance).
- CSRF: every viewer mutation is POST + server-validated CSRF token.
- Audit: every mutation audit-logged; tokens/secrets never logged.
- Rate limiting: IP-based middleware MAY wrap a JSON surface before auth (`examples/api`: 2 req/s per IP, burst 20, 429 + `Retry-After` + `X-RateLimit-*`, loopback exempt); viewer login throttle fixed at 5 failures/IP/min with backoff (viewer-security).

## 8. Middleware pipeline (shared)

Fixed order: **rate limit → auth → CSRF → handler**. Viewer enforces it with session auth + CSRF on mutations (plus login throttle); a JSON surface uses bearer auth and no CSRF. Rate-limit config applies to JSON surfaces (2 req/s, burst 20, loopback exempt, `trusted_proxies` only for `X-Forwarded-For`).

## 9. Validation

- Every `{hash}`: `sha256.Parse` first (it accepts `sha256:hexdigest` and bare hex) → 400 on malformed.
- Query params: reject out-of-range with 400 (never silently clamp); `limit` bounded (1–1000), `offset` ≥ 0.
- Request bodies: strict decoding; reject unknown JSON fields (`json.Decoder.DisallowUnknownFields` where sensible).
- Never trust client input — header, query, and body all validated (viewer-security).

## 10. Pagination & filtering

- Cursor-free offset pagination: `?limit=<1..max>&offset=<0..>`, documented defaults.
- Envelope `{"total": <int>, "<items>": [...]}` (`<items>` = plural resource name); `total` semantics documented per endpoint.
- Filters are query params (`q` for the viewer's hash/type search); filters change only the set, never the item shape.

## 11. Streaming & binary payloads

- Binary bodies `application/octet-stream` — **never base64 in JSON**.
- Binary metadata in `X-CAS-*` headers (`X-CAS-Algorithm`, `X-CAS-Size`); no `X-CAS-Type` (byte layer has no envelope type) — `meta` may sniff it best-effort from the envelope.
- Large payloads stream (`io.Reader`/`io.ReadCloser`); handlers never buffer whole objects (performance P-05).

## 12. Versioning

- A JSON surface MAY carry its major in the URL prefix (`/api/cas/v1`): breaking changes require a new major; additive allowed within a major.
- The viewer is unversioned — htmx fragments evolve with the UI.
- Versioning is in the URL, never headers.

## 13. Documentation & OpenAPI

- A JSON surface's endpoints MUST be documented in an OpenAPI document it serves (`examples/api` serves `/api/cas/v1/openapi.yaml`).
- OpenAPI MUST live in separate files — an `openapi.yaml` next to the serving code, embedded via `//go:embed` + `embed.FS`; never an inline Go string (the doc is data, must be diffable/lintable natively).
- Docs MUST match implemented routes exactly; CI regenerates/compares on route changes. The HTML viewer needs no OpenAPI (hypermedia surface per `viewer-design.md`).

## 14. Designing a new endpoint

1. Pick the surface — browser-facing (HTML/htmx) → `/viewer/`; programmatic (JSON) → an example surface; never both on one prefix.
2. Model the resource — plural noun path, nesting, action as POST sub-resource.
3. Define the contract — method, body, response shape/content type, statuses (200/201/204 + 400/401/403/404/429).
4. Define errors — JSON `{"error":…}` or minimal HTML; 401/403 never disclose existence.
5. Apply middleware — rate limit, auth, CSRF (viewer mutations), role check; audit-log mutations.
6. Validate — parse the `{hash}` with the client's `sha256.Parse`, strict query/body.
7. Document — add to the surface's OpenAPI.
8. Test — `httptest` for status/roles/429/streaming; fuzz complex input.

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
