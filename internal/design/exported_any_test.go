package design

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// allowedAny lists the exported symbols permitted to carry `any` in a value
// position, keyed by "<module-relative package path>.<symbol>": the recorded
// exception for cas/codec/cbor's dynamic value codec (library-design §5,
// go-cask#191). Adding an entry here IS the explicit ratification the rule
// requires; TestAllowListIsExact fails when an entry outlives its `any`, so the
// list cannot rot into a blanket permission.
var allowedAny = map[string]string{
	"cas/codec/cbor.NewValue": "dynamic CBOR value model by design (go-cask#191)",
	"cas/codec/cbor.NewMap":   "dynamic CBOR map model by design (go-cask#191)",
}

// finding is one exported `any` (or `interface{}`) the check found.
type finding struct {
	// symbol is "<package path>.<symbol>", with the type name inserted for a
	// method: "cas.Store.Type", "cas/codec/cbor.NewValue".
	symbol string
	// position names what carried the `any`, e.g. "parameter v", "map value",
	// "result 1", "field Value".
	position string
	// file is the repository-relative path.
	file string
	// line is where the declaration starts.
	line int
}

// String renders the finding the way the test reports it.
func (f finding) String() string {
	return fmt.Sprintf("%s: %s: %s:%d", f.symbol, f.position, f.file, f.line)
}

// TestExportedAPICarriesNoAny is the rule itself: every exported declaration in
// the module, outside the recorded exception, must be free of `any` in a value
// position.
func TestExportedAPICarriesNoAny(t *testing.T) {
	var unexpected []string
	for _, f := range scanRepo(t) {
		if _, ok := allowedAny[f.symbol]; !ok {
			unexpected = append(unexpected, f.String())
		}
	}
	if len(unexpected) > 0 {
		t.Errorf("exported `any` in a value position (library-design §5):\n  %s\n"+
			"Replace it with a concrete or constrained type, or add the symbol to allowedAny "+
			"with the explicit ratification the rule requires.",
			strings.Join(unexpected, "\n  "))
	}
}

// TestAllowListIsExact keeps the exemption honest in the other direction: an
// entry whose symbol no longer exports `any` must be deleted, so the allow-list
// stays a record of one decision rather than a growing permission.
func TestAllowListIsExact(t *testing.T) {
	found := make(map[string]bool)
	for _, f := range scanRepo(t) {
		found[f.symbol] = true
	}
	for symbol, reason := range allowedAny {
		if !found[symbol] {
			t.Errorf("allowedAny still lists %s (%s), but that symbol no longer exports `any`; remove the stale exemption",
				symbol, reason)
		}
	}
}

// TestDetectsAnyInExportedValuePositions pins the detector: each value position
// is found, including the nested ones, and each declaration kind is inspected.
func TestDetectsAnyInExportedValuePositions(t *testing.T) {
	const src = `package p

type Codec[T any] struct{}

func Direct(v any) {}

func EmptyFace(v interface{}) {}

func Slice(v []any) {}

func Map(m map[string]any) {}

func Chan(c chan any) {}

func Ptr(v *any) {}

func Func(f func(any)) {}

func Variadic(v ...any) {}

func Result() Codec[any] { return Codec[any]{} }

func Pair() (int, any) { return 0, nil }

type Record struct {
	Value any
}

type Service interface {
	Handle(any) any
}

type Client struct{}

func (Client) Send(v any) {}

var Global any
`

	got := symbolSet(findingsInSource(t, src))
	want := []string{
		"p.Chan",
		"p.Client.Send",
		"p.Direct",
		"p.EmptyFace",
		"p.Func",
		"p.Global",
		"p.Map",
		"p.Pair",
		"p.Ptr",
		"p.Record",
		"p.Result",
		"p.Service",
		"p.Slice",
		"p.Variadic",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("flagged symbols = %v, want %v", got, want)
	}

	// Pin the labelling for the nested cases, so a vague report cannot replace
	// an actionable one.
	for _, want := range []string{
		"p.Map: parameter m map value",
		"p.Slice: parameter v element",
		"p.Func: parameter f parameter 1",
		"p.Record: struct field Value",
		"p.Service: interface method Handle parameter 1",
	} {
		if !strings.Contains(strings.Join(findingStrings(findingsInSource(t, src)), "\n"), want) {
			t.Errorf("findings do not report %q:\n%s", want,
				strings.Join(findingStrings(findingsInSource(t, src)), "\n"))
		}
	}
}

// TestIgnoresConstraintsAndNonValueInterfaces pins the carve-outs: constraint
// syntax, inline interfaces with methods, unexported declarations and function
// bodies are not value positions.
func TestIgnoresConstraintsAndNonValueInterfaces(t *testing.T) {
	const src = `package p

type Constrained[T any] struct{ Value T }

type Generic[T any] interface{ Get() T }

func Identity[T any](v T) T { return v }

func WithMethod(v interface{ Read([]byte) (int, error) }) {}

func unexported(v any) {}

type unexportedRecord struct{ Value any }

func (unexportedRecord) Method(v any) {}

type Exported struct{ hidden any }

type ExportedFace interface{ hidden(any) }

func Body() {
	var local any
	_ = local
}
`

	if got := findingsInSource(t, src); len(got) != 0 {
		t.Errorf("findings = %v, want none: constraints, method-bearing interfaces, unexported declarations and bodies are not value positions",
			findingStrings(got))
	}
}

// findingsInSource parses one source string and returns its findings, so the
// detector can be tested without touching the tree.
func findingsInSource(t *testing.T, src string) []finding {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "snippet.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse snippet: %v", err)
	}
	return findingsInFile("p", "snippet.go", fset, file)
}

// symbolSet returns the sorted, deduplicated symbols of findings.
func symbolSet(findings []finding) []string {
	seen := make(map[string]bool, len(findings))
	for _, f := range findings {
		seen[f.symbol] = true
	}
	out := make([]string, 0, len(seen))
	for symbol := range seen {
		out = append(out, symbol)
	}
	sort.Strings(out)
	return out
}

// findingStrings renders findings for a failure message.
func findingStrings(findings []finding) []string {
	out := make([]string, 0, len(findings))
	for _, f := range findings {
		out = append(out, f.String())
	}
	sort.Strings(out)
	return out
}

// scanRepo walks the module's own Go sources and returns every finding.
func scanRepo(t *testing.T) []finding {
	t.Helper()
	root := repoRoot(t)
	var out []finding
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			// `.gocache` holds other sessions' worktrees, and `site`/`vendor`
			// are generated or third-party; none of them is this module's API.
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
		out = append(out, findingsInFile(packagePath(rel), rel, fset, file)...)
		return nil
	})
	if err != nil {
		t.Fatalf("scan repository: %v", err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// packagePath turns a repository-relative file path into the package prefix the
// allow-list uses, e.g. "cas/codec/cbor/cbor.go" -> "cas/codec/cbor".
func packagePath(rel string) string {
	dir := filepath.ToSlash(filepath.Dir(rel))
	if dir == "." {
		return "(root)"
	}
	return dir
}

// repoRoot resolves the repository root from this source file, so the check does
// not depend on the working directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test source to resolve the repository root")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// findingsInFile inspects one parsed file's exported declarations.
func findingsInFile(pkg, rel string, fset *token.FileSet, file *ast.File) []finding {
	if file.Name.Name == "main" {
		// Nothing imports a main package, so it has no exported API to keep lean.
		return nil
	}
	var out []finding
	add := func(symbol string, hits []string, pos token.Pos) {
		line := fset.Position(pos).Line
		for _, hit := range hits {
			out = append(out, finding{symbol: symbol, position: hit, file: rel, line: line})
		}
	}

	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if !ast.IsExported(d.Name.Name) {
				continue
			}
			symbol := pkg + "." + d.Name.Name
			if recv := receiverName(d.Recv); recv != "" {
				if !ast.IsExported(recv) {
					continue
				}
				symbol = pkg + "." + recv + "." + d.Name.Name
			}
			var hits []string
			hits = append(hits, fieldsAny(d.Type.Params, "parameter")...)
			hits = append(hits, fieldsAny(d.Type.Results, "result")...)
			add(symbol, hits, d.Pos())

		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if !ast.IsExported(s.Name.Name) {
						continue
					}
					// A type parameter's constraint is Go's constraint syntax,
					// never a value position, so TypeParams is not inspected.
					add(pkg+"."+s.Name.Name, anyPositions(s.Type, typePosition(s.Type)), s.Pos())
				case *ast.ValueSpec:
					if s.Type == nil {
						continue
					}
					for _, name := range s.Names {
						if ast.IsExported(name.Name) {
							add(pkg+"."+name.Name, anyPositions(s.Type, "value"), s.Pos())
						}
					}
				}
			}
		}
	}
	return out
}

// typePosition names the outermost position of a type declaration's value.
func typePosition(expr ast.Expr) string {
	switch expr.(type) {
	case *ast.StructType:
		return "struct"
	case *ast.InterfaceType:
		return "interface"
	default:
		return "type"
	}
}

// anyPositions reports every `any` (or empty `interface{}`) reachable from a
// type expression, each with the position that carried it.
func anyPositions(expr ast.Expr, where string) []string {
	var hits []string
	switch e := expr.(type) {
	case nil:
		return nil
	case *ast.Ident:
		if e.Name == "any" {
			hits = append(hits, where)
		}
	case *ast.InterfaceType:
		if e.Methods == nil || len(e.Methods.List) == 0 {
			hits = append(hits, where)
			break
		}
		for _, method := range e.Methods.List {
			name := fieldNames(method)
			if name != "" && !ast.IsExported(name) {
				continue // an unexported method is not part of the API
			}
			inner := where
			if name != "" {
				inner = where + " method " + name
			}
			hits = append(hits, anyPositions(method.Type, inner)...)
		}
	case *ast.StructType:
		if e.Fields != nil {
			for _, field := range e.Fields.List {
				if !fieldIsAPI(field) {
					continue // an unexported field is not part of the API
				}
				name := fieldNames(field)
				if name == "" {
					name = "embedded"
				}
				hits = append(hits, anyPositions(field.Type, where+" field "+name)...)
			}
		}
	case *ast.MapType:
		hits = append(hits, anyPositions(e.Key, where+" map key")...)
		hits = append(hits, anyPositions(e.Value, where+" map value")...)
	case *ast.ArrayType:
		hits = append(hits, anyPositions(e.Elt, where+" element")...)
	case *ast.StarExpr:
		hits = append(hits, anyPositions(e.X, where+" pointer")...)
	case *ast.ChanType:
		hits = append(hits, anyPositions(e.Value, where+" channel")...)
	case *ast.Ellipsis:
		hits = append(hits, anyPositions(e.Elt, where+" variadic")...)
	case *ast.ParenExpr:
		hits = append(hits, anyPositions(e.X, where)...)
	case *ast.FuncType:
		hits = append(hits, fieldsAny(e.Params, where+" parameter")...)
		hits = append(hits, fieldsAny(e.Results, where+" result")...)
	case *ast.IndexExpr:
		hits = append(hits, anyPositions(e.Index, where+" type argument")...)
	case *ast.IndexListExpr:
		for _, index := range e.Indices {
			hits = append(hits, anyPositions(index, where+" type argument")...)
		}
	}
	return hits
}

// fieldsAny reports the `any` in a parameter or result list, naming each by its
// parameter name when it has one and by position otherwise.
func fieldsAny(list *ast.FieldList, label string) []string {
	if list == nil {
		return nil
	}
	var hits []string
	for i, field := range list.List {
		where := fmt.Sprintf("%s %d", label, i+1)
		if name := fieldNames(field); name != "" {
			where = label + " " + name
		}
		hits = append(hits, anyPositions(field.Type, where)...)
	}
	return hits
}

// fieldNames renders a field's declared names, or "" for an embedded field.
func fieldNames(field *ast.Field) string {
	if len(field.Names) == 0 {
		return ""
	}
	names := make([]string, 0, len(field.Names))
	for _, name := range field.Names {
		names = append(names, name.Name)
	}
	return strings.Join(names, ", ")
}

// fieldIsAPI reports whether a struct field is part of an exported type's API: a
// named field only when its name is exported, an embedded field only when the
// embedded type is. An unexported field carries no API, so its width is the
// package's own business — `cas/repo.Registry`'s unexported store map is the
// case that keeps this honest.
func fieldIsAPI(field *ast.Field) bool {
	if len(field.Names) == 0 {
		return ast.IsExported(typeName(field.Type))
	}
	for _, name := range field.Names {
		if ast.IsExported(name.Name) {
			return true
		}
	}
	return false
}

// receiverName returns a method's receiver type name, unwrapping the pointer and
// generic forms so an exported receiver is recognized.
func receiverName(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return ""
	}
	return typeName(recv.List[0].Type)
}

// typeName names a type expression, or "" when it is not a named type.
func typeName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.StarExpr:
		return typeName(e.X)
	case *ast.IndexExpr:
		return typeName(e.X)
	case *ast.IndexListExpr:
		return typeName(e.X)
	case *ast.ParenExpr:
		return typeName(e.X)
	default:
		return ""
	}
}
