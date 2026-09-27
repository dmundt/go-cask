// Package layers checks a dependency-layer rule: which packages of a module may
// import which.
//
// A module states its own layers — the classic shape is a library chain plus a
// product tree beside it — as a table of arms, where each arm owns a set of
// packages and lists the module-local prefixes its members may import. The check is
// a pure function over the package list Go reports, so a table is covered by
// ordinary tests instead of by running a whole build.
//
// The table is the caller's: this package ships none. Build one with OwnsTree,
// OwnsExact and OwnsAnyTree, which also own the matching semantics — the separator
// rule that keeps a root like `…/cas` from claiming `…/casket`.
//
// Go list's own view is the input, which is the one thing this package cannot
// obtain itself; the caller gathers it and passes the packages in.
package layers

import (
	"fmt"
	"sort"
	"strings"
)

// Layer is one row of the matrix: a group of packages in this module and the
// import prefixes its members may use.
type Layer struct {
	// Name is how the layer is reported.
	Name string
	// Owns reports whether a package in this module belongs to this layer.
	Owns func(packagePath string) bool
	// Allowed are the module-local prefixes a member may import. An import that
	// does not start with one of them is a violation; an import that is not
	// module-local at all (the standard library, a third-party dependency) is
	// outside this rule and never a violation.
	Allowed []string
}

// Package is one package's import data, as `go list -f` reports it.
type Package struct {
	// ImportPath is the package's own path.
	ImportPath string
	// Imports are the import paths it uses. Only production imports appear:
	// go list omits imports that occur solely in _test.go files, so a cas/**
	// test may keep importing internal/test.
	Imports []string
}

// Violation is one import that breaks the matrix.
type Violation struct {
	// Package is the importing package's path.
	Package string
	// Import is the import path that is not allowed.
	Import string
	// Layer is the layer the importing package belongs to.
	Layer string
}

// String renders the violation for a gate log.
func (v Violation) String() string {
	return fmt.Sprintf("%s: %s may not import %s", v.Package, v.Layer, v.Import)
}

// Check returns every import violation in packages, sorted for a stable log.
//
// A package that no layer owns is not a violation here: this check answers
// "may this import use that import", and a package outside the matrix (a new
// top-level tree) is a gap in the matrix rather than an illegal import. The
// matrix arms are kept exhaustive over the module by layers_test.go.
func Check(modulePath string, matrix []Layer, packages []Package) []Violation {
	violations := []Violation{}
	for _, pkg := range packages {
		layer, ok := owner(matrix, pkg.ImportPath)
		if !ok {
			continue
		}
		for _, imp := range pkg.Imports {
			// Only module-local imports are constrained: the standard library
			// and third-party dependencies are outside this rule.
			if !isWithin(imp, modulePath) {
				continue
			}
			if allowed(layer, imp) {
				continue
			}
			violations = append(violations, Violation{
				Package: pkg.ImportPath,
				Import:  imp,
				Layer:   layer.Name,
			})
		}
	}
	sort.Slice(violations, func(i, j int) bool {
		if violations[i].Package != violations[j].Package {
			return violations[i].Package < violations[j].Package
		}
		return violations[i].Import < violations[j].Import
	})
	return violations
}

// Owner returns the layer that claims a package, or false when none does. It is
// exported so the matrix can be checked for exhaustiveness over the module's
// real package list, which is what keeps a new top-level tree from escaping the
// rule by simply not appearing in any arm.
func Owner(matrix []Layer, packagePath string) (Layer, bool) {
	return owner(matrix, packagePath)
}

// owner returns the layer that claims a package, or false when none does.
func owner(matrix []Layer, packagePath string) (Layer, bool) {
	for _, layer := range matrix {
		if layer.Owns(packagePath) {
			return layer, true
		}
	}
	return Layer{}, false
}

// allowed reports whether a module-local import is permitted for a layer.
func allowed(layer Layer, importPath string) bool {
	for _, prefix := range layer.Allowed {
		if isWithin(importPath, prefix) {
			return true
		}
	}
	return false
}

// isWithin reports whether path is root itself or a package beneath it. The
// separator matters: root "…/cas" must not claim "…/casket".
func isWithin(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+"/")
}

// OwnsExact returns a predicate that matches one package path exactly.
//
// These three constructors are how a caller builds the table: the toolkit owns the
// matching semantics — the separator rule that keeps "…/cas" from claiming
// "…/casket" — so a policy table cannot get it subtly wrong.
func OwnsExact(path string) func(string) bool {
	return func(candidate string) bool { return candidate == path }
}

// OwnsTree returns a predicate that matches a package and everything beneath it.
func OwnsTree(root string) func(string) bool {
	return func(candidate string) bool { return isWithin(candidate, root) }
}

// OwnsAnyTree returns a predicate that matches any of several trees.
func OwnsAnyTree(roots ...string) func(string) bool {
	return func(candidate string) bool {
		for _, root := range roots {
			if isWithin(candidate, root) {
				return true
			}
		}
		return false
	}
}
