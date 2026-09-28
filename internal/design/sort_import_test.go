package design

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// sortBuildToolReason and sortBuildCheckReason explain the two trees the sweep
// behind go-cask#341 did not cover: the `cask` build tool and the gate's own
// build-time checkers. #341's table named only `cas/**` and the repository's
// design checks, so these files are the sweep's boundary rather than leftovers,
// and they are owned by lanes this change does not touch.
const (
	sortBuildToolReason  = "build-tool source: outside #341's sweep (cas/** and the design checks)"
	sortBuildCheckReason = "build-time checker: outside #341's sweep (cas/** and the design checks)"
)

// sortAllowed is the explicit allow-list of files still permitted to import
// "sort", keyed by the repository-relative path and carrying the reason it must
// keep the old package. Adding an entry IS the decision the check asks for; a
// file that stops importing "sort" must be removed from the list, so an
// exemption cannot rot into blanket permission.
var sortAllowed = map[string]string{
	"cmd/gate/main.go":                            sortBuildToolReason,
	"cmd/gate/prlane.go":                          sortBuildToolReason,
	"cmd/gate/taskstatus.go":                      sortBuildToolReason,
	"cmd/gate/worktree.go":                        sortBuildToolReason,
	"internal/build/coverage/coverage.go":         sortBuildCheckReason,
	"internal/build/depgraph/depgraph.go":         sortBuildCheckReason,
	"internal/build/deps/deps.go":                 sortBuildCheckReason,
	"internal/build/docs/docs.go":                 sortBuildCheckReason,
	"internal/build/layers/layers.go":             sortBuildCheckReason,
	"internal/build/policy/documentation_test.go": sortBuildCheckReason,
	"internal/build/policy/verify_test.go":        sortBuildCheckReason,
	"internal/build/receipt/receipt.go":           sortBuildCheckReason,
	"internal/build/taskstate/taskstate.go":       sortBuildCheckReason,
	"internal/build/versioning/versioning.go":     sortBuildCheckReason,
	"internal/build/website/website.go":           sortBuildCheckReason,
	"internal/build/website/website_test.go":      sortBuildCheckReason,
}

// sortImport is one `import "sort"` the scan found.
type sortImport struct {
	// file is the repository-relative path.
	file string
	// line is the import's line.
	line int
}

// String renders the import for a failure message.
func (s sortImport) String() string {
	return fmt.Sprintf("%s:%d", s.file, s.line)
}

// TestSortImportHasAReason is the rule go-cask#341 asks for: sorting goes
// through `slices` (with `cmp.Compare` for a non-ordered key) rather than the
// `sort` package, in every file the sweep covers. A new `sort` import therefore
// fails `go test ./...` instead of waiting for the next audit, and the only way
// to keep it is to name the file in sortAllowed with its reason.
func TestSortImportHasAReason(t *testing.T) {
	var unexpected []string
	for _, imp := range scanSortImports(t) {
		if _, ok := sortAllowed[imp.file]; !ok {
			unexpected = append(unexpected, imp.String())
		}
	}
	if len(unexpected) > 0 {
		t.Errorf("`sort` is imported outside the allow-list (go-cask#341, coding-guidelines §3):\n  %s\n"+
			"Use slices.Sort / slices.SortFunc with cmp.Compare / slices.IsSortedFunc (or slices.SortStableFunc), "+
			"or add the file to sortAllowed with the reason it must keep `sort`.",
			strings.Join(unexpected, "\n  "))
	}
}

// TestSortAllowListIsCurrent keeps the exemption honest in the other direction:
// a listed file that no longer imports `sort` — because it was swept or deleted
// — must leave the list, so the allow-list records today's boundary rather than
// a growing permission.
func TestSortAllowListIsCurrent(t *testing.T) {
	found := make(map[string]bool)
	for _, imp := range scanSortImports(t) {
		found[imp.file] = true
	}
	for file, reason := range sortAllowed {
		if !found[file] {
			t.Errorf("sortAllowed still lists %s (%s), but that file no longer imports `sort`; remove the stale exemption",
				file, reason)
		}
	}
}

// TestDetectsSortImports pins the detector: every form of the import is found —
// plain, aliased, dot-imported and blank — while a file that merely mentions
// `sort` (as a local name, or inside a string) is not.
func TestDetectsSortImports(t *testing.T) {
	const src = `package p

import (
	"sort"
	alias "sort"
	. "sort"
	_ "sort"
	"fmt"
)

var local = fmt.Sprintf("sort")
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "snippet.go", src, parser.SkipObjectResolution|parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse snippet: %v", err)
	}
	got := sortImportsInFile(fset, file, "snippet.go")
	if len(got) != 4 {
		t.Fatalf("detected %d sort imports, want 4 (plain, aliased, dot and blank): %v", len(got), got)
	}
	for _, imp := range got {
		if imp.file != "snippet.go" || imp.line <= 0 {
			t.Errorf("finding %v does not name snippet.go and its line", imp)
		}
	}

	const clean = `package p

import "slices"

var sorted = slices.Sorted(m)
`
	file, err = parser.ParseFile(token.NewFileSet(), "clean.go", clean, parser.SkipObjectResolution|parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse clean snippet: %v", err)
	}
	if got := sortImportsInFile(token.NewFileSet(), file, "clean.go"); len(got) != 0 {
		t.Errorf("a snippet without the import produced findings: %v", got)
	}
}

// scanSortImports walks the module's own Go sources — tests included, since the
// sweep converts test files too — and returns every import of "sort".
func scanSortImports(t *testing.T) []sortImport {
	t.Helper()
	root := repoRoot(t)
	var out []sortImport
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			// The scratch tree and the task worktrees live in dot-directories,
			// and `site`/`vendor` are generated or third-party; none of them is
			// this module's code.
			if path != root && (strings.HasPrefix(name, ".") || name == "site" || name == "vendor" || name == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution|parser.ImportsOnly)
		if err != nil {
			return fmt.Errorf("parse %s: %w", rel, err)
		}
		out = append(out, sortImportsInFile(fset, file, rel)...)
		return nil
	})
	if err != nil {
		t.Fatalf("scan repository: %v", err)
	}
	slices.SortFunc(out, func(a, b sortImport) int {
		if a.file != b.file {
			return strings.Compare(a.file, b.file)
		}
		return a.line - b.line
	})
	return out
}

// sortImportsInFile reports the file's imports of the `sort` package, however
// the import binds it.
func sortImportsInFile(fset *token.FileSet, file *ast.File, rel string) []sortImport {
	var out []sortImport
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || path != "sort" {
			continue
		}
		out = append(out, sortImport{file: rel, line: fset.Position(spec.Pos()).Line})
	}
	return out
}
