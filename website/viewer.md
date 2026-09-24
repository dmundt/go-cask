# The viewer (`cask web`)

`cask web` starts the embedded viewer: a server-rendered object browser that
answers "what is stored here, how large is it, and is this object still the bytes
its address claims?". It binds to loopback, requires a login, and serves HTML
only — vendored htmx, one scoped stylesheet, no JSON API. It is a byte-layer
tool: objects, their envelope type, size, bytes and on-demand integrity results,
never typed references or an application object model.

```text
cask web -store ./objects -backend fs -bind 127.0.0.1:8080 \
  -hash-algo sha256 -tokens viewer=…,operator=…,admin=…
```

Flags only, no config file. The viewer needs `-backend fs`, because it reads
per-object physical metadata; any other backend is refused with an error naming
the operation. Exposure is decided by `-bind` (loopback by default),
`-allow-insecure-bind`, `-tokens`/`-token-file`, `-trusted-proxy`, `-show-token`
and `-no-open`; the full flag contract is
[cli.md](https://github.com/dmundt/go-cask/blob/main/docs/specs/cli.md).

## Getting in

The startup token grants `admin`, and comes from `-token-file`, then
`CASK_VIEWER_TOKEN`, then a per-run `crypto/rand` value regenerated on restart. A
token you supply is never displayed; a generated one is shown once on stdout —
never through the logger at any level — when stdout is a terminal or when
`-show-token` asks for it, and otherwise the log names the remedy, not the token.

Sign in at `/viewer/`, or follow the `/viewer/?token=…` deep link `cask web` opens
unless `-no-open` was given. The token is accepted only from the viewer's own
origin and used once: the session cookie carries the session afterwards. A link
is printed only for a loopback bind, because `Secure` cookies mean a non-loopback
viewer is reachable only over `https://` through a TLS-terminating proxy.

## Sessions and roles

- A session lasts at most 30 idle minutes and 8 hours, and disappears on restart.
  Cookies are always `HttpOnly`, `SameSite=Strict` and `Secure`, so plain
  `http://` cannot hold a session.
- `viewer` lists, browses and inspects; `operator` adds verification; `admin`
  adds nothing further today.
- There is no delete, GC or prune route: destructive operations stay in the CLI,
  where they are scripted and paired with the roots a sweep needs.
- Login failures are throttled at five per caller address per minute and audited
  without the token. Behind a reverse proxy, set `-trusted-proxy`, or every
  client shares one throttle bucket.

## What you see

A master–detail workspace: a filter bar (search, type, size band, integrity, and
a reference filter when the host supplies the indexes), one row per stored object
(`Hash`, `Type`, `Size`, `Inbound`, `Integrity`, `References`, `Written`), and an
inspector with Metadata, References and Bytes panels — the last a lazy
16-byte-row hexdump of at most the first 256 bytes. Filter, sort, page, selection
and panel all live in the URL, so a view can be bookmarked or shared.

**Reference states** answer "what is safe to collect?", and appear only when the
host supplies the indexes: `Resolved` (reachable, with inbound edges), `Root`
(reachable, none), `Orphaned` (unreachable, still referenced), `Detached`
(unreachable and isolated — the sweep candidate). Without an index the column and
filter are hidden rather than guessed; `cask seed-preview` wires both for one
store shape.

**Verification** is session state, not a stored property: results vanish with the
session or a restart, which is why an unchecked object shows no finding.

## Where the detail lives

- [viewer-design.md](https://github.com/dmundt/go-cask/blob/main/docs/specs/viewer-design.md) —
  the page, the query contract, the four states, the availability matrix
- [viewer-security.md](https://github.com/dmundt/go-cask/blob/main/docs/specs/viewer-security.md) —
  token handling, sessions, roles, cookies, throttling, audit logging
- [cli.md](https://github.com/dmundt/go-cask/blob/main/docs/specs/cli.md) — every
  flag, its default, and the exit codes
- [backend-architecture.md](https://github.com/dmundt/go-cask/blob/main/docs/specs/backend-architecture.md) —
  how `cask web` is wired, configured and deployed
- [frontend-architecture.md](https://github.com/dmundt/go-cask/blob/main/docs/specs/frontend-architecture.md) —
  templates, htmx, URL-as-state
