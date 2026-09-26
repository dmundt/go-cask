// Package changes classifies a set of changed paths, which is how a gate decides
// that a branch touched documentation only and how CI decides which of its jobs the
// change can affect.
//
// The patterns and the rules are the caller's: a repository states which paths count
// as documentation, which paths are Go source, and which of those facts imply
// another. This package decides only whether a change set matches them, so the same
// table can classify a gate's working tree and a CI job's pushed range.
package changes

import (
	"fmt"
	"strings"
)

// Pattern matches a changed path. Exactly one field is set per pattern, so a table
// reads as the list of shapes a caller cares about.
type Pattern struct {
	// Exact matches the whole repository-relative path, e.g. "mkdocs.yml".
	Exact string
	// Prefix matches a path inside a directory, e.g. "website/". The separator is
	// part of the prefix, so "docs/" does not match "docs-archive/notes.md".
	Prefix string
	// Suffix matches the end of a path, e.g. ".md", in whatever directory it sits.
	Suffix string
}

// Matches reports whether the pattern covers a path.
func (p Pattern) Matches(path string) bool {
	switch {
	case p.Exact != "":
		return path == p.Exact
	case p.Prefix != "":
		return strings.HasPrefix(path, p.Prefix)
	case p.Suffix != "":
		return strings.HasSuffix(path, p.Suffix)
	default:
		// A pattern with no field set would match everything, which is never what
		// a caller means; it matches nothing instead.
		return false
	}
}

// String renders a pattern the way a finding names it.
func (p Pattern) String() string {
	switch {
	case p.Exact != "":
		return p.Exact
	case p.Prefix != "":
		return p.Prefix + "*"
	case p.Suffix != "":
		return "*" + p.Suffix
	default:
		return "(empty pattern)"
	}
}

// matchesAny reports whether any pattern covers the path.
func matchesAny(path string, patterns []Pattern) bool {
	for _, pattern := range patterns {
		if pattern.Matches(path) {
			return true
		}
	}
	return false
}

// Mode says how a rule reads a change set.
type Mode int

const (
	// Any holds when at least one changed path matches: "this change touches Go
	// source".
	Any Mode = iota
	// All holds when the change set is not empty and every changed path matches:
	// "this change is documentation only". An empty change set holds for no rule —
	// a gate that treated "nothing changed" as documentation-only would silently
	// shrink what it verifies.
	All
)

// Rule is one named classification: the paths that decide it, and the names of
// earlier rules whose holding decides it as well. `Also` is what keeps a rule such
// as "a security-relevant change is one that touches Go or the continuous
// integration configuration" from restating the Go patterns.
type Rule struct {
	// Name is the rule's identifier, in the form a caller reports it, e.g.
	// "docs_only".
	Name string
	// Mode says whether one matching path decides the rule or every path must.
	Mode Mode
	// Paths are the patterns the rule matches paths against.
	Paths []Pattern
	// Also names earlier rules that imply this one.
	Also []string
}

// Result is one rule's verdict on a change set.
type Result struct {
	// Name is the rule the verdict belongs to.
	Name string
	// Holds is the verdict.
	Holds bool
	// Matched is the paths the rule's patterns covered, in the order given.
	Matched []string
	// Unmatched is the paths the rule's patterns did not cover, in the order
	// given. For an All rule it is why the rule does not hold.
	Unmatched []string
}

// Classify evaluates every rule against the change set and returns one result per
// rule, in the order the rules were given.
//
// A name may be declared once, and `Also` may name only a rule declared before it,
// so the verdicts cannot be circular and a reader can read the table top to bottom.
func Classify(paths []string, rules []Rule) ([]Result, error) {
	seen := map[string]bool{}
	holds := map[string]bool{}

	results := make([]Result, 0, len(rules))
	for _, rule := range rules {
		if rule.Name == "" {
			return nil, fmt.Errorf("rule %d names nothing", len(results))
		}
		if seen[rule.Name] {
			return nil, fmt.Errorf("rule %q is declared twice", rule.Name)
		}
		var implied bool
		for _, name := range rule.Also {
			if !seen[name] {
				return nil, fmt.Errorf("rule %q is implied by %q, which is not declared before it", rule.Name, name)
			}
			implied = implied || holds[name]
		}

		result := Result{Name: rule.Name}
		result.Matched, result.Unmatched = Select(paths, rule.Paths)
		switch rule.Mode {
		case Any:
			result.Holds = len(result.Matched) > 0
		case All:
			result.Holds = len(paths) > 0 && len(result.Unmatched) == 0
		default:
			return nil, fmt.Errorf("rule %q has mode %d, which is not Any or All", rule.Name, rule.Mode)
		}
		result.Holds = result.Holds || implied

		seen[rule.Name] = true
		holds[rule.Name] = result.Holds
		results = append(results, result)
	}
	return results, nil
}

// Select splits the paths into the ones the patterns cover and the ones they do not,
// each in the order given: the halves a caller reports, and the halves an All rule
// decides on.
func Select(paths []string, patterns []Pattern) (covered, uncovered []string) {
	for _, path := range paths {
		if matchesAny(path, patterns) {
			covered = append(covered, path)
		} else {
			uncovered = append(uncovered, path)
		}
	}
	return covered, uncovered
}
