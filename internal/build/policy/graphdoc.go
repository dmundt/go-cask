package policy

import (
	"github.com/dmundt/go-cask/internal/build/core/depgraph"
	"github.com/dmundt/go-cask/internal/build/core/website"
	"strings"
)

// GraphDocPath is the committed artifact the package graph is rendered into. The
// engine renders; this repository decides where the document lives.
const GraphDocPath = "docs/design/package-graph.md"

// Inventories are the site's shipped-package inventory tables: each page that
// promises to document every package under a tree.
func Inventories() []website.Inventory {
	return []website.Inventory{
		{Page: "concepts/backends.md", Root: "cas/backend"},
		{Page: "concepts/hashes.md", Root: "cas/hash"},
		{Page: "concepts/codecs.md", Root: "cas/codec"},
		{Page: "specifications/object-format.md", Root: "cas/codec"},
	}
}

// GraphDoc is everything the package graph document needs beyond the graph: its
// title, its prose, and how a package is assigned to a subgraph.
//
// The subgraph order is the drawing order, and the FIRST subgraph that claims a
// package owns it. The claims must therefore be both exclusive and correctly
// ordered, and the trees nest: `cas/backend/fs` is inside `cas`, so whichever of
// the byte layer and the core is drawn first takes it.
//
// The grouping mirrors the arms AGENTS.md "Layers and citizen classes" states,
// resolved the way the bash `case` this replaced resolved them: the narrower tree is
// tested first, and each layer excludes the narrower one it contains.
func GraphDoc() depgraph.Doc {
	byteLayer := ownedBy("/cas/backend")
	core := ownedBy("/cas")
	// The helpers are the rest of the cas tree: under cas/, but neither the byte
	// layer nor the core itself.
	help := func(pkg string) bool {
		return core(pkg) && !byteLayer(pkg) && pkg != "cas"
	}
	reference := ownedBy("/gitlike")
	internal := ownedBy("/internal")
	// Everything the layers above did not claim is an application.
	apps := notClaimedBy(byteLayer, core, reference, internal)

	return depgraph.Doc{
		Title:           "Package Dependency Graph — go-cask",
		Description:     "Generated dependency graph of every package in the go-cask module, derived from go list and owned by internal/build/depgraph.",
		Generator:       "internal/build/depgraph",
		FrontmatterType: "Design Document",
		Subgraphs: []depgraph.Subgraph{
			{ID: "APPS", Title: "Applications - cmd/cask, examples, benchmarks", Claims: apps},
			{ID: "REFERENCE", Title: "Reference library - gitlike", Claims: reference},
			{ID: "INTERNAL", Title: "internal - not importable outside the module", Claims: internal},
			{ID: "HELP", Title: "cas helper and typed layer", Claims: help},
			{ID: "BYTE", Title: "cas/backend - byte layer", Claims: byteLayer},
			{ID: "CORE", Title: "cas - generic core", Claims: core},
		},
		Intro: graphIntro,
		Outro: graphOutro,
	}
}

// graphIntro is the document's text before the diagram. It is the exact text the
// committed file carries, which a test pins by re-rendering it.
const graphIntro = `
# Package Dependency Graph — go-cask

The local dependency graph of every package in this module, grouped by layer. An
edge points from the importing package to the package it imports, so the
applications sit at the top and ` + "`cas`" + ` — which imports no local package at all —
sits at the bottom.

The diagram is generated from ` + "`go list`" + ` by ` + "`internal/build/depgraph`" + `; edit that
package, not this file. Only production imports are drawn: imports that appear
solely in ` + "`_test.go`" + ` files are excluded and the standard library is not drawn, so
this is the consumer-visible build graph. The local package and edge sets do not
vary with ` + "`GOOS`" + ` or ` + "`GOARCH`" + `, so the file is reproducible on any host.
`

// graphOutro is the document's text after the diagram, including the closing
// regeneration note.
const graphOutro = `
## Layers

| Layer | Subgraph | Holds |
|---|---|---|
| Core | ` + "`CORE`" + ` | The generic ` + "`cas`" + ` core: ` + "`Store[T]`" + `, ` + "`Digest`" + `, ` + "`Hasher`" + `, ` + "`Codec[T]`" + `, the envelope. |
| Byte layer | ` + "`BYTE`" + ` | ` + "`cas/backend`" + ` and its ` + "`fs`" + `, ` + "`mem`" + `, ` + "`packfs`" + ` and ` + "`snapshot`" + ` implementations. |
| Helpers | ` + "`HELP`" + ` | The typed and maintenance layer under ` + "`cas/`" + `: codecs, hashers, caches, bloom filters, verification, ` + "`pack`" + `, ` + "`refs`" + `, ` + "`repo`" + `. |
| Reference library | ` + "`REFERENCE`" + ` | ` + "`gitlike`" + `: the reference object model apps and examples build on. A 2nd-class library, not an application. |
| Internal | ` + "`INTERNAL`" + ` | ` + "`internal/*`" + `, not importable outside this module. |
| Applications | ` + "`APPS`" + ` | ` + "`cmd/cask`" + ` (the product binary), ` + "`examples/*`" + ` and ` + "`benchmarks`" + `. |

Packages that import no local package are drawn as leaves.

## Regenerating

` + "```bash" + `
go run ./cmd/buildtool dep-graph --write   # rewrite this file from go list
go run ./cmd/buildtool dep-graph           # report staleness; writes nothing
` + "```" + `

` + "`scripts/verify.sh`" + ` runs the checking form, so a change that adds, removes or
re-points a local import must regenerate this file in the same change. Regeneration
is idempotent: when the graph is unchanged nothing is written and the frontmatter
` + "`version`" + ` is left alone; when the graph changes, that version moves by one.
`

// ownedBy returns a predicate for a tree given as a module-relative prefix with a
// leading slash: "/cas" claims `cas` and everything beneath it, but not a sibling
// whose name merely starts with it. The root itself counts, which is why a prefix
// is compared with and without its trailing slash.
func ownedBy(prefix string) func(string) bool {
	root := strings.TrimPrefix(prefix, "/")
	return func(pkg string) bool {
		return pkg == root || strings.HasPrefix(pkg, root+"/")
	}
}

// notClaimedBy returns a predicate for a package no listed tree owns, which is how
// a catch-all subgraph collects what the other layers left.
func notClaimedBy(trees ...func(string) bool) func(string) bool {
	return func(pkg string) bool {
		for _, owns := range trees {
			if owns(pkg) {
				return false
			}
		}
		return true
	}
}
