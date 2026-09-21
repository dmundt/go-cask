---
type: Specification
title: Viewer Security — go-cask
description: Security requirements for the embedded viewer — secure by default, authn/authz, session management, cookie requirements, and audit logging.
version: v8
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

- The viewer SHALL require authentication for all **protected** resources; unauthenticated access to them is not permitted. The only unauthenticated entry points are the login page (`/viewer/login`) and the `/viewer/` landing, which either redirects (303) to login or performs a direct `?token=` login (§5.1) — neither exposes store data.
- Login attempts MUST be rate limited (max 5 failures/IP/min) with exponential backoff; each failure MUST be audit-logged without the submitted token value.
- **Preferred mechanism — startup-generated admin token:** grants the `admin` role. Additional viewer/operator principals are provisioned via the configured identity provider (OIDC) or configured per-role tokens. Sessions MUST carry exactly one role resolved at login.
- Startup token characteristics: cryptographically secure random; displayed only at startup (once); not stored in plaintext config; regenerated on every restart.
- A startup or configured per-role token establishes a session two ways: via `POST /viewer/login` (preferred) or via a direct `GET /viewer/?token=<token>` — the `cask web` "open viewer" deep link. Every other endpoint MUST reject the token and require a valid session cookie.
- **Direct `?token=` login (MUST):** the token appears only in that one login URL — it MUST NOT be echoed into the session, cookies, or logs; the server MUST send `Referrer-Policy: no-referrer` on the response so the token does not leak via `Referer`; login still honors the throttle and audit-logs the action **without** the token value; after the session cookie is set the client MUST NOT reuse the token URL (a stale token URL is just a login attempt, not a session).

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

All administrative actions MUST be logged, including timestamp, user/session identifier, action, affected resource, result. Never log passwords, session cookies, authentication tokens, or secret keys.

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

## 11. Secret handling

Secrets must never be hardcoded, committed to source control, written to logs, or returned in API responses (access/secret keys, session/startup tokens, encryption keys). Use environment variables or dedicated secret providers. The only place a token MAY appear in a URL is the documented `GET /viewer/?token=` login deep link (§5.1) — that URL is one-time, is never logged, and its response carries `Referrer-Policy: no-referrer`.

## 12. Production deployments

If remote access is required, the preferred architecture is **VPN + reverse proxy + OIDC/SSO + viewer** (e.g. Microsoft Entra ID, Keycloak, Authentik, OAuth2 Proxy). Do not expose the viewer directly to the public internet. Behind an OIDC/SSO proxy the backend MUST derive the role from a configurable claim (default `groups`), mapping configured group names to viewer/operator/admin, and MUST deny access when no mapping matches.

## 13. Defensive programming

- Always validate query parameters, headers, JSON payloads, and object/bucket names — do not trust client input. Fail securely, return minimal error information. Return 401 (empty body) for missing/expired sessions and 403 (empty body) for insufficient role on data endpoints; never disclose whether the target bucket/object exists. The viewer landing (`GET /viewer/`) alone redirects (303) to `/viewer/login` when no session is present, so a browser can reach the login page; it also accepts the direct `?token=` login (§5).

## 14. Security principle

The viewer is an administrative tool. Priority: 1 Security, 2 Auditability, 3 Simplicity, 4 Convenience. When in doubt, choose the more secure implementation.

## 15. Compliance checklist

- [x] Runs only via explicit `cask web`; loopback default; non-loopback requires HTTPS or `allow_insecure_bind: true` (§3–§4)
- [x] Auth required; login throttled (5/IP/min, backoff, audit-logged without the token) (§5)
- [x] Startup token accepted only by `POST /login` **or** the direct `GET /viewer/?token=` deep link (§5); regenerated per start; never stored in plaintext (§5)
- [x] Sessions: idle 30 min / max 8 h; re-auth on expiry/restart (§6)
- [x] Cookies always use `HttpOnly` + `SameSite=Strict` + `Secure`; no sensitive data in cookies (§7)
- [x] Roles viewer/operator/admin enforced; authn and authz separated (§8)
- [x] All admin actions audit-logged; secrets never logged (§9)
- [x] Browser talks only to the backend API; all authz in the backend (§10)
- [x] Secrets never hardcoded/committed/logged/returned (§11)
- [x] Remote access only via VPN + reverse proxy + OIDC/SSO; role derived from the configured claim (§12)
- [x] Input validated everywhere; 401/403 empty bodies never disclose existence (§13)
