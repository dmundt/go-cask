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

// codecLabelLiteral is the one string literal that spells the census's
// "no codec identity" answer.
const codecLabelLiteral = "unspecified"

// codecLabelOwner declares where that literal is allowed to live: the constant
// both surfaces read, beside the Entry.Codec it labels (go-cask#324).
const codecLabelOwner = "internal/index/index.go"

// codecLabelFinding is one `"unspecified"` string literal the scan found in a
// non-test Go file.
type codecLabelFinding struct {
	// file is the repository-relative path.
	file string
	// line is where the literal appears.
	line int
	// context names the declaration it belongs to, e.g. "const UnspecifiedCodec".
	context string
}

// String renders the finding the way the test reports it.
func (f codecLabelFinding) String() string {
	return fmt.Sprintf("%s:%d: %s", f.file, f.line, f.context)
}

// TestCodecLabelHasOneOwner is the rule: the literal that renders a frame with no
// codec identity is declared once, in internal/index, and never restated by a
// surface that renders it (go-cask#324).
//
// This is the render half of the census convention cli.md §3 states — one header
// read feeds every surface, and every surface reports the same value. That held
// for internal/index.Entry (one BuildSnapshot fills it) but not for the label:
// cmd/cask and internal/web each declared their own `unspecified` constant, so
// either could have drifted from the other with every test still green, because
// each surface's test asserted its own literal.
func TestCodecLabelHasOneOwner(t *testing.T) {
	var stray []string
	for _, f := range scanCodecLabelLiterals(t) {
		if f.file != codecLabelOwner {
			stray = append(stray, f.String())
		}
	}
	if len(stray) > 0 {
		t.Errorf("a second %q codec label was declared (cli.md §3, viewer-design §3):\n  %s\n"+
			"Render it through index.CodecLabel instead of restating the literal; the label "+
			"lives once, beside the index.Entry.Codec both surfaces fill.",
			codecLabelLiteral, strings.Join(stray, "\n  "))
	}
}

// TestCodecLabelOwnerStillDeclaresIt keeps the rule honest in the other
// direction: the owner must actually hold the literal, so deleting or renaming
// the constant surfaces here instead of silently disarming the check above.
func TestCodecLabelOwnerStillDeclaresIt(t *testing.T) {
	for _, f := range scanCodecLabelLiterals(t) {
		if f.file == codecLabelOwner {
			return
		}
	}
	t.Errorf("%s no longer declares a %q string literal; the census label and this check "+
		"must move together", codecLabelOwner, codecLabelLiteral)
}

// TestDetectsCodecLabelLiterals pins the detector: a package-level const, a var,
// and a literal returned from a function are all found, while a documented
// literal (prose in a comment) and a differently-cased near miss are not.
func TestDetectsCodecLabelLiterals(t *testing.T) {
	const src = `package snippet

// "unspecified" here is prose in a comment, not a declaration.
const A = "unspecified"

var B = "unspecified"

func f() string { return "unspecified" }

const C = "Unspecified"
const D = "unspecified "
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "snippet.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse snippet: %v", err)
	}
	got := codecLabelLiteralsInFile(fset, file, "snippet.go")
	if len(got) != 3 {
		t.Fatalf("detected %d literals, want 3 (the const, the var and the return): %v", len(got), got)
	}
	for _, f := range got {
		if f.file != "snippet.go" {
			t.Errorf("finding %v carries file %q, want snippet.go", f, f.file)
		}
		if f.line <= 0 {
			t.Errorf("finding %v carries no line", f)
		}
	}
}

// scanCodecLabelLiterals walks the module's own non-test Go sources and returns
// every `"unspecified"` string literal with the declaration it belongs to.
//
// Test files are skipped on purpose: a test may assert the rendered value by
// name, and this file's own detector table quotes the literal as data.
func scanCodecLabelLiterals(t *testing.T) []codecLabelFinding {
	t.Helper()
	root := repoRoot(t)
	var out []codecLabelFinding
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			// `.gocache` holds other sessions' worktrees, and `site`/`vendor`
			// are generated or third-party; none of them is this module's source.
			if path != root && (strings.HasPrefix(name, ".") || name == "site" || name == "vendor" || name == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("parse %s: %w", rel, err)
		}
		out = append(out, codecLabelLiteralsInFile(fset, file, rel)...)
		return nil
	})
	if err != nil {
		t.Fatalf("scan repository: %v", err)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].file != out[j].file {
			return out[i].file < out[j].file
		}
		return out[i].line < out[j].line
	})
	return out
}

// codecLabelLiteralsInFile reports every string literal equal to the label in one
// parsed file, naming the declaration it sits in. fset must be the FileSet the
// tree was parsed with, so a finding points at the literal's own line.
func codecLabelLiteralsInFile(fset *token.FileSet, file *ast.File, rel string) []codecLabelFinding {
	quoted := strconv.Quote(codecLabelLiteral)
	var out []codecLabelFinding
	for _, decl := range file.Decls {
		context := declContext(decl)
		ast.Inspect(decl, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING || lit.Value != quoted {
				return true
			}
			out = append(out, codecLabelFinding{
				file:    rel,
				line:    fset.Position(lit.Pos()).Line,
				context: context,
			})
			return true
		})
	}
	return out
}

// declContext names the declaration a literal sits in, so a finding reads
// "const UnspecifiedCodec" rather than a bare line number. An unrecognized
// declaration shape (a bare `func` in a generated file) reads as its kind.
func declContext(decl ast.Decl) string {
	switch d := decl.(type) {
	case *ast.GenDecl:
		for _, spec := range d.Specs {
			switch s := spec.(type) {
			case *ast.ValueSpec:
				if len(s.Names) > 0 {
					return strings.ToLower(d.Tok.String()) + " " + s.Names[0].Name
				}
			case *ast.TypeSpec:
				return "type " + s.Name.Name
			}
		}
		return d.Tok.String()
	case *ast.FuncDecl:
		return "func " + d.Name.Name
	default:
		return "declaration"
	}
}
