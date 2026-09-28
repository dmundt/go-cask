# cask — the CLI and the viewer

`cmd/cask` builds the module's only binary: a thin CLI over the `cas` library, plus `cask web`
— the embedded viewer, the product's only HTTP surface. Every command maps to a core operation
or the viewer composition; never a second implementation.

Contract — subcommands, flags, output, exit codes:
[`docs/specs/cli.md`](../../docs/specs/cli.md). Below: the operator's path into the viewer;
normative contracts stay with the specs linked at the end.

## Store operations

```text
cask -store ./objects put <file>          # store bytes; prints sha256:<hexdigest>
cask -store ./objects get <hash> -o out   # retrieve to a file or stdout
cask -store ./objects list -limit 20      # list objects
cask -store ./objects meta <hash>         # size, envelope type, algorithm
cask -store ./objects stats               # N objects, M bytes
cask -store ./objects verify <hash>       # recompute the address
cask -store ./objects verify --all -hash-algo sha512
                                          # ... for a store addressed by another algorithm
cask -store ./objects gc --min-age 1h <roots...>
cask -store ./objects prune --min-age 1h <roots...>
cask -store ./objects clean
cask -store ./objects seed-preview -count 500
```

- `-store` names the store's own directory. Both backends reject an empty value, `.`, a
  filesystem or volume root, and a parent-traversal path before creating anything.
- `-backend fs` (default) or `-backend packfs`; every store operation runs over either, the
  viewer does not.
- Every store operation except `verify` digests with `cas/hash/sha256` — the core names no
  algorithm, and that constant is the CLI's own default. `verify -hash-algo <name>` selects the
  algorithm its addresses are expressed in, so a `sha512`- or `sha512_256`-addressed store stays
  checkable; the viewer path (`web`, `seed-preview`) takes the same flag.
- Destructive: `gc`, `prune`, `clean`. `gc` and `prune` are grace-gated — they reclaim only
  objects absent from `<roots...>` **and** older than `--min-age` (default `1h`). `<roots...>`
  MUST already be the complete reachable set, not just entry points: the byte layer cannot
  expand a root into what it references.
- Sweeps take the store's exclusive cross-process lock (`.cask.lock`, store root); writers and
  reads never lock.

## The viewer (`cask web`)

`cask web` starts a server-rendered object browser: what is stored here, how large, and are
these still the bytes their addresses claim? Loopback-bound, login required, HTML only —
vendored htmx, one scoped stylesheet, no JSON API, no custom JavaScript.

Byte-layer tool: objects, envelope types, exact sizes, bytes, on-demand integrity results.
Never resolves typed references, never imports an application object model.

```text
cask web -store ./objects -backend fs -bind 127.0.0.1:8080 \
  -hash-algo sha256 -tokens viewer=…,operator=…,admin=…
```

Invoking `cask web` is the explicit enablement: no other subcommand starts the viewer, no
`enabled` switch. Flags only — no config file.

### Flags

| Flag | Default | Effect |
|---|---|---|
| `-store` | `./objects` | store directory to inspect |
| `-backend` | `fs` | must be `fs`: the viewer reads per-object physical metadata, so `packfs` is refused with an error naming the operation (exit 1) |
| `-bind` | `127.0.0.1:8080` | listen address; loopback spellings are pinned to an explicit numeric address before listening, and any other address needs `-allow-insecure-bind` (Deployment) |
| `-hash-algo` | `sha256` | `sha256`, `sha512`, or `sha512_256`; parses and validates digests, verifies, and shows in Metadata → Identity → Algorithm |
| `-tokens` | empty | comma-separated `role=token` pairs for non-admin logins, for example `viewer=…,operator=…` |
| `-token-file` | empty | file holding the startup admin token; used instead of generating one, and never displayed |
| `-trusted-proxy` | empty | IPs, `ip:port` values, or CIDR blocks whose forwarded client address the login throttle may believe; empty trusts none, and a malformed entry fails startup |
| `-allow-insecure-bind` | `false` | allow a non-loopback bind; startup then logs a prominent warning |
| `-show-token` | terminal heuristic | `-show-token` forces the one-time login hint, `-show-token=false` never shows it and never opens the browser, and an absent flag shows it only on an interactive stdout; a loopback bind is required in every case |
| `-no-open` | `false` | do not open the default browser |

Exit codes, as for every subcommand: `0` success, `1` runtime error, `2` usage error.

### Getting in

The startup token grants `admin`; resolution order:

1. `-token-file <path>`;
2. the `CASK_VIEWER_TOKEN` environment variable;
3. a per-run `crypto/rand` token, regenerated on every restart.

An operator-supplied token is never displayed. A generated one is shown once on **stdout** —
interactive stdout, or `-show-token` — loopback bind only, never through the logger at any
level; when hidden, the log names the remedy and the reason, not the token. A non-loopback bind
shows no token at all: the notice names the bind and the `https://` expectation.

Unless `-no-open` is given, `cask web` opens the default browser at the one-time deep link
`http://<bind>/viewer/?token=<token>` and prints it in the startup notice. The link
percent-encodes the token, so a supplied token holding a URL reserved character (`&`, `#`, `%`,
a space) logs in rather than being truncated. On Windows the launch goes through
`rundll32.exe url.dll,FileProtocolHandler`, not `cmd /c start`: the token passes as data, never
re-parsed as command-line syntax. The link still carries the token on the browser process's
command line, so the launch is skipped for a non-loopback bind and when `-show-token=false`
suppresses the display. The token is accepted only from the viewer's own origin — same-origin
form post, link, or htmx request, or a top-level navigation with no initiator — and only once;
the session cookie carries the session afterwards. A non-loopback bind prints no link: an
always-`Secure` cookie cannot be set over plain `http://`.

### Sessions and roles

- Session: at most 30 idle minutes and 8 hours; gone on restart. Cookies always `HttpOnly`,
  `SameSite=Strict`, `Secure`.
- Roles: `viewer` lists, browses, inspects; `operator` adds verification of one object and of
  the whole store; `admin` adds nothing further today. One role per session.
- Failed logins: five per caller address per minute with exponential backoff, audited without
  the token. Behind a reverse proxy set `-trusted-proxy` to its address — without it every
  client shares one bucket, and five failures from anyone lock out all operators.
- The viewer inspects, never destroys: no delete, GC, or prune route, and no route outside the
  table below.
- `cask web` takes no maintenance lock, so a `gc` or `prune` sweep may run while the viewer is
  live; the sweep's `--min-age` grace protects fresh objects.

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

Every other path under `/viewer/` is answered by one catch-all naming no method: `401` without
a session, `404` with one.

### What you see

Master–detail workspace: a top bar with the module version and a Verify-all control, a filter
bar, one row per stored object, an inspector following the selection.

- **Filter bar** — search text, object type, size band, integrity state, and, with the host
  indexes, the reference filter.
- **Table columns** — `Hash`, `Type`, `Size`, `Inbound`, `Integrity`, `References`, `Written`;
  every column sorts.
- **Inspector panels** — `Metadata` (full digest, algorithm, envelope type, exact size,
  physical written time, the state section, the role-gated verify form), `References`
  (digest-sorted inbound and outbound edges), `Bytes` (a lazy 16-byte-row hexdump of at most the
  first 256 bytes, with a truncation note).

Filter, sort, page, selection, and panel live in the URL, so a view is bookmarkable:

```text
/viewer/objects?q=&type=&size=&status=&reach=&sort=&dir=&limit=&offset=&selected=&tab=
```

An unknown enumeration, a malformed selected digest, or a negative offset answers `400` rather
than being corrected; only the page size is clamped into range, and the Rows control names the
size in effect. Defaults: hash ascending, `limit=25`, no filters, Metadata panel.

### The two axes

Integrity and reachability are independent, kept apart — two table columns, two inspector
states.

| Column | Values | Source |
|---|---|---|
| `Integrity` | `Unverified`, `Verified`, `Corrupt` | an on-demand check in this session |
| `References` | `Resolved`, `Root`, `Orphaned`, `Detached` | the host's reachability and reference indexes |

- **Verification is session state, not a stored property** — it disappears with the session or
  a restart, so an unchecked object shows no finding. A failed check renders as owned prose
  with its state pill — `Corrupt`, with the expected address and the digest the stored bytes
  actually hash to; `Missing`; or `Unreadable` — and the underlying Go error never reaches the
  page. An object absent or unreadable was never verified, so it keeps the neutral `Unverified`
  state with that explanation.
- **Reference states appear only when the host supplies the indexes.** A `ReachabilityIndex`
  marks objects unreachable from a configured root `Orphaned`; adding a `ReferenceIndex`
  separates `Root` (reachable, no inbound edges), `Detached` (unreachable, no inbound edges —
  the sweep candidate), and `Resolved`. Without the indexes the column and filter are hidden
  rather than guessed, and `reach=root` or `reach=detached` answers `400`.
- The filters combine with AND: `status=corrupt&reach=orphaned` returns corrupt orphans.
  Verification stays available for orphans — the objects a sweep is about to reclaim.

### Seeing the reference states without a typed store

The CLI has no typed object model, so `cask web` supplies references for the one graph it can
derive itself: the deterministic preview graph `cask seed-preview` writes.

```text
cask -store ./objects seed-preview -count 500 -hash-algo sha256
cask web -store ./objects -hash-algo sha256
```

`-hash-algo` MUST match between the two commands: the viewer recognizes the graph by
re-deriving every ordinal's digest with its own hasher, so seeding with one algorithm and
reading with another finds no graph and shows no references. Every eight-object block includes
a `Root` entry, orphans with inbound edges, and a `Detached` entry, so all four states are
visible; every eighth object carries tampered bytes that do not hash to their own address, so
`verify` genuinely fails for it. Seeded objects are current-format envelopes with the `preview`
codec tag (the payload is a synthetic byte pattern no shipped codec produced), framed through
the core's own writer so their digests match what the store would have written. An ordinary
store stays reference-free until an embedding host supplies its own indexes (`web.Config`,
[`internal/web/README.md`](../../internal/web/README.md)).

### Deployment

- **Loopback by default.** `-bind` is `127.0.0.1:8080`; exposing the viewer is an explicit
  decision.
- **Remote access needs HTTPS.** Cookies are always `Secure`, so a non-loopback bind works only
  through a TLS-terminating proxy. Point the proxy at the bind, keep the browser's `Host`
  header (the same-origin rule compares it), and pass the proxy's address in `-trusted-proxy`
  so the login throttle keys on the real client, not the proxy.
- **Never expose the viewer to the public internet.** Intended remote shape: VPN plus reverse
  proxy, authentication in front.
- **`-allow-insecure-bind` is an escape hatch, not a deployment.** Startup logs a warning, no
  login link is printed, plain `http://` still cannot hold a session.
- **The bind address is pinned, not resolved.** `-bind` accepts `127.0.0.1`, `localhost`, and
  `[::1]`; `cask` resolves `localhost` to `127.0.0.1` before the listener exists, so the
  listener, printed origin, deep link, and browser URL agree on every machine instead of
  following the hosts file. A bare `:8080`, `0.0.0.0:8080`, or `[::]:8080` listens on every
  interface and is refused like any other non-loopback address.
- **A firewall prompt means the bind left loopback** (Windows Defender Firewall, for one). The
  default `127.0.0.1:8080` never prompts, so a prompt is the override's expected consequence,
  not a viewer defect.
- **Inside WSL?** A viewer started in WSL2 and opened from the Windows browser crosses the WSL
  localhost relay; a prompt there names the relay (`wslrelay.exe` or `vmmem`), not `cask`. No
  `-bind` value avoids it: use the address WSL reports.

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
