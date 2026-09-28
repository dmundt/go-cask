---
type: Specification
title: Viewer Security — go-cask
description: Security requirements for the embedded viewer — secure by default, authn/authz, session management, cookie requirements, and audit logging.
version: v18
---

# Viewer Security — go-cask

Security requirements for the embedded technical viewer. **Nothing may weaken this file** (AGENT.md
§8 precedence); viewer design and HTTP surface MUST comply. Related: `viewer-design.md`,
`api-design.md`.

## 1. Project context and intent

Object-store inspection/integrity tool for **developers, operators, and troubleshooting**. **Not
intended to be publicly accessible**; MUST always be secure-by-default.

## 2. Guiding priority

Groups: access, sessions, authorization, operations, deployment. Priority: §14.

## 3. Secure by default (explicit enablement)

- Viewer SHALL run only when explicitly invoked; `cask web` starts it, no other subcommand does.
- No separate API server, no `enabled` switch — invoking `cask web` IS the explicit enablement
  (backend-architecture §1).
- All else off by default: loopback §4, auth §5.

## 4. Localhost only

| Bind | Rule |
| --- | --- |
| Default | SHALL be `127.0.0.1` or `localhost`; MUST NOT expose all interfaces |
| Loopback | SHALL be pinned to an explicit numeric loopback address before the listener is created; spellings `127.0.0.1`, `localhost`, `[::1]`; `localhost` MUST resolve, in the viewer, to `127.0.0.1` — not via the host resolver (cli.md §2, go-cask#337) |
| Exposure | explicit config change only (`bind`) (cli.md §2) |
| Non-loopback | MUST log a prominent startup warning; MUST refuse to start unless HTTPS is enabled or `allow_insecure_bind: true` is set |
| Non-loopback | MUST NOT print a login link (cookie always `Secure`, §7); notice MUST name the bind and the `https://` expectation (cli.md §2) |

## 5. Authentication

- SHALL guard all **protected** resources; unauthenticated: the login page (`/viewer/login`) and the
  `/viewer/` landing (303 to login, or the same-origin `?token=` deep link, §5.1) — neither exposes
  store data.
- Login MUST be rate limited (max 5 failures/caller-address/min, exponential backoff); each failure
  MUST be audit-logged without the token value. Throttle keys on the **caller address** of §5.2, not
  the direct peer.
- Authentication alone MUST NOT let one session monopolize the server: store-proportional routes
  (verify-all, metadata-snapshot rebuild) are bounded per session and server-wide, answer
  `429` + `Retry-After` past the bound, never queue behind a running operation (viewer-design §3,
  defaults.md); refusal audit-logged.
- **Preferred mechanism — startup-generated admin token:** grants `admin`. Other viewer/operator
  principals: configured identity provider (OIDC) or per-role tokens. Sessions MUST carry exactly one
  role, resolved at login.
- **Startup token:** cryptographically secure random; supplied with
  `-token-file`/`CASK_VIEWER_TOKEN`, or shown once on **standard output** (interactive terminal, or
  `cask web -show-token`), loopback bind only (§5.1, §7, §11); never via the logging package at any
  level (cli.md §4, §9, §11); never plaintext config; regenerated each restart unless supplied.
- Session establishment: `POST /viewer/login` (preferred) or `GET /viewer/?token=<token>`, the
  `cask web` "open viewer" deep link. Every other endpoint MUST reject the token and require a
  session cookie. Token-accepting endpoints accept a token only from the viewer's own origin (§5.1).
- **CSRF (MUST):** every viewer mutation is POST with a per-session CSRF token compared in constant
  time; the token MUST come from the request body (form hidden field) or `X-CSRF-Token`, never the
  query string (URL-borne tokens leak to logs, bookmarks, proxies, `Referer`). A `?csrf=<token>` value
  MUST NOT validate.

### 5.1 Direct `?token=` deep link (acceptance)

`GET /viewer/?token=<token>` is the `cask web` "open viewer" deep link. Both token-accepting
endpoints — `POST /viewer/login`, token-bearing `GET /viewer/` — MUST establish same-origin first,
else answer `403` with an empty body (§13) plus an audit line (§9); cross-site carriers (`<img>`,
`<link>`, navigation) MUST NOT mint a session.

| Signal | Verdict |
| --- | --- |
| `Sec-Fetch-Site: same-origin` | form post, link or htmx request from a viewer page — accepted |
| `Sec-Fetch-Site: none` | top-level navigation, no initiator; unforgeable — accepted |
| `same-site`, `cross-site` | not the viewer's origin; MUST be refused even with a valid token |
| absent | fallback: `Origin` naming the request's own host; two missing headers MUST fail closed |

- Refused: MUST NOT create a session or set a cookie, and MUST be refused before the login throttle.
- Token lives only in that login URL: MUST NOT be echoed into session, cookies or logs; the server
  MUST send `Referrer-Policy: no-referrer` so it does not leak via `Referer`; throttle and audit
  line (no token value) still apply; the client MUST NOT reuse the token URL once the cookie is set.
- Notice: loopback prints `http://<addr>/viewer/?token=…`; non-loopback MUST NOT print a link (its
  plain `http://` cannot hold the session, §7) — it names the bind and the `https://` expectation.

### 5.2 Caller address (proxy deployments)

Throttle keys on the caller address: the direct TCP peer, unless a **configured trusted proxy**
forwards a client address on its behalf (`internal/web/proxy.go`).

- `cask web -trusted-proxy <ip|cidr,...>` and `TrustedProxies` name the peers whose forwarded address
  may be believed: CIDR blocks (`10.0.0.0/8`) or single IPs (`127.0.0.1`, `::1`, an `ip:port`, port
  ignored). Any other entry MUST fail viewer startup.

| Rule | Requirement |
| --- | --- |
| Default | **no** proxy trusted — key on `RemoteAddr` alone; a forwarded header is ignored in full; behavior MUST match the pre-rule viewer exactly |
| Headers | matching direct peer only: `X-Forwarded-For`, else `Forwarded` (RFC 7239, its first `for=`) |
| Value | **rightmost address in the chain that is not itself a trusted proxy**; all hops trusted → leftmost; text left of that hop discarded, so no client picks its own bucket |
| Non-IP hop | verbatim, not normalized — normalizing could hand a caller another client's bucket |
| Keying | a forwarded address from a trusted peer MUST key the throttle; otherwise the peer address; the two cannot share a bucket. Session and audit log record the same caller address |
| Proxy duty | trusting a peer delegates caller identity: the proxy MUST overwrite the forwarded header, never append, and MUST NOT be directly reachable by arbitrary clients |

No trusted proxy behind a reverse proxy: **all clients share the peer's bucket** — five failures from
anyone block all operators for up to 30 minutes (`-trusted-proxy`, rate-limit there too, or bind
directly: §4, §12).

## 6. Session management

- After auth: secure session + session cookie; no per-request re-entry of the startup token.
- Idle timeout 30 min; maximum lifetime 8 h; re-authenticate on idle expiry, max lifetime or restart.

## 7. Cookie requirements

- Session cookies MUST use `HttpOnly`, `SameSite=Strict` and `Secure`; callers MUST NOT disable them.
- Sensitive data must never be stored in browser-accessible cookies.

## 8. Authorization (roles)

Authentication and authorization MUST be separated. Roles: `viewer`, `operator`, `admin`. The viewer
inspects; it never mutates the store (viewer-design §5, §11 below).

| Role | Permissions |
| --- | --- |
| `viewer` | list and browse objects, inspect metadata and references, read an object's bytes. Not: anything that writes to or removes from the store |
| `operator` | viewer permissions + verify one object, or every object in one sweep. Reads and re-digests; never mutates |
| `admin` | operator permissions. No destructive operation exists, so `admin` reaches nothing beyond `operator` |

Store-lifecycle operations (write, delete, garbage collection) belong to the CLI; a viewer gaining
one MUST extend this table in the same commit.

## 9. Audit logging

- All administrative actions MUST be logged: timestamp, user/session identifier, action, resource,
  result.
- Never log passwords, session cookies, authentication tokens or secret keys.
- `cask web` MUST NOT hand the startup token to the logging package at any level; it goes only to the
  one-time login notice on standard output, or nowhere (§5, §5.1, §11).

## 10. API architecture

- MUST talk only to the backend API; never direct browser access to storage internals.
- All authorization checks MUST occur in the backend (browser → viewer routes → object store).
- **Response hardening (MUST):** every response MUST carry `X-Content-Type-Options: nosniff`,
  `X-Frame-Options: DENY`, and this CSP — deny by default, own origin only; `connect-src 'self'`
  required, `style-src` allows inline, `script-src` strict:

  `default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'`

- **No viewer response is cacheable (MUST):** every response MUST carry `Cache-Control: no-store`;
  session-dependent pages MUST name `Cookie` in `Vary`.

## 11. Secret handling

- Secrets must never be hardcoded, committed, logged or returned in API responses (access/secret
  keys, session/startup tokens, encryption keys); use environment variables or dedicated providers.
- One URL exception: the documented `GET /viewer/?token=` login deep link (§5.1) — one-time, never
  logged, its response carrying `Referrer-Policy: no-referrer`.
- A startup token MAY come from `-token-file` or `CASK_VIEWER_TOKEN`, and MAY be shown once on
  standard output (§5) — loopback bind only (§5.1, §7).
- Otherwise it MUST NOT appear in the process log at any level, in the output of a process not asked
  to display it, or in an API response.
- Browser launch: only when the bind is loopback and the display is not suppressed with
  `-show-token=false`.

## 12. Production deployments

Remote access: **VPN + reverse proxy + OIDC/SSO + viewer** (e.g. Microsoft Entra ID, Keycloak,
Authentik, OAuth2 Proxy) — never directly on the public internet.

| Rule | Requirement |
| --- | --- |
| OIDC role | an OIDC/SSO proxy MUST derive the role from a configurable claim (default `groups`), map configured group names to viewer/operator/admin, and MUST deny access when no mapping matches |
| Same-origin | §5.1 reads `Sec-Fetch-Site` first, else the `Origin` host against the request `Host`; a proxy MUST preserve the browser's `Host` header |

Reverse proxy (§5.2), the direct peer for every client:

- Configure `cask web -trusted-proxy` with the proxy's address (§5.2).
- Trust only the proxy's own addresses, never a broad range: the proxy MUST overwrite
  `X-Forwarded-For` (not append) and MUST NOT be directly reachable by untrusted clients.
- Otherwise bind the viewer directly (§4).

## 13. Defensive programming

| Rule | Requirement |
| --- | --- |
| Input | validate query parameters, headers, JSON payloads and object/bucket names — never trust client input |
| Fail securely | 401 (empty body) for missing/expired sessions; 403 (empty body) for insufficient role on data endpoints or a not-same-origin token-bearing login (§5.1); never disclose whether the target bucket/object exists |
| Landing | `GET /viewer/` alone redirects (303) to `/viewer/login` with no session, and completes the same-origin direct `?token=` login (§5.1) |
| Body bound (MUST) | body cap **4 KiB** (a login token, a CSRF token, a digest — defaults §4) in one middleware every route inherits; over it, `413` **before** parsing; parsed with `ParseForm`, never `ParseMultipartForm`; an oversized `POST /viewer/login` creates no session; refusal audit-logged without the body (§9) |
| Connection bound (MUST) | `http.Server` sets `ReadTimeout`, `WriteTimeout` and `IdleTimeout` beside `ReadHeaderTimeout` (values in defaults §4) |

## 14. Security principle

Viewer = administrative tool. Priority: 1 Security, 2 Auditability, 3 Simplicity, 4 Convenience. When
in doubt, choose the more secure implementation.

## 15. Compliance checklist

- [x] Runs only via explicit `cask web`; loopback default; non-loopback needs HTTPS or `allow_insecure_bind: true` (§3–§4)
- [x] Loopback bind pinned to an explicit numeric loopback address before the listener exists (`localhost` → `127.0.0.1`); a bare `:port`, `0.0.0.0:port` or `[::]:port` is refused without the override like any other non-loopback bind (§4)
- [x] Auth required; login throttled (5/caller-address/min, backoff, audit-logged without the token) (§5)
- [x] A forwarded client address is believed only from a configured trusted proxy; with none, the peer address keys the throttle and the header is ignored (§5.2)
- [x] Startup token accepted only by `POST /login` **or** `GET /viewer/?token=` (§5); regenerated per start; never plaintext (§5); never logged — stdout once, loopback bind only (terminal or `-show-token`), or out of band via `-token-file`/`CASK_VIEWER_TOKEN` (§9, §11); own origin only (§5.1)
- [x] No login link for a non-loopback bind; the notice names the bind and the `https://` expectation instead (§4, §5.1)
- [x] No token displayed for a non-loopback bind, whatever the display choice; the browser launch is skipped unless the bind is loopback and the display is on, so the token cannot reach another process's argument vector (§11)
- [x] Both token-accepting endpoints refuse a non-same-origin request (403, empty body, audit line); no cross-site request mints a session (§5.1)
- [x] CSRF token from the POST body or the `X-CSRF-Token` header only; `?_csrf=` never validates (§5)
- [x] Sessions: idle 30 min / max 8 h; re-auth on expiry/restart (§6)
- [x] Cookies always use `HttpOnly` + `SameSite=Strict` + `Secure`; no sensitive data in cookies (§7)
- [x] Roles viewer/operator/admin enforced; authn and authz separated (§8)
- [x] All admin actions audit-logged; secrets never logged (§9)
- [x] Browser talks only to the backend API; all authz in the backend (§10)
- [x] Every response `no-store`; session-dependent pages vary on `Cookie` (§10)
- [x] Secrets never hardcoded/committed/logged/returned (§11)
- [x] Remote access only via VPN + reverse proxy + OIDC/SSO; role from the configured claim (§12)
- [x] Input validated everywhere; 401/403 empty bodies never disclose existence (§13)
- [x] Every request body bounded (4 KiB) by one middleware every route inherits: `413` before parsing over the bound, a multipart body is refused rather than spooled, an oversized `POST /viewer/login` mints no session (§13)
- [x] The viewer's server carries `ReadTimeout`, `WriteTimeout` and `IdleTimeout` beside `ReadHeaderTimeout`, so a completed header phase cannot hold a connection open (§13)
