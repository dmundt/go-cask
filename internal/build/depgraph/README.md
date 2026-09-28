---
type: Guide
title: depgraph (build engine) — go-cask
description: Local package dependency graph; the committed Mermaid document showing it.
version: v2
---

# depgraph

Derives a module's local package dependency graph; renders the committed Mermaid
document that displays it.

## The graph

`Derive` takes the module path + the package list `go list` reports; returns three
sorted sets:

- **nodes** — module-relative package paths;
- **edges** — `from>to` pairs, one per local import;
- **leaves** — packages importing no local package; filled style, so a reader need not
  trace every arrow to find the bottom.

Local edges only: an import outside the module is not a node. An import target is a node
even when its own package was not listed → an edge always has both ends. Everything
ordered by byte value → the document is reproducible on any host.

## The document

`Document` renders the frontmatter, the caller's introduction, the diagram and the
caller's closing sections. Repository-specific parts = the caller's: title, description,
generator name, **subgraphs**, prose; this package ships none of it.

`Subgraph` = id + title + `Claims` predicate. **The first subgraph that claims a package
owns it** → the order is the rule; trees nest (`cas/backend/fs` inside `cas`), so a table
must test the narrower tree first and make each claim exclusive, else a package is drawn
twice.

```go
doc := depgraph.Doc{Title: "Graph", Generator: "…", Subgraphs: []depgraph.Subgraph{
    {ID: "CORE", Title: "core", Claims: ownedBy("/core")},
}}
graph := depgraph.Derive(module, packages)
rendered := depgraph.Document(doc, graph, version)
```

`Version` reads a committed document's frontmatter version; `Bump` moves it by one →
re-render at the version already on disk (regeneration stays idempotent), and move the
version only when the body actually changed.

## Testing

`go test ./depgraph/` — node ids, derivation from a fixture package list, the version
arithmetic, and that the document carries the caller's prose verbatim.
