package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestExamplesTableIsWellFormed pins the table's own shape: every row names a
// directory, no name is used twice, and the manual entries carry the commands a reader
// needs — a manual example with no commands would be refused with nothing to do.
func TestExamplesTableIsWellFormed(t *testing.T) {
	t.Parallel()

	table := Examples()
	if len(table) == 0 {
		t.Fatal("the example table is empty")
	}
	seen := map[string]bool{}
	for _, example := range table {
		if example.Name == "" {
			t.Error("a table row names no example")
			continue
		}
		if seen[example.Name] {
			t.Errorf("the table carries %q twice, so the name resolves to whichever row is first", example.Name)
		}
		seen[example.Name] = true
		if len(example.Manual) > 0 && len(example.Args) > 0 {
			t.Errorf("%s carries both manual commands and run arguments; only one can apply", example.Name)
		}
	}
}

// TestExamplesTableMatchesTheTree holds the table against the tree in both directions:
// every row names a directory that exists, and every example program under `examples/`
// is reachable from the table. A new example that no runner knows is exactly the drift
// this catches.
func TestExamplesTableMatchesTheTree(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	entries, err := os.ReadDir(filepath.Join(root, ExamplesDir))
	if err != nil {
		t.Fatalf("reading %s: %v", ExamplesDir, err)
	}

	for _, example := range Examples() {
		dir := filepath.Join(root, ExamplesDir, example.Name)
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Errorf("the table names %s/%s, which is not a directory: %v", ExamplesDir, example.Name, err)
		}
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// An example is a directory holding a Go program of its own; `examples/api`
		// holds its programs in subdirectories, which is why it is manual.
		children, err := os.ReadDir(filepath.Join(root, ExamplesDir, entry.Name()))
		if err != nil {
			t.Fatalf("reading %s/%s: %v", ExamplesDir, entry.Name(), err)
		}
		program := false
		for _, child := range children {
			if !child.IsDir() && strings.HasSuffix(child.Name(), ".go") {
				program = true
				break
			}
		}
		if !program {
			continue
		}
		if _, found := findExample(entry.Name()); !found {
			t.Errorf("%s/%s holds a Go program and no table row names it, so no runner knows it",
				ExamplesDir, entry.Name())
		}
	}
}

// findExample reports whether the table carries an example by name.
func findExample(name string) (int, bool) {
	for i, example := range Examples() {
		if example.Name == name {
			return i, true
		}
	}
	return 0, false
}
