# cask — the CLI and the viewer

`cmd/cask` builds the module's only binary. `cask` is a thin CLI over the `cas`
library for store operations, and `cask web` starts the embedded viewer — the
product's only HTTP surface. Every command maps to a core operation or to the
viewer server composition; the CLI is a wrapper, never a second implementation.

The subcommand, flag, output, and exit-code contract is
[`docs/specs/cli.md`](../../docs/specs/cli.md). This README is the operator's
path into the viewer: how to start it, how to get in, what it shows, and what
each state means. The normative contracts stay with the specs linked at the end.

## Store operations

```text
cask -store ./objects put <file>          # store bytes; prints sha256:<hexdigest>
cask -store ./objects get <hash> -o out   # retrieve to a file or stdout
cask -store ./objects list -limit 20      # list objects
cask -store ./objects meta <hash>         # size, envelope type, algorithm
cask -store ./objects stats               # N objects, M bytes
cask -store ./objects verify <hash>       # recompute the address
cask -store ./objects gc --min-age 1h <roots...>
cask -store ./objects prune --min-age 1h <roots...>
cask -store ./objects clean
cask -store ./objects seed-preview -count 500
```

- `-store` names the store's own directory. Both backends reject an empty
  value, `.`, a filesystem or volume root, and a parent-traversal path before
  creating anything.
- `-backend fs` (the default) or `-backend packfs` selects the storage backend.
  Every store operation runs over either; the viewer does not.
- The CLI digests with `cas/hash/sha256`, and no store operation takes an
  algorithm flag — the core names no algorithm.
- `gc`, `prune`, and `clean` are the destructive commands. `gc` and `prune` are
  grace-gated: they reclaim only objects absent from `<roots...>` **and** older
  than `--min-age` (default `1h`). `<roots...>` must already be the complete
  reachable set, not just entry points, because the byte layer cannot expand a
  root into what it references.
- Maintenance sweeps take the store's exclusive cross-process lock
  (`.cask.lock` at the store root); writers and reads never lock.

## The viewer (`cask web`)

`cask web` starts a server-rendered object browser that answers "what is stored
here, how large is it, and are these still the bytes their addresses claim?" It
binds to loopback, requires a login, and serves HTML only — vendored htmx, one
scoped stylesheet, no JSON API, and no custom JavaScript.

It is a **byte-layer** tool. It shows objects, envelope types, exact sizes,
bytes, and on-demand integrity results. It never resolves typed references and
never imports an application object model.

```text
cask web -store ./objects -backend fs -bind 127.0.0.1:8080 \
  -hash-algo sha256 -tokens viewer=…,operator=…,admin=…
```

Invoking `cask web` is the explicit enablement: no other subcommand starts the
viewer, and there is no `enabled` switch. Configuration is flags only — there is
no config file.

### Flags

| Flag | Default | Effect |
|---|---|---|
| `-store` | `./objects` | the store directory to inspect |
| `-backend` | `fs` | must be `fs`: the viewer reads per-object physical metadata, so `packfs` is refused with an error naming the operation (exit 1) |
| `-bind` | `127.0.0.1:8080` | listen address; a non-loopback address needs `-allow-insecure-bind` |
| `-hash-algo` | `sha256` | `sha256`, `sha512`, or `sha512_256`; used to parse and validate digests and to verify, and shown in Metadata → Identity → Algorithm |
| `-tokens` | empty | comma-separated `role=token` pairs for non-admin logins, for example `viewer=…,operator=…` |
| `-token-file` | empty | file holding the startup admin token; used instead of generating one, and never displayed |
| `-trusted-proxy` | empty | IPs, `ip:port` values, or CIDR blocks whose forwarded client address the login throttle may believe; empty trusts none, and a malformed entry fails startup |
| `-allow-insecure-bind` | `false` | allow a non-loopback bind; startup then logs a prominent warning |
| `-show-token` | terminal heuristic | `-show-token` forces the one-time login hint, `-show-token=false` never shows it and never opens the browser, and an absent flag shows it only on an interactive stdout; a loopback bind is required in every case |
| `-no-open` | `false` | do not open the default browser |

Exit codes match every other subcommand: `0` success, `1` runtime error, `2`
usage error.

### Getting in

The startup token grants `admin` and is resolved in this order:

1. `-token-file <path>`;
2. the `CASK_VIEWER_TOKEN` environment variable;
3. a per-run `crypto/rand` token, regenerated on every restart.

A token the operator supplied is never displayed. A generated one is shown once
on **stdout** — on an interactive stdout, or when `-show-token` asks for it, and
only for a loopback bind — and never through the logger at any level; when it is
not shown, the log names the remedy and the reason, not the token. A bind that is
not loopback displays no token at all: the notice names the bind and the
`https://` expectation instead.

Unless `-no-open` is given, `cask web` opens the default browser at the one-time
deep link `http://<bind>/viewer/?token=<token>` and prints the same link in the
startup notice. The link carries the raw token in the browser process's command
line, so the launch is skipped for a non-loopback bind and when
`-show-token=false` suppresses the display. The token is accepted only from the
viewer's own origin — a
same-origin form post, link, or htmx request, or a top-level navigation with no
initiator — and only once: the session cookie carries the session afterwards. A
non-loopback bind prints no link at all, because an always-`Secure` cookie
cannot be set over plain `http://`.

### Sessions and roles

- A session lasts at most 30 idle minutes and 8 hours, and disappears on
  restart. Cookies are always `HttpOnly`, `SameSite=Strict`, and `Secure`.
- Three roles: `viewer` lists, browses, and inspects; `operator` adds
  verification of one object and of the whole store; `admin` adds nothing
  further today. A session carries exactly one role.
- Failed logins are throttled at five per caller address per minute with
  exponential backoff, and audited without the token. Behind a reverse proxy,
  set `-trusted-proxy` to that proxy's address: without it every client shares
  one bucket, and five failures from anyone lock out all operators.
- The viewer inspects; it does not destroy. There is no delete, GC, or prune
  route — and no route outside the table below.
- `cask web` does not take the store's maintenance lock, so a `gc` or `prune`
  sweep may run while the viewer is live; the sweep's `--min-age` grace protects
  fresh objects.

### Routes

| Route | What it does |
|---|---|
| `/viewer/` | entry point: completes the `?token=` deep link, redirects to login without a session, otherwise the object browser |
| `/viewer/objects` | the object browser; every filter, sort, page, and selection addresses it |
| `/viewer/objects/{hash}` | cold link: redirects (303) to the browser with that object selected |
| `/viewer/objects/{hash}/dump` | lazy hexdump fragment (HTML, not the stored bytes) |
| `POST /viewer/objects/{hash}/verify` | verify one object (`operator`) |
| `POST /viewer/objects/verify` | verify every stored object (`operator`) |
| `/viewer/login` | login page and token submission |
| `/viewer/static/{viewer.css,htmx.min.js}` | the viewer's only two assets, served from its own origin |

Every other path under `/viewer/` is answered by one catch-all that names no
method: `401` without a session, `404` with one. The surface cannot be mapped by
probing it.

### What you see

A master–detail workspace: a top bar with the module version and a Verify-all
control, a filter bar, one row per stored object, and an inspector that follows
the selection.

- **Filter bar** — search text, object type, size band, integrity state, and,
  when the host supplies the indexes, the reference filter.
- **Table columns** — `Hash`, `Type`, `Size`, `Inbound`, `Integrity`,
  `References`, `Written`. Every column sorts.
- **Inspector panels** — `Metadata` (full digest, algorithm, envelope type,
  exact size, physical written time, the state section, and the role-gated
  verify form), `References` (digest-sorted inbound and outbound edges), and
  `Bytes` (a lazy 16-byte-row hexdump of at most the first 256 bytes, with a
  truncation note).

Filter, sort, page, selection, and panel live in the URL, so a view can be
bookmarked or shared:

```text
/viewer/objects?q=&type=&size=&status=&reach=&sort=&dir=&limit=&offset=&selected=&tab=
```

An enumeration the viewer does not know, a malformed selected digest, or a
negative offset answers `400` instead of being corrected; only the page size is
clamped into range, and the Rows control then names the size in effect. Defaults
are hash ascending, `limit=25`, no filters, and the Metadata panel.

### The two axes

Integrity and reachability are independent facts, and the viewer keeps them
apart — in the table as two columns, in the inspector as two states.

| Column | Values | Source |
|---|---|---|
| `Integrity` | `Unverified`, `Verified`, `Corrupt` | an on-demand check in this session |
| `References` | `Resolved`, `Root`, `Orphaned`, `Detached` | the host's reachability and reference indexes |

- **Verification is session state, not a stored property.** A result disappears
  with the session or a restart, which is why an unchecked object shows no
  finding at all. A failed check is rendered as owned prose with its state pill
  — `Corrupt`, with both the expected address and the digest the stored bytes
  actually hash to; `Missing`; or `Unreadable` — and the underlying Go error
  never reaches the page. An object that is absent or unreadable was never
  verified, so it keeps the neutral `Unverified` state with that explanation.
- **Reference states appear only when the host supplies the indexes.** With a
  `ReachabilityIndex`, objects unreachable from a configured root are
  `Orphaned`. Adding a `ReferenceIndex` separates `Root` (reachable, no inbound
  edges), `Detached` (unreachable, no inbound edges — the sweep candidate), and
  `Resolved` from each other. Without the indexes the column and the filter are
  hidden rather than guessed, and `reach=root` or `reach=detached` answers
  `400`.
- The two filters combine with AND, so `status=corrupt&reach=orphaned` returns
  corrupt orphans. Verification stays available for orphans: they are exactly
  the objects a sweep is about to reclaim.

### Seeing the reference states without a typed store

The CLI has no typed object model, so `cask web` can supply references for the
one graph it can derive itself: the deterministic preview graph that
`cask seed-preview` writes.

```text
cask -store ./objects seed-preview -count 500 -hash-algo sha256
cask web -store ./objects -hash-algo sha256
```

`-hash-algo` must match between the two commands: the viewer recognizes the
graph by re-deriving every ordinal's digest with its own hasher, so seeding with
one algorithm and reading with another finds no graph and shows no references.
Every eight-object block includes a `Root` entry, orphans with inbound edges,
and a `Detached` entry, so all four states are visible; every eighth object is
written with tampered bytes that do not hash to their own address, so `verify`
genuinely fails for it. An ordinary store stays reference-free until an
embedding host supplies its own indexes (`web.Config`,
[`internal/web/README.md`](../../internal/web/README.md)).

### Deployment

- **Loopback by default.** `-bind` is `127.0.0.1:8080`; exposing the viewer is
  an explicit decision.
- **Remote access needs HTTPS.** Session cookies are always `Secure`, so a
  non-loopback bind is usable only through a TLS-terminating proxy. Point the
  proxy at the bind, keep the browser's `Host` header (the same-origin rule
  compares it), and pass the proxy's address in `-trusted-proxy` so the login
  throttle keys on the real client rather than the proxy.
- **Do not expose the viewer to the public internet.** The intended shape for
  remote access is VPN plus reverse proxy, with authentication in front.
- **`-allow-insecure-bind` is an escape hatch, not a deployment.** Startup logs
  a warning, no login link is printed, and plain `http://` still cannot hold a
  session.

## Where the contracts live

| Topic | Normative file |
|---|---|
| Subcommands, flags, output, exit codes | [`docs/specs/cli.md`](../../docs/specs/cli.md) |
| Screens, columns, URL state, reference states | [`docs/specs/viewer-design.md`](../../docs/specs/viewer-design.md) |
| Tokens, sessions, roles, cookies, throttle, audit | [`docs/specs/viewer-security.md`](../../docs/specs/viewer-security.md) |
| Server wiring, middleware, lifecycle, deployment | [`docs/specs/backend-architecture.md`](../../docs/specs/backend-architecture.md) |
| Templates, htmx, URL-as-state, embedding | [`docs/specs/frontend-architecture.md`](../../docs/specs/frontend-architecture.md) |
| The viewer package: files, config, boundaries | [`internal/web/README.md`](../../internal/web/README.md) |
| Path-to-rule lookup for any file | [`docs/index.md`](../../docs/index.md) |
