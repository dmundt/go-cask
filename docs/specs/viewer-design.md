---
type: Specification
title: Viewer Design — go-cask
description: Design of the embedded technical viewer — a styled, server-rendered master-detail object browser composed from Go templates, scoped CSS, and htmx-only interaction.
version: v42
---

# Viewer Design — go-cask

The embedded technical browser UI in `internal/web/` — a dense, desktop-first
object browser for developers and operators. Defines the viewer's screens,
visual system, template composition, hypermedia interactions. Read with
`viewer-security.md`, `frontend-architecture.md`, `coding-guidelines.md`,
`api-design.md`. Visual reference:
[`docs/design/go-cask-viewer.html`](../design/go-cask-viewer.html); its
JavaScript is prototype-only and MUST NOT ship.

Reading order: this file **defines** the viewer, the
[package README](../../internal/web/README.md) maps code to these contracts, the
[CLI README](../../cmd/cask/README.md) **explains** the result to the `cask web`
operator, including the §3 reference states.

## 1. Purpose and boundaries

- Persona: developer/operator answering "what is stored, how large is it, is a
  selected object intact?"
- **Byte-layer tool**: objects, envelope types, exact sizes, bytes, on-demand
  integrity results. MUST NOT resolve typed references or import application
  object models.
- Object identity: a raw lowercase-hex digest. Lists show `Digest.Prefix(8)`
  plus `…`, the inspector the full digest in a readonly text field — one
  selection target, no clipboard script. The client algorithm may appear in the
  metadata view as `sha256`, but MUST NOT prefix a displayed digest.
- The object browser is the sole operational workspace and viewer landing at
  `/viewer/`.
- **Loose objects only.** The viewer lists through the concrete filesystem
  backend and reads each object's physical size and modification time from its
  own file, so `cask web -backend packfs` is refused with `cas.ErrUnsupported`
  and a message naming the remedy (open a loose store) rather than served with
  numbers that describe a pack file instead of an object. Pack support is a
  possible parity step, not a present capability: a packed object's loose copy
  and its pack record are two different answers to "how large is it, and how
  old", and that answer belongs here before the viewer promises any age-based
  view over packs (cli.md §1, §2; go-cask#189).
- Out of scope: mutable object editing, uploads, buckets, charting, JSON APIs,
  browser storage, client-side application state, custom JavaScript. htmx is the
  only script shipped.

## 2. Visual system

MUST use a flat VS Code-style workbench hierarchy through
`internal/web/viewer.css`: white workspace, near-white panel/header surfaces,
dark foreground, muted metadata, hairline borders, one blue accent (`#007acc`),
system body font, monospace hashes/numbers/bytes. Panels must not use shadows,
gradients, elevation, or card-like decoration. The CSS file is the only viewer
stylesheet (coding-guidelines §4).

| Token/metric | Contract |
|---|---|
| Top bar | 36px; `CA` mark, `go-cask` wordmark, and the build version as secondary text |
| Filter bar | 36px; search, type, size, and integrity filters plus reset |
| Main workspace | flexible object-list column and 440px inspector column; the inspector resizes natively through CSS |
| Inspector bounds | 280px–560px visual range; fixed 440px default |
| Object table | fixed 26px dense mono rows, sticky 12px/600 muted header, content-sized digest/size/inbound/integrity/references/written columns, type fills remaining width |
| Controls | one 28px height across form controls, actions, and pager; compact bordered pager/action controls; icon-sized history arrows stay 22px |
| Narrow view | at ≤900px, document scrolls; list precedes full-width inspector; filters scroll horizontally |

Colors, font stack, spacing, radii, status-tag colors, row hover/selection
tints, and focus indicators MUST follow the token values in
[`go-cask-object-browser.design.json`](../design/go-cask-object-browser.design.json).
CSS provides appearance only: every control, value, state label, and focusable
target MUST remain semantic HTML.

Neutral UI surfaces use a constrained VS Code-style palette: white surface,
`#f3f3f3` panel/header and quiet hover, `#f8f8f8` workspace/disabled surface,
`#e5e5e5` divider/disabled border, `#c8c8c8` control border, `#999999`
secondary control border/scrollbar hover, `#dddddd` pressed state. New gray
values need a component-specific contrast justification.

Controls: 26px buttons, 28px inputs/selects, 0–2px radii; inspector metadata an
88px label column with an 8px value gap. Active tabs use only a 1px blue bottom
indicator. Hover states stay subtle and non-animated apart from short
background/border transitions; selection uses a muted blue background without
text inversion. Hashes, identifiers, algorithm values, hexadecimal content, and
byte views use the monospace stack.

Interactive controls MUST take their size from the three-step control type scale
(`--viewer-control` for 28px form controls, `--viewer-control-sm` for compact
22–28px controls, `--viewer-control-xs` for icon-sized controls), never the body
font, sized for prose and overwhelming a 28px control. Status pills carry a
faint outline derived from their own text colour (`currentColor`), so each state
keeps one hue; the outline stays transparent at rest, appearing only through the
state fill. The control font reset normalising the user-agent font onto the
shell font MUST stay at zero specificity (`:where(.viewer-shell) button, …`): a
specificity-bearing reset outranks every single-class component rule and
silently discards the declared size.

## 3. Pages and visible data

| Route | View | Role |
|---|---|---|
| `/viewer/` | entry point: completes the `?token=` deep link, redirects to login without a session, otherwise the object browser | viewer |
| `/viewer/objects` | the object browser itself — the target every filter, sort, page, and selection control addresses | viewer |
| `/viewer/objects/{hash}` | cold-load object link: redirects (303) to the browser with that object selected | viewer |
| `/viewer/objects/{hash}/dump` | lazy hexdump fragment (HTML, not the stored bytes) | viewer |
| `POST /viewer/objects/{hash}/verify` | verifies one object, answers with the result fragment | operator |
| `POST /viewer/objects/verify` | verifies every stored object, answers with the summary fragment | operator |
| `/viewer/login` | login page and token submission; a rejected attempt is answered `401` (empty body), and the page states the reason when it is shown again | public |
| `/viewer/static/{viewer.css,htmx.min.js}` | the viewer's only two assets, served from its own origin | public |

That table is the whole surface. Every other path under `/viewer/` hits one
catch-all naming no method, so a path the viewer never served — including the
object-delete and GC routes it deliberately omits — replies on the session, not
the path: 401 without one, 404 with. Probing cannot map the surface.

The browser is a top bar, filter bar, table/pager master column, and inspector
detail column; the object table carries digest, type, IEC-formatted size,
optional inbound-reference count, integrity status, plus optional metadata the
backend can supply truthfully. MUST NOT fabricate reference counts, object age,
incoming/outgoing references, or stored verification state. `not verified`
holds until an on-demand result exists in the current server session.
Verification — success or failure — MUST refresh the visible object table
through an htmx response event, its status cell immediately reflecting the
session-scoped result.

Digest URLs are algorithm-agnostic at the viewer layer: `Server` takes a
`cas.Hasher`; routes parse canonical hex with `cas.ParseDigest` and validate the
width through it. The CLI supplies SHA-256 by default; another client can inject
a compatible hasher without changing viewer routes.

Each recorded verification MUST carry its run time, and the Metadata tab MUST
restate the recorded finding — state, explanation, mismatch digests, check time
— every time the object is selected, not only in the click's response. A result
is only as good as its age (bytes can rot after a check), so the reported time
MUST be the age at render time, not a label frozen at check time. An object
unchecked in this session MUST show no finding at all: such a row states only
that the operator has not clicked yet.

The top bar MUST offer a Verify control verifying every stored object in one
request, recording each result in the session, refreshing the object table
through the same status event. It MUST NOT report the resulting counts (§5): the
per-object status cells already carry them, so a count would only duplicate
them. A sweep also changes the integrity of the inspector's open object, so the
inspector MUST subscribe to that status event and re-render alongside the table.
The sweep is audited as one event with counts — one audit line per object would
flood the log.

A sweep is expensive — it re-reads and re-hashes every stored object — so it is
**bounded**, and the bound is what the operator sees rather than a stall: at most
one sweep runs at a time, and one session may start `expensiveBurst` of them and
then one per cooldown (the numbers are defaults.md's, not this page's). A request that exceeds the
bound MUST answer `429` with `Retry-After` and a fragment that says how long to
wait (the control's label carries it), MUST NOT queue behind the running sweep,
and MUST NOT emit the status event. The object browser's metadata snapshot is
bounded the same way and shares the session's budget: a request refused a rebuild
MUST serve the last published snapshot — stale, not wrong — so a page always
renders, and the staleness is at most one cooldown. `Retry-After` here follows
the same rule as the login throttle's refusal (viewer-security §5).

The top bar MUST render the build's module version beside the wordmark, so a
page in a bug report identifies the producing binary. It comes from build info —
a pseudo-version until the first tag, `dev` with no module version
(versioning §2) — and the CLI's `version` subcommand MUST print the same string
from that source: two version readers eventually disagree.

A failed action MUST render as structured prose, never a raw Go error string.
The viewer MUST classify the failure with the `cas` sentinel errors and render a
state pill, a plain-language explanation, and — for a digest mismatch — both the
expected address and the digest the stored bytes hash to: one unlabeled hash
does not tell an operator which side it is.

That rule covers the whole result, not only a mismatch: a failure the sentinels
do not classify renders with the viewer's own sentence, and the underlying error
MUST NOT appear in the response — a wrapped backend or interpreter error carries
whatever the failing layer put in it, including the store's absolute paths, and
the operator needs the finding, not the layer. The error goes to the audit line
(viewer-security §9), where implementation causes are read.

The login page carries the same rule. A rejected token is answered `401` with an
empty body — the refusal a data endpoint gives a sessionless caller — and the
login page, shown again, states in one owned sentence why the operator sees it.
The rejection never describes the token or the account (api-design §5–§6).

The integrity filter orders states Verified, Unverified, Corrupt — exclusive
alternatives on one axis, so one single-choice `status` control, empty meaning
every state. Reachability is the other axis, MUST be a separate single-choice
`reach` filter (`reachable`/`orphaned`/`detached`/`root`, empty meaning any).
`detached`: an orphaned object with zero host-supplied inbound references — a
disconnected component entry, not a retention root. `root`: a reachable object
with zero host-supplied inbound references — the entry point of a reachable
subtree, structurally consistent with being a root but not an assertion that the
viewer has seen the host's actual root list (it sees only the two independent
indexes below). The axes combine with AND — `status=corrupt&reach=orphaned`
returns corrupt orphans only. Mixing axes in one control is forbidden: it makes
combinations describing nothing selectable. An unknown value on either filter is
rejected with 400. A host MAY supply a `ReachabilityIndex` with root-based graph
reachability; only then does the viewer offer the reachability filter and label
objects unreachable from those roots orphaned. It MUST NOT infer orphanhood from
zero inbound references. It MAY label an orphan with zero references `Detached`
and a reachable object with zero references `Root`, but only when the host also
supplies a `ReferenceIndex`; without that source, `reach=detached` or
`reach=root` returns 400. Without a reachability source, every `reach` query
returns 400.

Byte integrity and root reachability are orthogonal axes; the viewer MUST keep
them independent facts in storage and display, names included: `Status` is not a
label either axis may use — a single "status" implies one verdict where there
are two. Each axis therefore gets its own table column — `Integrity`
(Unverified, Verified, or Corrupt) and `References` (Resolved, Orphaned,
Detached, or Root) — and the inspector names the same states. `Detached` has a
distinct muted-violet pill, `Root` a distinct blue pill; Resolved stays green,
Orphaned amber. `Resolved` rather than `Reachable`: a root has no inbound
references yet is reachable by definition, and the latter name invited reading
the column as a refcount; `Root` is the more specific label reserved for the
zero-inbound case of that same reachable state, so the pills never overlap on
one object. Neither axis may be collapsed into or suppressed by the other —
doing so hides a corrupt orphan's integrity behind its reachability (or the
reverse) exactly when both matter. The `References` column and filter appear
only with a configured `ReachabilityIndex`; the `Detached` and `Root` filter
choices additionally require a `ReferenceIndex`. The inbound-reference count is
named `Inbound`, so it never collides with them. Each filter matches its own
axis: a corrupt orphan is returned by `status=corrupt` and by `reach=orphaned`
alike.

Verification MUST remain available for orphaned objects: orphans rot unnoticed
most often and are what GC is about to reclaim, so disabling the action would
remove the only way to learn their integrity.

The inspector and object table label the inbound-reference count `References` —
source objects reported by `Inbound` for the selected digest. Without a viewer
`ReferenceIndex`, the viewer renders zero and MUST NOT scan or infer arbitrary
raw payloads.

The filesystem backend may expose its physical modification time as `Written`
and `Timestamp`. `Written` renders elapsed whole minutes below one hour, whole
hours below one day, whole days thereafter (for example, `59m ago`, `3h ago`,
`2d ago`); `Timestamp` the same value in UTC RFC 3339. Neither is an immutable
logical creation timestamp.

The inspector has a summary header and server-selected panels:

1. **Metadata** — full digest, client algorithm, envelope type, exact size in
   its Storage section,
   physical written/timestamp metadata when available, and a State section
   carrying the integrity result with its last check time, the reachability
   verdict, and the inbound-reference count. That count belongs to the
   reference axis, not the storage facts, so it sits beside the verdict it
   qualifies. The section ends with the role-gated verify form; forms stay
   ordinary, CSRF-protected POSTs. No redundant cold-detail link
   appears. No separate Actions tab: acting on an object belongs beside the
   state justifying the action.
2. **Bytes** — lazy 16-byte-row hexdump of at most the first 256 bytes and a
   truncation note when more bytes exist.

Byte quantities outside Metadata → Storage → Size use base-1024 IEC units:
`B`, `KiB`, `MiB`, `GiB`, `TiB`. The object table, pager totals,
truncation notes, and cold-detail size use this format; the selected object's
Storage size stays its exact integer byte count.

3. **References** — with a complete host-supplied viewer `ReferenceIndex`, show
   deterministic digest-sorted inbound (**By**) and outbound (**Out**) edges.
   Each edge is a selectable digest link showing its stored envelope type
   when readable (the `@1` suffix omitted in its compact pill). Without an
   index, render empty By and Out states.

Copy-to-clipboard is prototype behavior, not a viewer feature: writing the
clipboard needs a script, so the inspector renders the digest in a readonly
field the operator can select and copy. Inspector history is a viewer feature
but a server one — the session-scoped trail above, not browser state. The
inspector resizes through the CSS `resize` property, bounded to 280–560px by
`min-width`/`max-width`; its width is visual state only, resetting on reload.
The viewer ships no script beyond htmx.

## 4. Rendering architecture and composition

The viewer uses `html/template`, parsed from `embed.FS`, executing named
templates into a buffer before writing a response. `shell` is the only template
that MAY emit document structure: it owns document type, language, head, body,
and selects one page content component through a typed shell view model. Page
content components MUST NOT emit document chrome. Templates stay deliberately
small, composing into pages and fragments:

| Component | Responsibility |
|---|---|
| `shell` | Only document shell; selects one page content component |
| `head` | metadata, `/viewer/static/viewer.css`, vendored htmx |
| `top-bar` | brand, build version |
| `*-content` | page-specific composition without document chrome |
| `filter-bar` | one GET form for durable browser state |
| `object-table` | accessible headers, rows, empty state |
| `pager` | result summary plus first/previous/next/last links |
| `object-list` | table and pager; the primary htmx swap boundary |
| `inspector` | selected-object header and metadata/bytes/actions panels |
| `integrity` | verify result fragment |
| `hexdump-table` | lazy bytes fragment |
| `result` | mutation outcome; `result-swap` adds the out-of-band status refresh for action responses |

Full pages compose these components; fragments execute the same named
components standalone. A component MUST receive pre-shaped data: templates may
range and branch but MUST NOT calculate filtering, sorting, pagination,
integrity, or layout. The implementation inventory and composition tree live in
[`../design/viewer-template-index.md`](../design/viewer-template-index.md).

## 5. URL state and htmx interactions

The object-browser URL owns all view state:

```text
/viewer/objects?q=<text>&type=<type>&size=<bucket>&status=<state>
  &sort=<hash|type|size|inbound|status|reach|written>&dir=<asc|desc>&limit=<1..250>
  &offset=<non-negative>&selected=<digest>&tab=<metadata|references|bytes|actions>
```

- Omitted values default: hash ascending, `limit=25`, `offset=0`, no filters,
  metadata panel.
- Validate every parameter: invalid enumerations, malformed selected digests,
  negative/non-numeric offsets all return 400; handlers MUST NOT silently
  correct invalid input. `limit` excepted — a page size is a preference, not a
  claim about the store — so an out-of-set numeric limit is clamped into range,
  and the Rows control MUST name the size in effect.
- Filtering or changing `limit` zeroes `offset`. Sorting preserves filters and
  selection only while the selected digest remains in the filtered result set.
- Every data column sorts, both reference columns included: a column the
  operator cannot order by is a dead end.
- The server filters, sorts, counts, slices, renders: `#object-list` (table plus
  pager) to htmx requests, the whole document otherwise.
- Search uses `hx-get`, `input changed delay:300ms`, `hx-include` of the
  filter form, `hx-target="#object-list"`, `hx-push-url="true"`.
- Sort headers and pager are ordinary links carrying complete query state; htmx
  may enhance them with `hx-get`/`hx-target`/`hx-push-url`, never replacing link
  behavior.
- Selecting a row is a real link setting `selected`; htmx may request the same
  URL and swap `#object-inspector`, a direct request rendering the complete
  object-detail document.
- Selection defaults are a rendering choice, never navigation: rows present,
  none carried — the first visible row backs the inspector; a selection the
  active filters dropped falls back the same way. That fallback reaches the
  screen only by travelling with the table, so a list swap MUST carry the
  inspector out of band, and the embedded refresher URL MUST name the fallback,
  not the dropped digest. An explicitly empty `selected=` is a deselect and MUST
  render the empty inspector, so the selected row's link points at it.
- A selection present but off-page pages the table to it — what makes reference
  links navigate. An explicit `offset` is a page request and MUST win over that
  jump.
- Inspector header `‹`/`›` controls step through the session's reference chain.
  A selection carries a `nav` marker: `nav=ref` (reference link) appends to the
  trail, dropping abandoned forward entries; `nav=trail` (a `‹`/`›` step) moves
  the cursor only, MUST NOT extend the trail; an absent marker — table row,
  filter, pager, typed URL — restarts the trail there, a table pick being a new
  departure, not a step in the chain that led there; `nav=stay` (tab switch)
  re-renders the same selection, MUST leave the trail untouched. An exhausted
  direction renders as inert, dimmed text, not a dead link. `nav` describes a
  single click, so URLs built from that state MUST NOT carry it. The trail is
  session state: gone with the session, never persisted.
- Inspector panel links set `tab`; no browser-only tab state. The panel
  switchers are navigation links marked `aria-current="page"`, not an ARIA tab
  widget: `role="tablist"`/`role="tab"` promise arrow-key roving and a linked
  `tabpanel`, which the viewer cannot deliver without the JavaScript it forbids,
  so claiming the pattern would misdescribe the control. The tab lives in the
  URL, so row and reference links beneath it MUST carry the open tab, and a tab
  switch MUST re-render the object list out of band so those links adopt it —
  otherwise the next pick silently throws the operator back to Metadata. The
  bytes panel lazy-loads the hexdump via `hx-trigger="revealed"`.
- Verify stays POST + CSRF + role checks + audit logging, the inspector's only
  action; the viewer inspects, it does not destroy. Deleting an object is a
  store-lifecycle operation belonging to the CLI — scriptable, auditable, paired
  with the roots a sweep needs — so the viewer MUST NOT expose a delete route or
  control. Verify swaps only `#integrity`; that target is narrow, so verify MUST
  also swap the inspector's integrity state out of band, and MUST NOT report on
  the sweep control counts the status cells already carry. The out-of-band swap
  MUST live in a wrapper template used only by action responses: the inspector
  renders the same result panel inline, where an out-of-band element is never
  consumed and would show up as stray markup.
- A refresh trigger MUST live inside the fragment it refreshes: on the wrapper,
  an `innerHTML` swap leaves its URL untouched, so the next refresh replays the
  filters in effect at first render. The object list and the inspector each
  carry a trigger; one shared URL serves both, the response selected by the
  request's `HX-Target`.

## 6. Security and accessibility

`viewer-security.md` applies unchanged: protected routes require a session;
401/403 have empty bodies; mutations are role-gated, CSRF-protected, audited; no
token or secret reaches markup, logs, or browser state.

Tables require captions, scoped headers, `aria-sort` for the active sort column,
an explicit empty row. Every form control has a label. The selected row exposes
its state, status labels contain text rather than color alone, all actions/links
remain keyboard-operable. Focus styles in `viewer.css` MUST meet the mockup's
visible focus-ring contract.

## 7. Verification requirements

Viewer tests MUST cover:

- full-page and htmx rendering of each named component;
- URL defaults, valid filter/sort/page combinations, and invalid query 400s;
- pagination totals, boundaries, and pager links retaining query state;
- direct-link and htmx selection behavior;
- accessible table/sort/status markup and CSS asset headers;
- session/role/CSRF/error behavior and bounded raw-byte preview;
- response hygiene: `Retry-After` on a throttled login and on a refused
  expensive operation, `Cache-Control:
  no-store` (and `Vary: Cookie`) on every response, empty rejection bodies, and
  no response body carrying a Go error string or a filesystem path.

## 8. Checklist

- [x] Master-detail browser uses server-rendered HTML, htmx, and one scoped stylesheet
- [x] Component templates compose pages/fragments without duplicated rendering logic
- [x] Filter/sort/page/selection/panel state is URL-addressable and validated
- [x] Pagination is server-side and progressively enhanced
- [x] No browser storage, typed graph, or invented metadata; htmx is the only script
- [x] Security and accessibility requirements remain enforced, including response headers and error prose
