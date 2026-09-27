---
type: Guide
title: website (build engine) — go-cask
description: Checks keeping a docs site honest — Go fences, inventory tables, footer.
version: v3
---

# website

Checks that keep a documentation site honest about the code it publishes.

## Every Go fence is a complete unit

- `Materialize` → walks a site's Markdown, finds each fenced block, writes the Go ones into
  a scratch tree, one directory per block.
- Complete unit = copyable into a file: fence language exactly `go`, first non-blank line a
  package clause.
- A fragment, or a fence tagged `go extra` → reported, not written.
- Caller then builds and vets the materialized set: an uncompilable snippet = documentation
  that lies about the library.
- The scratch tree must not survive the run: it holds generated `.go` files inside the module
  → a later `gofmt -l .` would report them.

## Inventory tables match the tree

`Inventory` names a page and the tree it documents. `CheckInventory` compares the packages
under that tree — each immediate subdirectory holding at least one `.go` file — against the
backtick-quoted package names in the page's table rows:

- **missing** → a shipped package the table does not document;
- **extra** → a row naming a package that no longer exists;
- only table rows are read → prose may mention a package without counting as documented;
- a page that is not there → reported, not treated as an empty, satisfied table.

## The tables are the caller's

No inventory list shipped: which pages promise which tables = one site's decision.
`GoBlocks`, `Block.Dir`, `ShippedPackages`, `DocumentedPackages` exported for a caller
needing the pieces.

## The footer is one pinned line

- `FoldedScalar` → the base line a site's configuration declares as a `key: >-` folded
  scalar.
- `FooterLine` → completes it for one revision: the revision's year after the site's year
  token, then the label and the date, linked to the commit.
- `FooterFindings` → reports a base line carrying a literal year, a line that is not one
  line, text the contract forbids, the revision as visible text, a lost link, the label
  inside the anchor.
- A build that cannot read a revision → renders the base line: values degrade, never guessed.
- `SourceGuard`, `CheckAbsentPaths` → the same rule on the surrounding text: text a file must
  still carry, deleted machinery it must not carry, paths that must not exist again.
- Caller supplies every path, the pinned line, the guard lists → nothing here names a
  repository.

## Testing

`go test ./website/` — extraction, the not-a-unit and multi-info errors, scratch clearing,
inventory parsing, both inventory directions on a fixture tree, and the footer rules:
folding, the composed line, each finding.
