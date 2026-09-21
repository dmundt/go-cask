---
type: Specification
title: Viewer Design — go-cask
description: Design of the embedded technical viewer — a styled, server-rendered master-detail object browser composed from Go templates, scoped CSS, and htmx-only interaction.
version: v31
---

# Viewer Design — go-cask

The embedded technical browser UI in `internal/web/` is a dense, desktop-first
object browser for developers and operators. It defines the viewer's screens,
visual system, template composition, and hypermedia interactions. Read with
`viewer-security.md`, `frontend-architecture.md`, `coding-guidelines.md`, and
`api-design.md`. The visual reference is
[`docs/design/go-cask-viewer.html`](../design/go-cask-viewer.html); its
JavaScript is prototype-only and MUST NOT ship.

## 1. Purpose and boundaries

- Persona: developer/operator answering "what is stored, how large is it, and
  is a selected object intact?"
- The viewer is a **byte-layer tool**. It shows objects, envelope types, exact
  sizes, bytes, and on-demand integrity results. It MUST NOT resolve typed
  references or import application object models.
- Object identity is a raw lowercase-hex digest. Lists show `Digest.Prefix(8)`
  followed by `…`; the inspector shows the full digest in a readonly text
  field, which gives it one selection target without a clipboard script. The
  client algorithm may appear in the
  metadata view as `sha256`, but MUST NOT prefix a displayed digest.
- The object browser is the sole operational workspace and viewer landing at
  `/viewer/`.
- Out of scope: mutable object editing, uploads, buckets, charting, JSON APIs,
  browser storage, client-side application state, and custom JavaScript. htmx
  is the only script the viewer ships.

## 2. Visual system

The viewer MUST reproduce the mockup's restrained technical-browser hierarchy
through `internal/web/viewer.css`: a white/near-white surface, dark foreground,
muted metadata, hairline borders, one green accent, system body font, and
monospace hashes/numbers/bytes. The CSS file is the only viewer stylesheet
(coding-guidelines §4).

| Token/metric | Contract |
|---|---|
| Top bar | 46px; `CA` mark, `go-cask` wordmark, and the build version as secondary text |
| Filter bar | 47px; search, type, size, and integrity filters plus reset |
| Main workspace | flexible object-list column and 440px inspector column; the inspector resizes natively through CSS |
| Inspector bounds | 280px–560px visual range; fixed 440px default |
| Object table | dense mono data, sticky header, content-sized digest/size/inbound/integrity/references/written columns, type fills remaining width |
| Controls | one 28px height across form controls, actions, and pager; compact bordered pager/action controls; icon-sized history arrows stay 22px |
| Narrow view | at ≤900px, document scrolls; list precedes full-width inspector; filters scroll horizontally |

Colors, font stack, spacing, radii, status-tag colors, row hover/selection
tints, and focus indicators MUST follow the token values in
[`go-cask-object-browser.design.json`](../design/go-cask-object-browser.design.json).
CSS provides appearance only: every control, value, state label, and focusable
target MUST remain semantic HTML.

Interactive controls MUST take their size from the three-step control type
scale (`--viewer-control` for 28px form controls, `--viewer-control-sm` for
compact 22–28px controls, `--viewer-control-xs` for icon-sized controls) rather
than inheriting the body font, which is sized for prose and overwhelms a 28px
control. Status pills carry a faint outline derived from their own text colour
(`color-mix` against `currentColor`), so each state keeps a single hue. The control font reset that normalises the user-agent font onto the
shell font MUST stay at zero specificity (`:where(.viewer-shell) button, …`),
because a specificity-bearing reset outranks every single-class component rule
and silently discards the declared size.

## 3. Pages and visible data

| Route | View | Role |
|---|---|---|
| `/viewer/` | master-detail object browser landing | viewer |
| `/viewer/objects` | object-browser compatibility route | viewer |
| `/viewer/objects/{hash}` | cold-load object link: redirects (303) to the browser with that object selected | viewer |
| `/viewer/objects/{hash}/raw` | lazy hexdump fragment | viewer |

The browser has a top bar, filter bar, table/pager master column, and inspector
detail column. The object table contains digest, type, IEC-formatted size,
optional
inbound-reference count, integrity status, and any optional metadata the
backend can supply truthfully. It MUST
NOT fabricate reference counts, object age, incoming/outgoing references, or
stored verification state. A `not verified` status is valid until an
on-demand verification result exists in the current server session. A
successful or failed verification MUST refresh the visible object table through
an htmx response event so its status cell immediately reflects the
session-scoped result.

Each recorded verification MUST carry the time it ran, and the Metadata tab
MUST restate the recorded finding — its state, its explanation, the digests of
a mismatch, and the time of the check — every time the object is selected, not
only in the response to the click that produced it. A result is only as good as
its age — bytes can rot after a check — so the reported time MUST be the age at
render time rather than a label frozen when the check ran. An object that has
not been checked in this session MUST show no finding at all: a row saying so
states only that the operator has not clicked yet.

The top bar MUST offer a Verify control that verifies every stored object in
one request, records each result in the session, refreshes the object table
through the same status event, and reports the resulting counts on the control
itself. A sweep changes the integrity of the object currently open in the
inspector too, so the inspector MUST subscribe to that status event and
re-render alongside the table. The sweep is audited as a single event with
counts, because one audit line per object would flood the log.

The top bar MUST render the build's module version beside the wordmark, so a
page in a bug report identifies the binary that produced it. It comes from
build info — a pseudo-version until the first tag and `dev` for a build with
no module version (versioning §2) — and the CLI's `version` subcommand MUST
print the same string from the same source, because two version readers
eventually disagree.

A failed action MUST be presented as structured prose, never as a raw Go error
string. The viewer MUST classify the failure with the `cas` sentinel errors and
render a state pill, a plain-language explanation, and — for a digest mismatch
— both the expected address and the digest the stored bytes actually hash to.
Reporting one unlabeled hash does not tell an operator which side it is.

The integrity filter orders its states as Verified, Unverified, and Corrupt.
These are exclusive alternatives on one axis, so the filter is a single-choice
`status` control with an empty value meaning every state. Reachability is the
other axis and MUST be a separate single-choice `reach` filter
(`reachable`/`orphaned`, empty meaning either). The two combine with AND —
`status=corrupt&reach=orphaned` returns corrupt orphans only. Mixing the axes
in one control is forbidden: it makes
combinations that describe nothing selectable. An unknown value on either
filter is rejected with 400. A host MAY supply a `ReachabilityIndex` containing
root-based graph
reachability. Only then does the viewer offer the reachability filter and label
objects that
are unreachable from those roots as orphaned. It MUST NOT infer orphanhood from
zero inbound references. Without this source, a `reach` query returns 400.

Byte integrity and root reachability are orthogonal axes, and the viewer MUST
keep them independent facts in both storage and display, including in their
names: `Status` is not a label either axis may use, because a single "status"
implies one verdict where there are two. The table therefore gives each axis a
column of its own — `Integrity` (Unverified, Verified, or Corrupt) and
`References` (Resolved or Orphaned) — and the inspector names the same two
rows. `Resolved` rather than `Reachable`, because a root has no inbound
references yet is reachable by definition, and the latter name invited reading
the column as a refcount. Neither axis may be collapsed into or suppressed by
the other, because
doing so hides a corrupt orphan's integrity behind its reachability (or the
reverse) exactly when both matter. The `References` column and its filter
appear only when a `ReachabilityIndex` is configured; the inbound-reference
count is named `Inbound` so it never collides with them. Each filter matches
its own axis, so a corrupt orphan is returned by `status=corrupt` and by
`reach=orphaned` alike.

Verification MUST remain available for orphaned objects. Orphans are the
objects most likely to rot unnoticed and are the ones GC is about to reclaim,
so disabling the action would remove the only way to learn their integrity.

The inspector and object table label the inbound-reference count
`References`. It is the number of source objects reported by `Inbound` for the
selected digest. Without a viewer `ReferenceIndex`, the viewer renders zero
and MUST NOT scan or infer arbitrary raw payloads.

The filesystem backend may expose its physical modification time as `Written`
and `Timestamp`. `Written` renders elapsed whole minutes below one hour, whole
hours below one day, and whole days thereafter (for example, `59m ago`,
`3h ago`, or `2d ago`); `Timestamp` renders the same value in UTC RFC 3339
format. Neither is an immutable logical creation timestamp.

The inspector contains a summary header and server-selected panels:

1. **Metadata** — full digest, client algorithm, envelope type, exact size in
   its Storage section,
   physical written/timestamp metadata when available, and a State section
   carrying the integrity result with its last check time, the reachability
   verdict, and the inbound-reference count. The count belongs to the
   reference axis, not to the storage facts, so it sits beside the verdict it
   qualifies. The section ends with the role-gated verify form. The
   forms remain ordinary, CSRF-protected POSTs. No redundant cold-detail link
   appears. There is no separate Actions tab: acting on an object belongs next
   to the state that justifies the action.
2. **Bytes** — lazy 16-byte-row hexdump of at most the first 256 bytes and a
   truncation note when more bytes exist.

Byte quantities outside Metadata → Storage → Size use base-1024 IEC units:
`B`, `KiB`, `MiB`, `GiB`, and `TiB`. The object table, pager totals,
truncation notes, and cold-detail size use this format; the selected object's
Storage size remains its exact integer byte count.

3. **References** — when the host supplies a complete viewer `ReferenceIndex`, show
   deterministic digest-sorted inbound (**By**) and outbound (**Out**) edges.
   Each edge is a selectable digest link and shows its stored envelope type
   when readable (the `@1` suffix is omitted in its compact pill). Without an
   index, render empty By and Out states.

Copy-to-clipboard is a prototype behavior and not a viewer feature: writing to
the clipboard needs a script, so the inspector instead renders the digest in a
readonly field the operator can select and copy. Inspector history is a viewer
feature but a server one — the session-scoped trail above, not browser state.
The inspector resizes through the CSS `resize` property, bounded to 280–560px
by `min-width`/`max-width`; its width is visual state only and resets on
reload. The viewer therefore ships no script of its own beyond htmx.

## 4. Rendering architecture and composition

The viewer uses `html/template`, parsed from `embed.FS`, and executes named
templates into a buffer before writing a response. `shell` is the only
template that MAY emit document structure. It owns document type, language,
head, and body, and selects one page content component through a typed shell
view model. Page content components MUST NOT emit their own document chrome.
Templates are deliberately small and compose into pages and fragments:

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
integrity, or layout. The concrete implementation inventory and composition
tree live in [`../design/viewer-template-index.md`](../design/viewer-template-index.md).

## 5. URL state and htmx interactions

The object-browser URL owns all view state:

```text
/viewer/objects?q=<text>&type=<type>&size=<bucket>&status=<state>
  &sort=<hash|type|size|inbound|status|reach|written>&dir=<asc|desc>&limit=<1..250>
  &offset=<non-negative>&selected=<digest>&tab=<metadata|references|bytes|actions>
```

- Omitted values select defaults: hash ascending, `limit=25`, `offset=0`, no
  filters, metadata panel.
- Validate every parameter. Invalid enumerations, malformed selected digests,
  and negative/non-numeric offsets return 400; handlers MUST NOT silently
  correct invalid input. `limit` is the exception: a page size is a
  preference, not a claim about the store, so a numeric limit outside the
  offered set is clamped into range and the Rows control MUST name the size
  actually in effect.
- Filtering or changing `limit` resets `offset` to zero. Sorting preserves
  filters and selection only when the selected digest remains in the filtered
  result set.
- Every data column sorts, the two reference columns included: a column
  showing a fact the operator cannot order by is a dead end.
- The server filters, sorts, counts, slices, and renders. It returns
  `#object-list` (table plus pager) for htmx requests and the whole document
  otherwise.
- Search uses `hx-get`, `input changed delay:300ms`, `hx-include` of the
  filter form, `hx-target="#object-list"`, and `hx-push-url="true"`.
- Sort headers and pager are ordinary links with complete query state; htmx
  may enhance them with `hx-get`/`hx-target`/`hx-push-url`, never replacing
  their link behavior.
- Selecting a row is a real link that sets `selected`. htmx may request the
  same URL and swap `#object-inspector`; a direct request renders the complete
  object-detail document.
- Selection defaults are a rendering choice, never navigation: with rows
  present and no selection carried, the first visible row backs the inspector,
  and a selection the active filters dropped falls back the same way. That
  fallback only reaches the screen if it travels with the table, so a list
  swap MUST carry the inspector out of band, and the refresher URL embedded in
  the response MUST name the fallback rather than the dropped digest. An
  explicitly empty `selected=` is a deselect and MUST render the empty
  inspector instead, so the selected row's own link points at it.
- A selection that is present but off the current page pages the table to it,
  which is what makes reference links navigate. An explicit `offset` is an
  explicit page request and MUST win over that jump.
- The inspector header carries `‹`/`›` controls that step through the chain of
  references the session followed. A selection carries a `nav` marker saying
  how the operator reached it: `nav=ref` (a reference link) appends to the
  trail and drops the abandoned forward entries, `nav=trail` (a `‹`/`›` step)
  moves the cursor only and MUST NOT extend the trail, and an absent marker —
  a table row, a filter, a pager, a typed URL — restarts the trail at that
  object, because a table pick is a new point of departure rather than a step
  in the chain that led there. `nav=stay` (a tab switch) re-renders the same
  selection and MUST leave the trail untouched. An exhausted direction renders
  as an inert, dimmed `<span>` rather than a dead link. `nav` describes a
  single click, so it MUST NOT be carried by the URLs built from that state.
  The trail is session state — it disappears with the session and is never
  persisted.
- Inspector panel links set `tab`; no browser-only tab state. Because the tab
  therefore lives in the URL, the row and reference links rendered beneath it
  MUST carry the open tab, and a tab switch MUST re-render the object list out
  of band so those links adopt the new tab — otherwise the next pick silently
  throws the operator back to Metadata. The bytes panel lazy-loads the hexdump
  through `hx-trigger="revealed"`.
- Verify remains POST + CSRF + role checks + audit logging, and is the only
  action the inspector offers. The viewer inspects; it does not destroy.
  Deleting an object is a store-lifecycle operation that belongs to the CLI,
  where it can be scripted, audited, and paired with the roots a sweep needs,
  so the viewer MUST NOT expose a delete route or control. Verify swaps only
  `#integrity`. Because that target is
  narrow, verify MUST also swap the inspector's integrity state out of band,
  and MUST NOT report counts on the sweep control that the status cells already
  carry. The out-of-band swap MUST live in a wrapper template used only by
  action responses: the inspector renders the same result panel inline, where
  an out-of-band element is never consumed and would show up as stray markup.
- A refresh trigger MUST live inside the fragment it refreshes. On the wrapper
  an `innerHTML` swap leaves its URL untouched, so the next refresh replays the
  filters that were in effect when the page was first rendered. The object list
  and the inspector each carry their own trigger; one shared URL serves both
  because the response is selected by the request's `HX-Target`.

## 6. Security and accessibility

`viewer-security.md` applies unchanged: protected routes require a session;
401/403 have empty bodies; mutations are role-gated, CSRF-protected, and
audited; no token or secret reaches markup, logs, or browser state.

Tables require captions, scoped headers, `aria-sort` for the active sort
column, and an explicit empty row. Every form control has a label. The selected
row exposes its state, status labels contain text rather than color alone, and
all actions/links remain keyboard-operable. Focus styles in `viewer.css` MUST
meet the mockup's visible focus-ring contract.

## 7. Verification requirements

Viewer tests MUST cover:

- full-page and htmx rendering of each named component;
- URL defaults, valid filter/sort/page combinations, and invalid query 400s;
- pagination totals, boundaries, and pager links retaining query state;
- direct-link and htmx selection behavior;
- accessible table/sort/status markup and CSS asset headers;
- session/role/CSRF/error behavior and bounded raw-byte preview.

## 8. Checklist

- [x] Master-detail browser uses server-rendered HTML, htmx, and one scoped stylesheet
- [x] Component templates compose pages/fragments without duplicated rendering logic
- [x] Filter/sort/page/selection/panel state is URL-addressable and validated
- [x] Pagination is server-side and progressively enhanced
- [x] No browser storage, typed graph, or invented metadata; htmx is the only script
- [x] Security and accessibility requirements remain enforced
