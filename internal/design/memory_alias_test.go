package design

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// canonicalMemoryAlias maps each import path whose package clause is `memory`
// to the alias this repository imports it under (cas/AGENT.md "Core rules",
// library-design §1, go-cask#269). Both packages export the same three names
// (`New`, `Option`, `Backend`), so an unaliased import leaves a reader guessing
// which half of the store a selector belongs to — the backend or the cache.
//
// The aliases are a convention, not a rename: both clauses are named in the
// frozen surface (cas-core §7.1), so changing one is a breaking change and
// waits for a major version.
var canonicalMemoryAlias = map[string]string{
	"github.com/dmundt/go-cask/cas/backend/mem": "backmem",
	"github.com/dmundt/go-cask/cas/cache/mem":   "cachemem",
}

// memoryImport is one import of a `package memory` path, as written in a file.
type memoryImport struct {
	// file is the repository-relative path.
	file string
	// line is the import's line.
	line int
	// alias is the identifier the import binds: its explicit alias, or the
	// package clause (`memory`) when the import carries none.
	alias string
	// importPath is the imported package path.
	importPath string
}

// String renders the import for a failure message.
func (m memoryImport) String() string {
	return fmt.Sprintf("%s:%d: imported as %s, want %s", m.file, m.line, m.alias, canonicalMemoryAlias[m.importPath])
}

// TestMemoryPackagesUseCanonicalAliases is the rule itself: every import of
// cas/backend/mem and cas/cache/mem in the module — test files included, since
// the aliases are what the tests read — names the canonical alias.
func TestMemoryPackagesUseCanonicalAliases(t *testing.T) {
	var offenders []string
	for _, imp := range scanMemoryImports(t) {
		if want := canonicalMemoryAlias[imp.importPath]; imp.alias != want {
			offenders = append(offenders, imp.String())
		}
	}
	if len(offenders) > 0 {
		t.Errorf("the two `package memory` packages must be imported under their canonical aliases (cas/AGENT.md, library-design §1):\n  %s\n"+
			"Import cas/backend/mem as `backmem` and cas/cache/mem as `cachemem`; never unaliased, and never as `mem`, `memory`, `membackend` or `memcache`.",
			strings.Join(offenders, "\n  "))
	}
}

// TestDetectsMemoryImportAliases pins the detector: an explicit alias, an
// unaliased import, and a non-memory import are all classified correctly.
func TestDetectsMemoryImportAliases(t *testing.T) {
	const src = `package p

import (
	backmem "github.com/dmundt/go-cask/cas/backend/mem"
	"github.com/dmundt/go-cask/cas/cache/mem"
	mem "github.com/dmundt/go-cask/cas/cache/lru"
	_ "github.com/dmundt/go-cask/cas/cache/mem"
)
`

	got := memoryImportsInSource(t, src)
	want := []string{
		"snippet.go:4: imported as backmem, want backmem",
		"snippet.go:5: imported as memory, want cachemem",
		"snippet.go:7: imported as _, want cachemem",
	}
	var rendered []string
	for _, imp := range got {
		rendered = append(rendered, imp.String())
	}
	if strings.Join(rendered, "\n") != strings.Join(want, "\n") {
		t.Errorf("memory imports = %v, want %v", rendered, want)
	}
}

// memoryImportsInSource parses one source string and returns its memory imports,
// with the expected alias substituted for the canonical one in String.
func memoryImportsInSource(t *testing.T, src string) []memoryImport {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "snippet.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse snippet: %v", err)
	}
	out := memoryImportsInFile(fset, file, "snippet.go")
	sort.Slice(out, func(i, j int) bool { return out[i].line < out[j].line })
	return out
}

// scanMemoryImports walks the module's own Go sources — tests included, because
// they import these packages most — and returns every import of a
// `package memory` path.
func scanMemoryImports(t *testing.T) []memoryImport {
	t.Helper()
	root := repoRoot(t)
	var out []memoryImport
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			// `.gocache` holds other sessions' worktrees, and `site`/`vendor`
			// are generated or third-party; none of them is this module's code.
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
		out = append(out, memoryImportsInFile(fset, file, rel)...)
		return nil
	})
	if err != nil {
		t.Fatalf("scan repository: %v", err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// memoryImportsInFile reports the file's imports of a `package memory` path that
// do not use the canonical alias. The import path is carried in a field the
// String method reads.
func memoryImportsInFile(fset *token.FileSet, file *ast.File, rel string) []memoryImport {
	var out []memoryImport
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		if _, ok := canonicalMemoryAlias[importPath]; !ok {
			continue
		}
		alias := "memory" // the package clause, when the import carries no alias
		if spec.Name != nil {
			alias = spec.Name.Name
		}
		out = append(out, memoryImport{
			file:       rel,
			line:       fset.Position(spec.Pos()).Line,
			alias:      alias,
			importPath: importPath,
		})
	}
	return out
}
