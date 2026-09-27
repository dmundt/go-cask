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

// TestInstructionBudgetsAreLive pins the ceiling against the file it binds. A table
// entry whose path no longer exists is stale, and a stale entry is worse than none: it
// reads as coverage while the instruction file it named has been renamed, deleted or
// outgrown. The engine reports that staleness, and the command feeds it what it read, so
// this test checks the table the same way — the entry resolves, the file is the router
// docs/AGENT.md §2.1 describes, and the router is inside its own ceiling today.
func TestInstructionBudgetsAreLive(t *testing.T) {
	t.Parallel()

	budgets := InstructionBudgets()
	if len(budgets) == 0 {
		t.Fatal("no instruction budget is declared, so nothing bounds an auto-read instruction file")
	}

	root := repoRoot(t)
	for _, budget := range budgets {
		if budget.MaxBytes <= 0 {
			t.Errorf("%s carries ceiling %d, want a positive byte count", budget.Path, budget.MaxBytes)
			continue
		}
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(budget.Path)))
		if err != nil {
			t.Errorf("instruction budget names %s: %v", budget.Path, err)
			continue
		}
		fields, found := docs.Fields(string(content))
		if !found {
			t.Errorf("%s carries no frontmatter; an auto-read router declares type, title, description and version", budget.Path)
			continue
		}
		// The router routes: the description says so, which is the property the
		// ceiling exists to protect. A file that starts specifying is the drift
		// the budget is meant to catch first.
		if !strings.Contains(strings.ToLower(fields["description"]), "router") {
			t.Errorf("%s describes itself as %q, not as the router docs/AGENT.md §2.1 requires", budget.Path, fields["description"])
		}
		if len(content) > budget.MaxBytes {
			t.Errorf("%s is %d bytes, over its %d-byte ceiling: relocate a rule instead of raising the ceiling",
				budget.Path, len(content), budget.MaxBytes)
		}
	}
}
