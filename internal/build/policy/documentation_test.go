package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/internal/build/core/docs"
)

// TestEveryBuildReadmeIsVersioned pins the subtree's documentation rule: a README under
// `internal/build` is a versioned document, so a reader can tell how current it is — and
// so the version-field rule covers it when it changes. A README with no frontmatter is
// judged by nothing at all, which is the quiet failure this check exists to prevent.
//
// The requirement is scoped to this subtree on purpose: it is the one whose READMEs carry
// frontmatter today, and widening it means writing the frontmatter for the other trees
// first.
func TestEveryBuildReadmeIsVersioned(t *testing.T) {
	t.Parallel()

	root := filepath.Join(repoRoot(t), "internal", "build")
	directories, err := buildDirectories(root)
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	if len(directories) < 2 {
		t.Fatalf("found %d directories under internal/build, so this test would prove nothing", len(directories))
	}

	table := PackageReadme()
	checked := 0
	for _, dir := range directories {
		rel, err := filepath.Rel(root, dir)
		if err != nil {
			t.Fatalf("relative path for %s: %v", dir, err)
		}
		readme := filepath.Join(dir, "README.md")
		content, err := os.ReadFile(readme)
		if err != nil {
			// A directory without a README is the sibling test's finding, not this one's.
			continue
		}
		checked++
		where := "internal/build/" + filepath.ToSlash(rel) + "/README.md"

		fields, found := docs.Fields(string(content))
		if !found {
			t.Errorf("%s carries no frontmatter; a versioned document declares type, title, description and version", where)
			continue
		}
		if missing := docs.MissingFields(string(content), table.Fields); len(missing) != 0 {
			t.Errorf("%s is missing frontmatter %s", where, strings.Join(missing, ", "))
		}
		if fields["type"] != table.Type {
			t.Errorf("%s declares type %q, want %q", where, fields["type"], table.Type)
		}
		if version := fields["version"]; !strings.HasPrefix(version, "v") || len(version) < 2 {
			t.Errorf("%s declares version %q, want the v<number> form docs/AGENT.md §5 uses", where, version)
		}
	}

	// The check must have looked at the tree's READMEs, not walked past them.
	if checked < 2 {
		t.Fatalf("inspected %d README(s) under internal/build, so this test proved nothing", checked)
	}
}
