---
type: Specification
title: Viewer Security — go-cask
description: Security requirements for the embedded viewer — secure by default, authn/authz, session management, cookie requirements, and audit logging.
version: v15
---

# Viewer Security — go-cask

Security requirements for the embedded technical viewer. **Nothing may weaken
this file** (AGENT.md §8 precedence); the viewer design and HTTP surface MUST
comply. Related: `viewer-design.md` (the UI it protects), `api-design.md`.

## 1. Project context and intent

The viewer is an object-store inspection/integrity tool for **developers,
operators, and troubleshooting** (list/inspect/verify). It is **not intended to
be publicly accessible** and MUST always be secure-by-default.

## 2. Guiding priority

Security > Auditability > Simplicity > Convenience (§14). When in doubt,
choose the more secure implementation. Requirements group into access,
sessions, authorization, operations, deployment.

## 3. Secure by default (explicit enablement)

The viewer SHALL run only when explicitly invoked: `cask web` starts it, no
other subcommand does, and no separate API server or `enabled` switch exists —
invoking `cask web` IS the explicit enablement (backend-architecture §1).
Everything else stays off by default (loopback §4, auth required §5).

## 4. Localhost only

- Default bind SHALL be `127.0.0.1` or `localhost`; the viewer MUST NOT be exposed on all interfaces by default.
- Network exposure requires an explicit config change (`bind`).
- If bind is a non-loopback address, the app MUST log a prominent startup warning, and MUST refuse to start unless HTTPS is enabled or explicit `allow_insecure_bind: true` is set.
- A non-loopback bind MUST NOT get a printed login link: the session cookie is always `Secure` (§7), so the only login that can hold is over `https://` through a TLS-terminating proxy. The notice MUST name the bind and that `https://` expectation instead (cli.md §2).

## 5. Authentication

- The viewer SHALL require authentication for all **protected** resources;
  unauthenticated access is not permitted. Its only unauthenticated entry points
  are the login page (`/viewer/login`) and the `/viewer/` landing, which either
  redirects (303) to login or completes the same-origin `?token=` deep link
  (§5.1) — neither exposes store data.
- Login attempts MUST be rate limited (max 5 failures/caller-address/min) with
  exponential backoff; each failure MUST be audit-logged without the submitted
  token value. The throttle keys on the **caller address** of §5.2, not
  necessarily the direct peer.
- Authentication alone MUST NOT let one session monopolize the server: the
  routes whose work is proportional to the store rather than to the request
  (verify-all, a metadata-snapshot rebuild) are bounded per session and
  server-wide, answer `429` + `Retry-After` when the bound is exceeded, and
  never queue work behind a running operation (viewer-design §3, defaults.md).
  The refusal is audit-logged like a throttled login.
- **Preferred mechanism — startup-generated admin token:** grants the `admin`
  role. Further viewer/operator principals come from the configured identity
  provider (OIDC) or per-role tokens. Sessions MUST carry exactly one role
  resolved at login.
- Startup token: cryptographically secure random; supplied out of band with
  `-token-file`/`CASK_VIEWER_TOKEN`, or displayed once on **standard output** —
  an interactive terminal, or any run whose operator explicitly requests the
  display with `cask web -show-token` — and only for a loopback bind, whose
  printed link can hold a session (§5.1, §7, §11);
  never through the logging package at any level (cli.md §4, §9, §11); not
  stored in plaintext config; regenerated each restart unless the operator
  supplied it.
- A startup or per-role token establishes a session two ways:
  `POST /viewer/login` (preferred) or a direct `GET /viewer/?token=<token>` — the
  `cask web` "open viewer" deep link. Every other endpoint MUST reject the token
  and require a valid session cookie. Both token-accepting endpoints admit a
  token only from the viewer's own origin (§5.1).
- **CSRF sourcing (MUST):** every viewer mutation is POST plus a per-session
  CSRF token compared in constant time. The token MUST be read from the request
  body (the form's hidden field) or the `X-CSRF-Token` header, never the query
  string — a URL-borne token is captured by access logs, bookmarks, proxies, and
  `Referer` chains. A `?csrf=<token>` value MUST NOT validate.

### 5.1 Direct `?token=` deep link (acceptance)

The deep link is the `cask web` "open viewer" URL, `GET /viewer/?token=<token>`.
`SameSite=Strict` (§7) governs whether the session cookie is *sent* on a later
cross-site request, not the login *response* that sets it. A cross-site `<img>`,
`<link>`, or navigation carrying a valid token would pin the victim's browser
into the presenter's session. Both token-accepting endpoints —
`POST /viewer/login` and the token-bearing `GET /viewer/` — MUST first establish
that the request comes from the viewer's own origin, and MUST otherwise answer
`403` with an empty body (§13) plus an audit line (§9):

- Same-origin means `Sec-Fetch-Site: same-origin` (a form post, link, or htmx
  request issued by a viewer page) or `Sec-Fetch-Site: none` (a top-level
  navigation with no initiator: the address bar, a bookmark, or the browser
  `cask web` opens). Only a browser sets it, and no page can forge it — a
  forbidden header name.
- `same-site` and `cross-site` are not the viewer's origin and MUST be refused
  even with a valid token.
- With no `Sec-Fetch-Site`, an `Origin` header naming the request's own host is
  the fallback; two missing headers MUST fail closed.
- A refused request MUST NOT create a session, MUST NOT set a session cookie,
  and MUST be refused before the login throttle is consulted, so a cross-site
  flood cannot spend a real caller's login budget.
- The token appears only in that one login URL — it MUST NOT be echoed into the
  session, cookies, or logs; the server MUST send `Referrer-Policy:
  no-referrer` so it does not leak via `Referer`; login still honors the
  throttle and audit-logs the action **without** the token value; after the
  cookie is set the client MUST NOT reuse the token URL (a stale one is just a
  login attempt, not a session).

The URL contract is unchanged for the operator: the opened token URL and a
same-origin link or form both still sign in, and no session is ever minted from
a request the viewer cannot attribute to its own origin. What the startup notice
prints is separate from what the endpoint accepts: a loopback bind prints
`http://<addr>/viewer/?token=…`; a non-loopback bind MUST NOT print a link at
all, its plain `http://` origin being unable to hold an always-`Secure` session
cookie (§7) — the notice names the bind and the `https://` expectation instead.

### 5.2 Caller address (proxy deployments)

The login throttle is per caller address: the direct TCP peer, unless the peer
is a **configured trusted proxy** and the request carries a client address
forwarded on that client's behalf (`internal/web/proxy.go`).

- Default: **no** proxy trusted — the throttle keys on `RemoteAddr` alone, a
  forwarded header is ignored in full, and the viewer MUST behave exactly as
  before this rule existed.
- `cask web -trusted-proxy <ip|cidr,...>` — and the `TrustedProxies` viewer
  config field behind it — names the peers whose forwarded address may be
  believed: CIDR blocks (`10.0.0.0/8`) or single IPs (`127.0.0.1`, `::1`, or an
  `ip:port` with the port ignored). Any other entry MUST fail viewer startup;
  silently trusting nothing would restore the shared-bucket lockout below.
- Only a matching direct peer permits the forwarded address: from
  `X-Forwarded-For`, or `Forwarded` (RFC 7239, its first `for=`) when that
  header is absent. The value is the **rightmost address in the chain that is
  not itself a trusted proxy**; when every hop is trusted, the leftmost entry is
  the caller. Text left of that hop is discarded, so a client cannot pick its
  own throttle bucket by writing the header.
- A hop that is not a bare IP is taken verbatim, not normalized: an unusual
  bucket over-throttles only the presented value, whereas normalizing text into
  an IP could hand a caller another client's bucket.
- With a forwarded address present and the peer trusted, the throttle MUST key
  on that address; with none, on the peer address — the two cases cannot share a
  bucket. Session and audit log record the same caller address.
- Trusting a peer delegates the caller identity: the proxy MUST overwrite the
  forwarded header rather than append to a client-supplied one, and MUST NOT be
  directly reachable by arbitrary clients. A CIDR broader than the actual proxy
  network weakens the throttle exactly as much as trusting the header
  unconditionally.

If `cask web` has no trusted proxy and sits behind a reverse proxy, **every
client shares the peer's bucket**: five failed logins from anyone block all
operators for up to 30 minutes. Remedies: configure `-trusted-proxy` for that
proxy, rate-limit there too, or bind the viewer directly (§4, §12).

## 6. Session management

After successful auth: create a secure session, issue a session cookie, and do
not require re-entering the startup token per request.

- Idle timeout 30 min; maximum lifetime 8 h.
- Re-authenticate when idle timeout expires, max lifetime is reached, or the app restarts.

## 7. Cookie requirements

Session cookies MUST always use `HttpOnly`, `SameSite=Strict`, and `Secure`.
Callers MUST NOT be able to disable these attributes. Sensitive data must never
be stored in browser-accessible cookies.

## 8. Authorization (roles)

Authentication and authorization MUST be separated. Roles: `viewer`, `operator`,
`admin`.

The viewer inspects; it does not mutate the store (viewer-design §5 and §11
below). The ladder gates what the viewer actually offers, nothing else — an
unimplemented permission here would read as a capability the viewer must ship:

- **viewer:** list and browse objects, inspect metadata and references, read an
  object's bytes. Not: any operation that writes to or removes from the store.
- **operator:** viewer permissions + verify an object, and verify every object
  in one sweep. Verification reads and re-digests; it never mutates.
- **admin:** operator permissions. The viewer exposes no destructive operation,
  so `admin` currently reaches nothing `operator` does not. The rank stays
  because the ladder defines it, not because the viewer needs it today.

Store-lifecycle operations — writing an object, deleting one, garbage
collection — belong to the CLI, where they can be scripted, audited, and paired
with the roots a sweep needs. A future viewer gaining one MUST extend this table
in the same commit that ships it.

## 9. Audit logging

All administrative actions MUST be logged, including timestamp, user/session
identifier, action, affected resource, result. Never log passwords, session
cookies, authentication tokens, or secret keys. That includes the process log:
`cask web` MUST NOT hand the startup token to the logging package at any level —
under systemd/journald, Docker, or a log shipper that output is retained and
indexed beyond the operator. The token goes only to the one-time login notice on
standard output, or nowhere (§5, §5.1, §11).

## 10. API architecture

The viewer MUST communicate only with the backend API; never allow direct
browser access to storage internals. All authorization checks MUST occur in the
backend (browser → viewer routes → object store).

**Response hardening (MUST):** every viewer response MUST carry
`X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, and a Content
Security Policy that denies by default and allows only the viewer's own origin:
`default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'`.
Both of its assets come from that origin, so no third-party source needs
allowing. `connect-src 'self'` is required, not optional: every htmx swap is an
XHR to a viewer route, and omitting it blocks the whole interaction model.
`style-src` admits inline styles because htmx injects a style element for its
indicator class; `script-src` stays strict, the directive that governs
injection.

**No viewer response is cacheable (MUST):** every response MUST carry
`Cache-Control: no-store`, and the pages whose body depends on the session MUST
name `Cookie` in `Vary`. The viewer renders digests, object bytes, and the
session's verification state, and §12's remote shape reaches it through a
TLS-terminating proxy; without these headers a shared cache there could retain a
response, or answer a later caller with a page rendered for someone else's
session.

## 11. Secret handling

Secrets must never be hardcoded, committed to source control, written to logs,
or returned in API responses (access/secret keys, session/startup tokens,
encryption keys). Use environment variables or dedicated secret providers. The
one place a token MAY appear in a URL is the documented `GET /viewer/?token=`
login deep link (§5.1) — one-time, never logged, its response carrying
`Referrer-Policy: no-referrer`. A startup token MAY also be supplied out of band
with `-token-file` or `CASK_VIEWER_TOKEN`, and MAY be displayed once on standard
output (§5) — only for a loopback bind, whose printed link can hold a session
(§5.1, §7). Outside that it MUST NOT appear in
the process log at any level, in the output of a process not asked to display
it, or in an API response. The browser launch follows the same two conditions as
the display, because it hands the login deep link to another process: `cask web`
opens the browser only when the bind is loopback and the operator has not
suppressed the display with `-show-token=false`, so the raw token cannot reach
an argument vector that any process listing can read.

## 12. Production deployments

Remote access, if required, is best served by **VPN + reverse proxy + OIDC/SSO +
viewer** (e.g. Microsoft Entra ID, Keycloak, Authentik, OAuth2 Proxy) — never
the viewer exposed directly to the public internet. Behind an OIDC/SSO proxy the
backend MUST derive the role from a configurable claim (default `groups`),
mapping configured group names to viewer/operator/admin, and MUST deny access
when no mapping matches. The same-origin rule (§5.1) reads `Sec-Fetch-Site`
first, falling back to the `Origin` host against the request `Host`; a proxy
MUST therefore preserve the browser's `Host` header. The scheme may differ (a
TLS-terminating proxy answers https while forwarding http), which the host-only
comparison tolerates.

A reverse proxy is also the deployment the login throttle must know about
(§5.2): the proxy is the direct peer for every client, so:

- Configure `cask web -trusted-proxy` with that proxy's address, so the throttle keys on the forwarded client address and one attacker cannot exhaust the shared bucket; an unconfigured viewer behind a proxy locks out every operator the same way (§5.2).
- Trust only the proxy's own addresses, never a broad range. A trusted-proxy entry delegates caller identity: the proxy MUST overwrite `X-Forwarded-For` (not append to a client-supplied value) and MUST NOT be reachable directly by untrusted clients.
- Proxy rate-limiting remains worthwhile as defence in depth but is no substitute: the viewer's own throttle is the only limit that survives a proxy already being passed.
- If neither is possible, bind the viewer directly (§4) instead of leaving it behind a proxy whose clients share one bucket.

## 13. Defensive programming

- Validate query parameters, headers, JSON payloads, and object/bucket names —
  never trust client input. Fail securely, returning minimal error information:
  401 (empty body) for missing/expired sessions, 403 (empty body) for
  insufficient role on data endpoints or a not-same-origin token-bearing login
  (§5.1); never disclose whether the target bucket/object exists. The viewer
  landing (`GET /viewer/`) alone redirects (303) to `/viewer/login` with no
  session, so a browser can reach the login page, and completes the same-origin
  direct `?token=` login (§5.1).

## 14. Security principle

The viewer is an administrative tool. Priority: 1 Security, 2 Auditability,
3 Simplicity, 4 Convenience. When in doubt, choose the more secure
implementation.

## 15. Compliance checklist

- [x] Runs only via explicit `cask web`; loopback default; non-loopback requires HTTPS or `allow_insecure_bind: true` (§3–§4)
- [x] Auth required; login throttled (5/caller-address/min, backoff, audit-logged without the token) (§5)
- [x] A forwarded client address is believed only from a configured trusted proxy; with none, the peer address keys the throttle and the header is ignored (§5.2)
- [x] Startup token accepted only by `POST /login` **or** the direct `GET /viewer/?token=` deep link (§5); regenerated per start; never stored in plaintext (§5); never logged at any level — shown once on stdout for a loopback bind only (interactive terminal, or a run passing `-show-token`), or supplied out of band via `-token-file`/`CASK_VIEWER_TOKEN` (§9, §11); both token-accepting endpoints admit a token only from the viewer's own origin (§5.1)
- [x] No login link is printed for a non-loopback bind; the notice names the bind and the `https://` expectation instead (§4, §5.1)
- [x] No token is displayed for a non-loopback bind, whatever the display choice, and the browser launch is skipped when the bind is not loopback or the display is suppressed, so the token cannot reach another process's argument vector (§11)
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
