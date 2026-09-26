package layers

import (
	"strings"
	"testing"
)

// modulePath is a stand-in for a caller's module path, so the tests do not depend
// on any real module.
const modulePath = "example.com/mod"

// testMatrix is a small table in the shape a caller builds: an arm that owns a
// whole tree, an arm that owns one package, and an arm that owns several trees. The
// toolkit ships no table of its own — a table is a caller's policy — so the
// mechanism is exercised against this.
func testMatrix() []Layer {
	local := func(suffix string) string { return modulePath + suffix }
	return []Layer{
		{Name: "core/", Owns: OwnsTree(local("/core")), Allowed: []string{local("/core")}},
		{Name: "ref/", Owns: OwnsExact(local("/ref")), Allowed: []string{local("/core")}},
		{Name: "app/", Owns: OwnsAnyTree(local("/app"), local("/tool")), Allowed: []string{local("/core"), local("/app")}},
		{Name: "ext/", Owns: OwnsTree(local("/ext")), Allowed: []string{local("/core"), local("/ref")}},
	}
}

// check runs the matrix over packages written as "packagePath|import1 import2",
// which is the shape `go list` prints and the shape a gate passes through.
func check(t *testing.T, specs ...string) []Violation {
	t.Helper()
	packages := make([]Package, 0, len(specs))
	for _, spec := range specs {
		path, imports, _ := strings.Cut(spec, "|")
		pkg := Package{ImportPath: path}
		if imports != "" {
			pkg.Imports = strings.Fields(imports)
		}
		packages = append(packages, pkg)
	}
	return Check(modulePath, testMatrix(), packages)
}

// TestCheckTable pins the mechanism: each arm allows what it lists and refuses what
// it does not, a nested package is owned by its tree's arm, and an import outside
// the module is outside the rule.
func TestCheckTable(t *testing.T) {
	t.Parallel()

	module := modulePath

	tests := []struct {
		name string
		pkg  string
		want int
	}{
		{name: "an arm may import what it allows", pkg: module + "/core/x|" + module + "/core/y"},
		{name: "an arm may import its own tree", pkg: module + "/core/x|" + module + "/core"},
		{name: "an arm may not import a sibling tree", pkg: module + "/core/x|" + module + "/ref", want: 1},
		{name: "an exact arm owns only its own package", pkg: module + "/ref|" + module + "/core"},
		{name: "a package no arm owns is skipped even for a nested path", pkg: module + "/ref/sub|" + module + "/other"},
		{name: "a multi-tree arm owns its first tree", pkg: module + "/app/x|" + module + "/core"},
		{name: "a multi-tree arm owns its second tree", pkg: module + "/tool/x|" + module + "/core"},
		{name: "another arm refuses an import it does not list", pkg: module + "/app/x|" + module + "/ref", want: 1},
		{name: "an arm listing a tree may import it", pkg: module + "/ext/x|" + module + "/ref"},
		{name: "the standard library is outside the rule", pkg: module + "/core/x|context io strings fmt"},
		{name: "a third-party import is outside the rule", pkg: module + "/core/x|golang.org/x/sys/unix"},
		{name: "a package no arm owns is skipped", pkg: "example.com/other/x|" + module + "/core"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := check(t, test.pkg)
			if len(got) != test.want {
				t.Fatalf("Check(%q) found %d violations, want %d: %v", test.pkg, len(got), test.want, got)
			}
		})
	}
}

// TestOwnershipBoundary pins the separator rule the toolkit owns, so a caller's
// table cannot get it subtly wrong: a tree root must not claim a sibling whose name
// merely starts with it.
func TestOwnershipBoundary(t *testing.T) {
	t.Parallel()

	matrix := testMatrix()
	cases := []struct {
		name      string
		pkg       string
		wantLayer string
		wantOwned bool
	}{
		{name: "the tree itself", pkg: modulePath + "/core", wantLayer: "core/", wantOwned: true},
		{name: "beneath the tree", pkg: modulePath + "/core/deep/pkg", wantLayer: "core/", wantOwned: true},
		{name: "a sibling sharing the prefix", pkg: modulePath + "/corey", wantOwned: false},
		{name: "a sibling of a multi-tree arm", pkg: modulePath + "/apple", wantOwned: false},
		{name: "an exact package", pkg: modulePath + "/ref", wantLayer: "ref/", wantOwned: true},
		{name: "beneath an exact arm", pkg: modulePath + "/ref/sub", wantOwned: false},
		{name: "an unowned package", pkg: modulePath + "/nope", wantOwned: false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			layer, owned := Owner(matrix, test.pkg)
			if owned != test.wantOwned {
				t.Fatalf("Owner(%q) owned = %v, want %v", test.pkg, owned, test.wantOwned)
			}
			if owned && layer.Name != test.wantLayer {
				t.Errorf("Owner(%q) layer = %q, want %q", test.pkg, layer.Name, test.wantLayer)
			}
		})
	}
}

// TestCheckIsNotVacuous pins the failure the check exists to catch: a forbidden
// module-local import is reported with the offending package, import and layer.
func TestCheckIsNotVacuous(t *testing.T) {
	t.Parallel()

	got := check(t, modulePath+"/ref|"+modulePath+"/core "+modulePath+"/app")
	if len(got) != 1 {
		t.Fatalf("found %d violations, want 1: %v", len(got), got)
	}
	violation := got[0]
	if violation.Package != modulePath+"/ref" {
		t.Errorf("violation package = %q, want %q", violation.Package, modulePath+"/ref")
	}
	if violation.Import != modulePath+"/app" {
		t.Errorf("violation import = %q, want %q", violation.Import, modulePath+"/app")
	}
	if violation.Layer != "ref/" {
		t.Errorf("violation layer = %q, want %q", violation.Layer, "ref/")
	}
	if !strings.Contains(violation.String(), "may not import") {
		t.Errorf("violation %q does not read as a rule, so a log line would not explain itself", violation.String())
	}
}

// TestCheckSortsForStableLogs pins deterministic output: a gate prints these lines,
// and a set that reorders between runs makes a diff of two runs useless.
func TestCheckSortsForStableLogs(t *testing.T) {
	t.Parallel()

	got := check(t,
		modulePath+"/app/x|"+modulePath+"/ref",
		modulePath+"/ref|"+modulePath+"/app",
	)
	if len(got) != 2 {
		t.Fatalf("found %d violations, want 2: %v", len(got), got)
	}
	if got[0].Package != modulePath+"/app/x" || got[1].Package != modulePath+"/ref" {
		t.Errorf("violations are not sorted by package: %v", got)
	}
}

// TestAllowedPrefixesAreModuleLocal pins that an arm's allowed prefixes begin with
// the module path. If one did not, it could never match a local import and the arm
// would silently refuse everything.
func TestAllowedPrefixesAreModuleLocal(t *testing.T) {
	t.Parallel()

	for _, layer := range testMatrix() {
		if len(layer.Allowed) == 0 {
			t.Errorf("layer %q allows no imports at all", layer.Name)
		}
		for _, prefix := range layer.Allowed {
			if !strings.HasPrefix(prefix, modulePath) {
				t.Errorf("layer %q allows %q, which is not inside the module", layer.Name, prefix)
			}
		}
	}
}
