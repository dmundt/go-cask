package depgraph

import (
	"strings"
	"testing"
)

// TestNodeID pins the Mermaid id: no slash or dot survives, and the substitution
// is per character so two packages cannot collide by collapsing differently.
func TestNodeID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		pkg  string
		want string
	}{
		{pkg: "cas", want: "cas"},
		{pkg: "cas/backend/fs", want: "cas_backend_fs"},
		{pkg: "cas/codec/sha256", want: "cas_codec_sha256"},
		{pkg: "examples/api/demo", want: "examples_api_demo"},
		{pkg: "cas", want: "cas"},
	}
	for _, test := range tests {
		if got := NodeID(test.pkg); got != test.want {
			t.Errorf("NodeID(%q) = %q, want %q", test.pkg, got, test.want)
		}
	}
	// Every non-alphanumeric character collapses to "_", so a package path that
	// already contained an underscore would share an id with one that used a
	// slash. No package path in this repository does; the substitution is
	// deliberately the one the generator used rather than a scheme that would
	// rename every node in the committed document.
}

// TestDerive pins the edges and leaves: only local imports become edges, an
// import target is a node even when its own package was not listed, and a package
// with no local import is a leaf.
func TestDerive(t *testing.T) {
	t.Parallel()

	const module = "example.com/mod"
	graph := Derive(module, []Package{
		{ImportPath: "example.com/mod/cmd/cask", Imports: []string{"context", "example.com/mod/cas", "example.com/mod/internal/store"}},
		{ImportPath: "example.com/mod/cas", Imports: []string{"context", "io"}},
		{ImportPath: "example.com/mod/internal/store", Imports: []string{"example.com/mod/cas"}},
		{ImportPath: "example.com/mod/benchmarks", Imports: nil},
		// Outside the module: neither a node nor an edge.
		{ImportPath: "example.com/other/x", Imports: []string{"example.com/mod/cas"}},
	})

	wantNodes := []string{"benchmarks", "cas", "cmd/cask", "internal/store"}
	if strings.Join(graph.Nodes, ",") != strings.Join(wantNodes, ",") {
		t.Errorf("Nodes = %q, want %q", graph.Nodes, wantNodes)
	}
	wantEdges := []string{"cmd/cask>cas", "cmd/cask>internal/store", "internal/store>cas"}
	if strings.Join(graph.Edges, ",") != strings.Join(wantEdges, ",") {
		t.Errorf("Edges = %q, want %q", graph.Edges, wantEdges)
	}
	wantLeaves := []string{"benchmarks", "cas"}
	if strings.Join(graph.Leaves, ",") != strings.Join(wantLeaves, ",") {
		t.Errorf("Leaves = %q, want %q", graph.Leaves, wantLeaves)
	}
}

// TestBump pins the version arithmetic and its refusal to guess.
func TestBump(t *testing.T) {
	t.Parallel()

	for _, test := range []struct{ in, want string }{
		{in: "v1", want: "v2"},
		{in: "v9", want: "v10"},
		{in: "v12", want: "v13"},
	} {
		got, err := Bump(test.in)
		if err != nil {
			t.Errorf("Bump(%q) returned %v", test.in, err)
			continue
		}
		if got != test.want {
			t.Errorf("Bump(%q) = %q, want %q", test.in, got, test.want)
		}
	}
	for _, bad := range []string{"", "v", "one", "v1x", "1"} {
		if _, err := Bump(bad); err == nil {
			t.Errorf("Bump(%q) succeeded, want an error", bad)
		}
	}
}

// TestVersion pins the frontmatter read the generator uses to decide whether its
// artifact changed, and that it round-trips what Frontmatter writes.
func TestVersion(t *testing.T) {
	t.Parallel()

	doc := Doc{
		Title:           "Graph",
		Description:     "A graph.",
		Generator:       "some/tool",
		FrontmatterType: "Design Document",
	}
	if got := Version(Frontmatter(doc, "v7")); got != "v7" {
		t.Errorf("Version(Frontmatter(doc, v7)) = %q, want v7", got)
	}
	if got := Version("# no frontmatter\n"); got != "" {
		t.Errorf("Version of a document without frontmatter = %q, want \"\"", got)
	}
}

// TestDocumentUsesTheCallersProse pins that the document carries the caller's
// introduction and closing text verbatim, so a caller's own architecture notes are
// what a reader sees.
func TestDocumentUsesTheCallersProse(t *testing.T) {
	t.Parallel()

	doc := Doc{
		Title:           "Graph — project",
		Description:     "A graph.",
		Generator:       "some/tool",
		FrontmatterType: "Design Document",
		Subgraphs:       []Subgraph{{ID: "ALL", Title: "Everything", Claims: func(string) bool { return true }}},
		Intro:           "\n# Graph — project\n\nThe introduction.\n",
		Outro:           "\n## Regenerating\n\nUse `some/tool`.\n",
	}
	graph := Derive("example.com/mod", []Package{{ImportPath: "example.com/mod/a"}})
	rendered := Document(doc, graph, "v3")

	for _, want := range []string{
		"type: Design Document",
		"title: Graph — project",
		"version: v3",
		"generated: some/tool",
		"The introduction.",
		"```mermaid",
		"## Regenerating",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered document lacks %q:\n%s", want, rendered)
		}
	}
}
