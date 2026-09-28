// Package deps holds two rules about what a package may depend on: a forbidden
// transitive dependency, and a well-formed module graph.
//
// Both were once inline shell in a gate: a `go list -deps | grep` pair for a
// dependency guard, and a `grep` for a well-formed module graph. A `grep` over
// command output is a rule with its reasoning in a comment and its behaviour in no
// test at all — the shape a thin gate exists to avoid.
//
// A module states its own guards and its own forbidden prefix; this package ships
// none. It turns them into violations, and checks that a module graph names the
// module it was read for.
package deps

import (
	"fmt"
	"sort"
	"strings"
)

// ForbiddenPath is the module-relative prefix the dependency guards refuse by
// default: a dependency on it, or on any package beneath it, is a violation. A
// caller that checks a different boundary changes this one constant.
const ForbiddenPath = "cas/codec"

// CodecGuard is one rule: a package that must not depend on the forbidden prefix,
// and why.
type CodecGuard struct {
	// Package is the package under test, e.g. "./gitlike".
	Package string
	// Remedy is what a reader should do instead, quoted in the failure.
	Remedy string
}

// CodecViolation is one guarded package that depends on the codec layer.
type CodecViolation struct {
	// Package is the guarded package.
	Package string
	// Dependency is the codec package it reaches.
	Dependency string
	// Remedy is what to do instead.
	Remedy string
}

// String renders the violation for a gate log.
func (v CodecViolation) String() string {
	return fmt.Sprintf("%s must not depend on the codec layer (%s); %s", v.Package, v.Dependency, v.Remedy)
}

// CheckCodecDeps reports every guarded package that reaches the codec layer.
//
// guards is the caller's table — this package ships none. deps maps a guarded
// package to the transitive dependency list `go list -deps` reports for it, in
// module-relative form so the rule does not depend on the module path. A dependency
// matches when it is the forbidden layer itself or a package beneath it — a prefix
// match on a path boundary, so `cas/codecbase` does not match `cas/codec`.
func CheckCodecDeps(guards []CodecGuard, deps map[string][]string) []CodecViolation {
	violations := []CodecViolation{}
	for _, guard := range guards {
		for _, dependency := range deps[guard.Package] {
			if !IsForbidden(dependency) {
				continue
			}
			violations = append(violations, CodecViolation{
				Package:    guard.Package,
				Dependency: dependency,
				Remedy:     guard.Remedy,
			})
			break
		}
	}
	sort.Slice(violations, func(i, j int) bool { return violations[i].Package < violations[j].Package })
	return violations
}

// IsForbidden reports whether a module-relative import path is the forbidden layer
// or a package inside it.
func IsForbidden(path string) bool {
	return path == ForbiddenPath || strings.HasPrefix(path, ForbiddenPath+"/")
}

// CheckModuleGraph reports whether `go list -m -json all` produced a graph whose
// main module is the expected one.
//
// The gate writes that command's output to a file and reads it back, so an empty
// or malformed graph — a failed `go list`, a broken go.mod — would otherwise be
// read as "no modules to check" and pass silently.
//
// The check requires the main module, not merely the path: `go list -m -json all`
// prints one object per module and a dependency could carry the same path, so a
// bare containment test would accept a graph in which this module is required
// rather than built. The main module is the object whose `"Path"` is followed on
// the next line by `"Main": true` — the shape `go list -m -json` emits, and one
// that cannot be produced by a dependency entry, which carries a version instead.
func CheckModuleGraph(modulePath, graph string) error {
	if strings.TrimSpace(graph) == "" {
		return fmt.Errorf("the module graph is empty; inspect `go list -m -json all`")
	}
	main := fmt.Sprintf("%q: %q,\n\t\"Main\": true", "Path", modulePath)
	if !strings.Contains(graph, main) {
		return fmt.Errorf("the module graph does not name %s as the main module; inspect `go list -m -json all`", modulePath)
	}
	return nil
}
