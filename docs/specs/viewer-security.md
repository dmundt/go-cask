---
type: Specification
title: Viewer Security — go-cask
description: Security requirements for the embedded viewer — secure by default, authn/authz, session management, cookie requirements, and audit logging.
version: v11
---

# Viewer Security — go-cask

Security requirements for the embedded technical viewer. **Nothing may weaken this file** (AGENT.md §8 precedence); the viewer design and HTTP surface MUST comply. Related: `viewer-design.md` (the UI it protects), `api-design.md`.

## 1. Project context and intent

The viewer is an object-store inspection/integrity tool for **developers, operators, and troubleshooting** (list/inspect/verify). It is **not intended to be publicly accessible** and MUST always be secure-by-default.

## 2. Guiding priority

Security > Auditability > Simplicity > Convenience (§14). When in doubt, choose the more secure implementation. Requirements grouped into access, sessions, authorization, operations, deployment.

## 3. Secure by default (explicit enablement)

The viewer SHALL run only when explicitly invoked: `cask web` starts it; no other subcommand does, and there is no separate API server or `enabled` switch — invoking `cask web` IS the explicit enablement (backend-architecture §1). Everything else stays off by default (loopback §4, auth required §5).

## 4. Localhost only

- Default bind SHALL be `127.0.0.1` or `localhost`; the viewer MUST NOT be exposed on all interfaces by default.
- Network exposure requires an explicit config change (`bind`).
- If bind is a non-loopback address, the app MUST log a prominent startup warning and MUST refuse to start unless HTTPS is enabled or explicit `allow_insecure_bind: true` is set.

## 5. Authentication

- The viewer SHALL require authentication for all **protected** resources; unauthenticated access to them is not permitted. The only unauthenticated entry points are the login page (`/viewer/login`) and the `/viewer/` landing, which either redirects (303) to login or completes the same-origin `?token=` deep link (§5.1) — neither exposes store data.
- Login attempts MUST be rate limited (max 5 failures/caller-address/min) with exponential backoff; each failure MUST be audit-logged without the submitted token value. The address the throttle keys on is the **caller address** defined in §5.2, not necessarily the direct peer.
- **Preferred mechanism — startup-generated admin token:** grants the `admin` role. Additional viewer/operator principals are provisioned via the configured identity provider (OIDC) or configured per-role tokens. Sessions MUST carry exactly one role resolved at login.
- Startup token characteristics: cryptographically secure random; supplied out of band with `-token-file`/`CASK_VIEWER_TOKEN` or displayed once, and only when stderr is an interactive terminal — never through the logging package, at any level (cli.md §4, §9, §11); not stored in plaintext config; regenerated on every restart unless the operator supplied it.
- A startup or configured per-role token establishes a session two ways: via `POST /viewer/login` (preferred) or via a direct `GET /viewer/?token=<token>` — the `cask web` "open viewer" deep link. Every other endpoint MUST reject the token and require a valid session cookie. Both token-accepting endpoints admit a token only from the viewer's own origin (§5.1).
- **CSRF sourcing (MUST):** every viewer mutation is POST plus a per-session CSRF token compared in constant time. The token MUST be read from the request body (the form's hidden field) or the `X-CSRF-Token` header, never from the query string: a URL-borne token is captured by access logs, bookmarks, proxies, and `Referer` chains. A `?csrf=<token>` value MUST NOT validate.

### 5.1 Direct `?token=` deep link (acceptance)

The deep link is the `cask web` "open viewer" URL, `GET /viewer/?token=<token>`. `SameSite=Strict` (§7) governs whether the session cookie is *sent* on a later cross-site request; it does not govern the login *response*, which sets the cookie. A cross-site `<img>`, `<link>`, or navigation carrying a valid token would therefore pin the victim's browser into the presenter's session. Both endpoints that accept a token — `POST /viewer/login` and the token-bearing `GET /viewer/` — MUST first establish that the request comes from the viewer's own origin, and MUST answer `403` with an empty body (§13) plus an audit line (§9) otherwise:

- Same-origin means `Sec-Fetch-Site: same-origin` (a form post, link, or htmx request issued by a viewer page) or `Sec-Fetch-Site: none` (a top-level navigation with no initiator: the address bar, a bookmark, or the browser `cask web` opens). Only a browser sets this header, and a page cannot forge it (it is a forbidden header name).
- `same-site` and `cross-site` are not the viewer's origin and MUST be refused even when the presented token is valid.
- When a browser sends no `Sec-Fetch-Site`, an `Origin` header naming the request's own host is the fallback; two missing headers MUST fail closed.
- A refused request MUST NOT create a session, MUST NOT set a session cookie, and MUST be refused before the login throttle is consulted, so a cross-site flood cannot spend a real caller's login budget.
- The token appears only in that one login URL — it MUST NOT be echoed into the session, cookies, or logs; the server MUST send `Referrer-Policy: no-referrer` on the response so the token does not leak via `Referer`; login still honors the throttle and audit-logs the action **without** the token value; after the session cookie is set the client MUST NOT reuse the token URL (a stale token URL is just a login attempt, not a session).

The URL contract is unchanged for the operator: the opened token URL and a same-origin link or form both still sign in. No session is ever minted from a request the viewer cannot attribute to its own origin.

### 5.2 Caller address (proxy deployments)

The login throttle is per caller address. That address is the direct TCP peer unless the peer is a **configured trusted proxy** and the request carries a client address forwarded on that client's behalf (`internal/web/proxy.go`).

- The default configuration trusts **no** proxy, so the throttle keys on `RemoteAddr` alone. A forwarded header is then ignored in full, and the viewer MUST behave exactly as it did before this rule existed.
- `cask web -trusted-proxy <ip|cidr,...>` — and the `TrustedProxies` viewer config field behind it — names the peers whose forwarded address may be believed: CIDR blocks (`10.0.0.0/8`) or single IPs (`127.0.0.1`, `::1`, or an `ip:port` whose port is ignored). An entry that is none of these MUST fail viewer startup; silently trusting nothing would restore the shared-bucket lockout below.
- Only when the direct peer matches that list may the forwarded address be used, taken from `X-Forwarded-For`, or from `Forwarded` (RFC 7239, its first `for=`) when `X-Forwarded-For` is absent. The value chosen is the **rightmost address in the chain that is not itself a trusted proxy**; when every hop is trusted, the leftmost entry is the caller. Text to the left of that hop is discarded, so a client cannot select its own throttle bucket by writing the header.
- A hop that is not a bare IP is taken verbatim, not normalized: an unusual bucket over-throttles only the value that was presented, whereas normalizing text into an IP could hand a caller another client's bucket.
- With a forwarded address present and the peer trusted, the throttle MUST key on that address; with no forwarded address, on the peer address — so the two cases cannot share a bucket. The session and the audit log record the same caller address the throttle used.
- Trusting a peer is a delegation of the caller identity: the proxy MUST overwrite the forwarded header rather than append to a client-supplied one, and MUST NOT be a peer that arbitrary clients can reach directly. Trusting a CIDR broader than the actual proxy network weakens the throttle exactly as much as trusting the header unconditionally.

If `cask web` has no trusted proxy configured and sits behind a reverse proxy, **every client shares the peer's bucket**: five failed logins from anyone block all operators for up to 30 minutes. The remedies are to configure `-trusted-proxy` for that proxy, to rate-limit at the proxy as well, or to bind the viewer directly (§4, §12).

## 6. Session management

After successful auth: create a secure session, issue a session cookie, do not require re-entering the startup token per request.

- Idle timeout 30 min; maximum lifetime 8 h.
- Re-authenticate when idle timeout expires, max lifetime is reached, or the app restarts.

## 7. Cookie requirements

Session cookies MUST always use `HttpOnly`, `SameSite=Strict`, and `Secure`.
Callers MUST NOT be able to disable these attributes. Sensitive data must never
be stored in browser-accessible cookies.

## 8. Authorization (roles)

Authentication and authorization MUST be separated. Roles: `viewer`, `operator`, `admin`.

The viewer inspects; it does not mutate the store (see viewer-design §5 and
§11 below). The ladder therefore gates what the viewer actually offers, and
nothing else — an unimplemented permission in this table would read as a
capability the viewer must ship:

- **viewer:** list and browse objects, inspect metadata and references, read an
  object's bytes. Not: any operation that writes to or removes from the store.
- **operator:** viewer permissions + verify an object, and verify every object
  in one sweep. Verification reads and re-digests; it never mutates.
- **admin:** operator permissions. The viewer exposes no destructive operation,
  so `admin` currently reaches nothing `operator` does not. The rank stays
  because the ladder defines it, not because the viewer needs it today.

Store-lifecycle operations — writing an object, deleting one, garbage
collection — belong to the CLI, where they can be scripted, audited, and paired
with the roots a sweep needs. A future viewer that gains one MUST extend this
table in the same commit that ships it.

## 9. Audit logging

All administrative actions MUST be logged, including timestamp, user/session identifier, action, affected resource, result. Never log passwords, session cookies, authentication tokens, or secret keys. The process log is one of those sinks: `cask web` MUST NOT hand the startup token to the logging package at any level, because under systemd/journald, Docker, or a log shipper that output is retained and indexed beyond the operator. The token is written only to an interactive terminal, or not at all (§5.1, §11).

## 10. API architecture

The viewer MUST communicate only with the backend API; never allow direct browser access to storage internals. All authorization checks MUST occur in the backend (browser → viewer routes → object store).

**Response hardening (MUST):** every viewer response MUST carry
`X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, and a Content
Security Policy that denies by default and allows only the viewer's own origin:
`default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'`.
The viewer serves its stylesheet and its single script from its own origin, so
no third-party source needs to be allowed. `connect-src 'self'` is required, not
optional: every htmx swap is an XHR to a viewer route, and omitting it blocks
the whole interaction model. `style-src` admits inline styles because htmx
injects a style element for its indicator class; `script-src` stays strict,
which is the directive that governs injection.

**No viewer response is cacheable (MUST):** every response MUST carry
`Cache-Control: no-store`, and the pages whose body depends on the session MUST
name `Cookie` in `Vary`. The viewer renders digests, object bytes, and the
session's verification state, and §12's remote shape reaches it through a
TLS-terminating proxy; without these headers a shared cache in that path could
retain a response, or answer a later caller with a page rendered for someone
else's session.

## 11. Secret handling

Secrets must never be hardcoded, committed to source control, written to logs, or returned in API responses (access/secret keys, session/startup tokens, encryption keys). Use environment variables or dedicated secret providers. The only place a token MAY appear in a URL is the documented `GET /viewer/?token=` login deep link (§5.1) — that URL is one-time, is never logged, and its response carries `Referrer-Policy: no-referrer`. A startup token MAY additionally be supplied out of band, with the `-token-file` flag or the `CASK_VIEWER_TOKEN` environment variable, and MAY be displayed once on an interactive terminal (cli.md §4); outside that it MUST NOT appear in the process log at any level, in the output of a process without a terminal, or in an API response.

## 12. Production deployments

If remote access is required, the preferred architecture is **VPN + reverse proxy + OIDC/SSO + viewer** (e.g. Microsoft Entra ID, Keycloak, Authentik, OAuth2 Proxy). Do not expose the viewer directly to the public internet. Behind an OIDC/SSO proxy the backend MUST derive the role from a configurable claim (default `groups`), mapping configured group names to viewer/operator/admin, and MUST deny access when no mapping matches. The same-origin rule (§5.1) reads the browser's `Sec-Fetch-Site` first and falls back to comparing the `Origin` host with the request `Host`; a proxy MUST therefore preserve the `Host` header the browser used. The scheme may differ (a TLS-terminating proxy answers https while forwarding http), which the host-only comparison tolerates.

A reverse proxy is also the deployment the login throttle must be told about (§5.2). Because the proxy is the direct peer for every client:

- Configure `cask web -trusted-proxy` with that proxy's address, so the throttle keys on the forwarded client address and one attacker cannot exhaust the shared bucket. An unconfigured viewer behind a proxy locks out every operator the same way (§5.2).
- Trust only the proxy's own addresses, never a broad range. A trusted-proxy entry is a delegation of caller identity: the proxy MUST overwrite `X-Forwarded-For` (not append to a client-supplied value) and MUST NOT be reachable directly by untrusted clients.
- Rate-limiting at the proxy remains worthwhile as defence in depth, but it is not a substitute: the viewer's own throttle is the only limit that survives a proxy already being passed.
- If neither is possible, bind the viewer directly (§4) instead of leaving it behind a proxy whose clients share one bucket.

## 13. Defensive programming

- Always validate query parameters, headers, JSON payloads, and object/bucket names — do not trust client input. Fail securely, return minimal error information. Return 401 (empty body) for missing/expired sessions and 403 (empty body) for insufficient role on data endpoints or for a token-bearing login that is not same-origin (§5.1); never disclose whether the target bucket/object exists. The viewer landing (`GET /viewer/`) alone redirects (303) to `/viewer/login` when no session is present, so a browser can reach the login page; it also completes the same-origin direct `?token=` login (§5.1).

## 14. Security principle

The viewer is an administrative tool. Priority: 1 Security, 2 Auditability, 3 Simplicity, 4 Convenience. When in doubt, choose the more secure implementation.

## 15. Compliance checklist

- [x] Runs only via explicit `cask web`; loopback default; non-loopback requires HTTPS or `allow_insecure_bind: true` (§3–§4)
- [x] Auth required; login throttled (5/caller-address/min, backoff, audit-logged without the token) (§5)
- [x] A forwarded client address is believed only from a configured trusted proxy; with none configured the peer address keys the throttle and the header is ignored (§5.2)
- [x] Startup token accepted only by `POST /login` **or** the direct `GET /viewer/?token=` deep link (§5); regenerated per start; never stored in plaintext (§5); never logged at any level — shown once on an interactive terminal only, or supplied out of band via `-token-file`/`CASK_VIEWER_TOKEN` (§9, §11); both token-accepting endpoints admit a token only from the viewer's own origin (§5.1)
- [x] Both token-accepting endpoints refuse a request that is not same-origin (403, empty body, audit line); no cross-site request mints a session (§5.1)
- [x] CSRF token accepted from the POST body or the `X-CSRF-Token` header only; `?_csrf=` never validates (§5)
- [x] Sessions: idle 30 min / max 8 h; re-auth on expiry/restart (§6)
- [x] Cookies always use `HttpOnly` + `SameSite=Strict` + `Secure`; no sensitive data in cookies (§7)
- [x] Roles viewer/operator/admin enforced; authn and authz separated (§8)
- [x] All admin actions audit-logged; secrets never logged (§9)
- [x] Browser talks only to the backend API; all authz in the backend (§10)
- [x] Every response `no-store` and session-dependent pages vary on `Cookie` (§10)
- [x] Secrets never hardcoded/committed/logged/returned (§11)
- [x] Remote access only via VPN + reverse proxy + OIDC/SSO; role derived from the configured claim (§12)
- [x] Input validated everywhere; 401/403 empty bodies never disclose existence (§13)
