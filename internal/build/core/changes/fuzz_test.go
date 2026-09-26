package changes

import (
	"strings"
	"testing"
)

// FuzzPatternMatches checks the one thing a fuzzer can prove about a matcher: it never
// panics on arbitrary input, and whatever it answers agrees with what the pattern
// promises. A path that an exact pattern matches IS that path; a prefix match starts
// with the prefix; a suffix match ends with the suffix.
func FuzzPatternMatches(f *testing.F) {
	f.Add(`docs/index.md`, "docs/", "", "")
	f.Add("mkdocs.yml", "", "", "mkdocs.yml")
	f.Add("cas/store.go", "", "", ".go")
	f.Add("", "", "website/", "")
	f.Add("docs", "docs/", "", "")

	f.Fuzz(func(t *testing.T, path, prefix, suffix, exact string) {
		for _, pattern := range []Pattern{{Prefix: prefix}, {Suffix: suffix}, {Exact: exact}} {
			matched := pattern.Matches(path)
			switch {
			case pattern.Exact != "":
				if matched != (path == pattern.Exact) {
					t.Fatalf("%s.Matches(%q) = %v", pattern, path, matched)
				}
			case pattern.Prefix != "":
				if matched != strings.HasPrefix(path, pattern.Prefix) {
					t.Fatalf("%s.Matches(%q) = %v", pattern, path, matched)
				}
			case pattern.Suffix != "":
				if matched != strings.HasSuffix(path, pattern.Suffix) {
					t.Fatalf("%s.Matches(%q) = %v", pattern, path, matched)
				}
			default:
				// An empty pattern matches nothing, whatever the path is.
				if matched {
					t.Fatalf("an empty pattern matched %q", path)
				}
			}
		}
	})
}

// FuzzClassify checks that a classification never panics and that its two halves are a
// partition of the change set: Select must return every path exactly once, and an All
// rule must hold exactly when nothing was left uncovered.
func FuzzClassify(f *testing.F) {
	f.Add("docs/index.md", "cas/store.go")
	f.Add("", "")
	f.Add("website/x.md", "docs/y.md")

	f.Fuzz(func(t *testing.T, first, second string) {
		paths := []string{first, second}
		rules := []Rule{
			{Name: "docs_only", Mode: All, Paths: []Pattern{{Suffix: ".md"}, {Prefix: "docs/"}}},
			{Name: "go_changed", Mode: Any, Paths: []Pattern{{Suffix: ".go"}}},
		}
		results, err := Classify(paths, rules)
		if err != nil {
			t.Fatalf("Classify rejected its own table: %v", err)
		}
		for _, result := range results {
			if len(result.Matched)+len(result.Unmatched) != len(paths) {
				t.Fatalf("%s split %d paths into %d and %d", result.Name,
					len(paths), len(result.Matched), len(result.Unmatched))
			}
			if result.Name == "docs_only" {
				want := len(paths) > 0 && len(result.Unmatched) == 0
				if result.Holds != want {
					t.Fatalf("docs_only = %v with %d uncovered paths", result.Holds, len(result.Unmatched))
				}
			}
		}
	})
}
