package policy

import (
	"os"
	"path/filepath"
	"sort"
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

// TestInstructionBudgetsAreLive pins every ceiling against the file it binds. A table
// entry whose path no longer exists is stale, and a stale entry is worse than none: it
// reads as coverage while the instruction file it named has been renamed, deleted or
// outgrown. The engine reports that staleness and the command feeds it what it read, so
// this test checks the table the same way — the entry resolves, the file declares its
// frontmatter, and it is inside its ceiling today. Root `AGENTS.md` carries one further
// property: its description must say it is a router, because a router that starts
// specifying is the drift its ceiling exists to catch.
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
			t.Errorf("%s carries no frontmatter; an instruction file declares type, title, description and version", budget.Path)
			continue
		}
		if budget.Path == "AGENTS.md" {
			if !strings.Contains(strings.ToLower(fields["description"]), "router") {
				t.Errorf("%s describes itself as %q, not as the router docs/AGENT.md §2.1 requires", budget.Path, fields["description"])
			}
		}
		if len(content) > budget.MaxBytes {
			t.Errorf("%s is %d bytes, over its %d-byte ceiling: relocate a rule instead of raising the ceiling",
				budget.Path, len(content), budget.MaxBytes)
		}
	}
}

// TestInstructionBudgetsCoverEveryInstructionFile closes the other half of the ratchet:
// the table is not a list a file may quietly fall out of. It walks the tree for every
// instruction file — `AGENTS.md` and any `AGENT.md` — and requires a ceiling for each, so
// a new guide fails until its number is declared, and a ceiling whose file moved fails
// here rather than at the next gate run.
//
// The walk skips what is not the repository: the git directory, the worktree cache, the
// generated site and any vendored tree.
func TestInstructionBudgetsCoverEveryInstructionFile(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	found, err := instructionFiles(root)
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	if len(found) < 10 {
		t.Fatalf("found %d instruction file(s), so this test would prove nothing", len(found))
	}

	present := make(map[string]bool, len(found))
	for _, path := range found {
		present[path] = true
	}
	budgeted := map[string]bool{}
	for _, budget := range InstructionBudgets() {
		budgeted[budget.Path] = true
		if !present[budget.Path] {
			t.Errorf("instruction budget names %s, which is not an instruction file in the tree", budget.Path)
		}
	}
	for _, path := range found {
		if !budgeted[path] {
			t.Errorf("%s is an instruction file with no ceiling: declare it in policy.InstructionBudgets", path)
		}
	}
}

// instructionFiles returns every instruction file in the tree, as repository-relative
// slash paths, sorted. The skip list is the trees that are not the repository: a linked
// worktree carries its own `AGENTS.md`, and counting it would demand a ceiling for a file
// that this checkout does not own.
func instructionFiles(root string) ([]string, error) {
	var paths []string
	// The worktrees' directory comes from the policy table, so this rule follows a
	// worktree wherever that table puts it.
	worktrees := Worktrees().Parent
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".gocache", "site", "node_modules", worktrees:
				return filepath.SkipDir
			}
			return nil
		}
		switch entry.Name() {
		case "AGENT.md", "AGENTS.md":
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			paths = append(paths, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	return paths, nil
}
