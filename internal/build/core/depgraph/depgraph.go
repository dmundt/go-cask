// Package depgraph derives a module's local package dependency graph as a Mermaid
// flowchart, and renders a committed document that displays it.
//
// The rules it encodes:
//
//   - Only local edges are drawn: an import outside the module (the standard
//     library, a dependency) is not a node.
//   - A package that imports no local package is a leaf, drawn with the filled
//     style, so a reader does not have to trace every arrow to find the bottom.
//   - Packages are grouped into subgraphs the caller names, each claiming a set of
//     packages; the order of the subgraphs is the drawing order, and the first one
//     that claims a package owns it.
//
// Everything is ordered by byte value, so the rendered document is reproducible on
// any host. The document's prose and its subgraph definitions are the caller's —
// this package ships neither.
//
// Deriving and rendering are pure functions over the package list and a Doc, so
// both are covered by ordinary tests; the caller reads `go list`, decides the file
// path, and writes the result.
package depgraph

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dmundt/go-cask/internal/build/core/docs"
)

// Package is one package's local import data.
type Package struct {
	// ImportPath is the package's own path, as `go list` reports it.
	ImportPath string
	// Imports are the import paths it uses.
	Imports []string
}

// Graph is the derived local dependency graph, in byte order.
type Graph struct {
	// Nodes are the module-relative package paths, sorted.
	Nodes []string
	// Edges are "from>to" module-relative pairs, sorted.
	Edges []string
	// Leaves are the nodes that import no local package, sorted.
	Leaves []string
}

// Subgraph is one group in the diagram: a set of packages and the title drawn
// above them.
type Subgraph struct {
	// ID is the Mermaid subgraph id, which may not contain '/' or '.'.
	ID string
	// Title is the label a reader sees.
	Title string
	// Claims reports whether a module-relative package belongs to this subgraph.
	// The first subgraph that claims a package owns it, so the order is the rule.
	Claims func(pkg string) bool
}

// Doc is everything a rendered document needs that is not the graph itself.
type Doc struct {
	// Title is the document's H1 name and its frontmatter title, which the caller
	// writes with whatever project suffix it uses.
	Title string
	// Description is the frontmatter description.
	Description string
	// Generator names what wrote the file, for the frontmatter and for the "edit
	// that, not this" note the caller's prose carries.
	Generator string
	// FrontmatterType is the frontmatter `type:` value, e.g. "Design Document".
	FrontmatterType string
	// Subgraphs are the groups, in drawing order.
	Subgraphs []Subgraph
	// Intro is the text between the frontmatter and the diagram, which is where a
	// caller describes its own architecture.
	Intro string
	// Outro is the text after the diagram: the caller's tables and notes.
	Outro string
}

// Derive returns the local graph for a module, from the package list `go list`
// reports. Packages outside the module are ignored, and so are their imports.
func Derive(module string, packages []Package) Graph {
	prefix := module + "/"
	nodeSet := map[string]bool{}
	edgeSet := map[string]bool{}
	leafSet := map[string]bool{}

	for _, pkg := range packages {
		if !strings.HasPrefix(pkg.ImportPath, prefix) {
			continue
		}
		node := strings.TrimPrefix(pkg.ImportPath, prefix)
		nodeSet[node] = true

		localImports := 0
		for _, imp := range pkg.Imports {
			if !strings.HasPrefix(imp, prefix) {
				continue
			}
			target := strings.TrimPrefix(imp, prefix)
			// An import target is a node too, so an edge always has both ends.
			nodeSet[target] = true
			edgeSet[node+">"+target] = true
			localImports++
		}
		if localImports == 0 {
			leafSet[node] = true
		}
	}

	return Graph{
		Nodes:  sortedKeys(nodeSet),
		Edges:  sortedKeys(edgeSet),
		Leaves: sortedKeys(leafSet),
	}
}

// NodeID renders a Mermaid node id: Mermaid ids may not contain '/' or '.', and
// the label beside the node is the package path the reader sees.
func NodeID(pkg string) string {
	var b strings.Builder
	for _, r := range pkg {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// Frontmatter renders a document's frontmatter block for a version.
func Frontmatter(doc Doc, version string) string {
	return docs.Frontmatter(
		[2]string{"type", doc.FrontmatterType},
		[2]string{"title", doc.Title},
		[2]string{"description", doc.Description},
		[2]string{"version", version},
		[2]string{"generated", doc.Generator},
	)
}

// Body renders the diagram alone: the subgraphs, the edges and the leaf styling.
func Body(doc Doc, graph Graph) string {
	var b strings.Builder
	b.WriteString("flowchart TD\n")
	for _, subgraph := range doc.Subgraphs {
		fmt.Fprintf(&b, "  subgraph %s[%q]\n", subgraph.ID, subgraph.Title)
		for _, node := range graph.Nodes {
			if !subgraph.Claims(node) {
				continue
			}
			fmt.Fprintf(&b, "    %s[%q]\n", NodeID(node), node)
		}
		b.WriteString("  end\n")
	}

	b.WriteString("\n")
	for _, edge := range graph.Edges {
		from, to, _ := strings.Cut(edge, ">")
		fmt.Fprintf(&b, "  %s --> %s\n", NodeID(from), NodeID(to))
	}

	b.WriteString("\n")
	b.WriteString("  classDef leaf fill:#eef7ee,stroke:#4a7c59,color:#12321c\n")
	ids := make([]string, 0, len(graph.Leaves))
	for _, leaf := range graph.Leaves {
		ids = append(ids, NodeID(leaf))
	}
	if len(ids) != 0 {
		fmt.Fprintf(&b, "  class %s leaf\n", strings.Join(ids, ","))
	}
	return b.String()
}

// Document renders the whole committed document for a version: the frontmatter,
// the caller's introduction, the diagram, and the caller's closing sections.
func Document(doc Doc, graph Graph, version string) string {
	var b strings.Builder
	b.WriteString(Frontmatter(doc, version))
	b.WriteString(doc.Intro)
	b.WriteString("\n```mermaid\n")
	b.WriteString(Body(doc, graph))
	b.WriteString("```\n")
	b.WriteString(doc.Outro)
	return b.String()
}

// Version returns the frontmatter `version:` of a document, or "" when it has
// none, so a caller can re-render at the version already committed.
func Version(document string) string { return docs.Field(document) }

// Bump returns the next version after one, reading the `vN` spelling the
// frontmatter uses. It reports an error rather than guessing when the value is not
// `v<digits>` — an unreadable version means the artifact cannot be bumped, so
// writing it would lose the version rather than move it.
func Bump(version string) (string, error) {
	digits, ok := strings.CutPrefix(version, "v")
	if !ok || digits == "" {
		return "", fmt.Errorf("cannot read the frontmatter version %q", version)
	}
	number := 0
	for _, r := range digits {
		if r < '0' || r > '9' {
			return "", fmt.Errorf("cannot read the frontmatter version %q", version)
		}
		number = number*10 + int(r-'0')
	}
	return fmt.Sprintf("v%d", number+1), nil
}

// sortedKeys returns a map's keys in byte order, matching `LC_ALL=C sort -u`.
func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
