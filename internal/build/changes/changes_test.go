package changes

import (
	"strings"
	"testing"
)

// docsPatterns is a pattern list shaped like a real site's, so the rules below are
// exercised on values they do not own.
func docsPatterns() []Pattern {
	return []Pattern{
		{Suffix: ".md"},
		{Prefix: "docs/"},
		{Prefix: "website/"},
		{Exact: "mkdocs.yml"},
	}
}

func TestPatternMatches(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		pattern Pattern
		path    string
		want    bool
	}{
		{"a suffix matches at any depth", Pattern{Suffix: ".md"}, "cas/README.md", true},
		{"a suffix matches a bare name", Pattern{Suffix: ".md"}, "README.md", true},
		{"a suffix does not match another extension", Pattern{Suffix: ".md"}, "README.mdx", false},
		{"a prefix needs its separator", Pattern{Prefix: "docs/"}, "docs-archive/notes.md", false},
		{"a prefix does not match the directory itself", Pattern{Prefix: "docs/"}, "docs", false},
		{"an exact pattern is the whole path", Pattern{Exact: "mkdocs.yml"}, "mkdocs.yml", true},
		{"an exact pattern is not a suffix", Pattern{Exact: "mkdocs.yml"}, "site/mkdocs.yml", false},
		{"an empty pattern matches nothing", Pattern{}, "anything", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.pattern.Matches(tc.path); got != tc.want {
				t.Errorf("%s.Matches(%q) = %v, want %v", tc.pattern, tc.path, got, tc.want)
			}
		})
	}
}

func TestPatternString(t *testing.T) {
	t.Parallel()

	cases := []struct {
		pattern Pattern
		want    string
	}{
		{Pattern{Exact: "go.mod"}, "go.mod"},
		{Pattern{Prefix: "docs/"}, "docs/*"},
		{Pattern{Suffix: ".go"}, "*.go"},
		{Pattern{}, "(empty pattern)"},
	}
	for _, tc := range cases {
		if got := tc.pattern.String(); got != tc.want {
			t.Errorf("%+v.String() = %q, want %q", tc.pattern, got, tc.want)
		}
	}
}

func TestClassifyAnyAndAll(t *testing.T) {
	t.Parallel()

	rules := []Rule{
		{Name: "docs_only", Mode: All, Paths: docsPatterns()},
		{Name: "go_changed", Mode: Any, Paths: []Pattern{{Suffix: ".go"}, {Exact: "go.mod"}}},
	}

	// One source file makes go_changed hold and docs_only fail.
	results, err := Classify([]string{"docs/index.md", "cas/store.go"}, rules)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("Classify returned %d results, want 2", len(results))
	}
	if results[0].Holds {
		t.Error("docs_only held for a change that touches Go source")
	}
	if len(results[0].Unmatched) != 1 || results[0].Unmatched[0] != "cas/store.go" {
		t.Errorf("docs_only unmatched = %q, want the Go path", results[0].Unmatched)
	}
	if !results[1].Holds {
		t.Error("go_changed did not hold for a change that touches Go source")
	}

	// An empty change set holds for neither: a gate must not read "nothing
	// changed" as "documentation only" and shrink what it verifies.
	results, err = Classify(nil, rules)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	for _, result := range results {
		if result.Holds {
			t.Errorf("%s held for an empty change set", result.Name)
		}
	}

	// Documentation alone holds for docs_only.
	results, err = Classify([]string{"docs/index.md", "website/index.md"}, rules)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if !results[0].Holds {
		t.Errorf("docs_only did not hold for documentation: %q", results[0].Unmatched)
	}
	if results[1].Holds {
		t.Error("go_changed held for a documentation-only change")
	}
}

// TestClassifyAlso pins the implication: a rule that names an earlier rule holds
// whenever that rule does, without restating its patterns.
func TestClassifyAlso(t *testing.T) {
	t.Parallel()

	rules := []Rule{
		{Name: "go_changed", Mode: Any, Paths: []Pattern{{Suffix: ".go"}}},
		{Name: "security_changed", Mode: Any, Paths: []Pattern{{Exact: ".github/workflows/ci.yml"}}, Also: []string{"go_changed"}},
	}

	results, err := Classify([]string{"cas/store.go"}, rules)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if !results[1].Holds {
		t.Error("security_changed did not inherit go_changed")
	}

	results, err = Classify([]string{"README.md"}, rules)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if results[1].Holds {
		t.Error("security_changed held for an unrelated path")
	}
}

func TestClassifyRejectsBadTables(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		rules []Rule
		want  string
	}{
		{
			name:  "a nameless rule",
			rules: []Rule{{Mode: Any, Paths: []Pattern{{Suffix: ".go"}}}},
			want:  "names nothing",
		},
		{
			name: "a duplicate name",
			rules: []Rule{
				{Name: "go_changed", Mode: Any, Paths: []Pattern{{Suffix: ".go"}}},
				{Name: "go_changed", Mode: Any, Paths: []Pattern{{Suffix: ".md"}}},
			},
			want: "declared twice",
		},
		{
			name: "an implication of a later rule",
			rules: []Rule{
				{Name: "security_changed", Mode: Any, Also: []string{"go_changed"}},
				{Name: "go_changed", Mode: Any, Paths: []Pattern{{Suffix: ".go"}}},
			},
			want: "not declared before it",
		},
		{
			name:  "an unknown mode",
			rules: []Rule{{Name: "docs_only", Mode: Mode(9), Paths: docsPatterns()}},
			want:  "is not Any or All",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Classify([]string{"a.md"}, tc.rules)
			if err == nil {
				t.Fatalf("Classify accepted a table it should reject")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Classify error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestSelect(t *testing.T) {
	t.Parallel()

	covered, uncovered := Select([]string{"a.md", "cas/store.go", "website/x.md"}, docsPatterns())
	if strings.Join(covered, ",") != "a.md,website/x.md" {
		t.Errorf("covered = %q, want the two documentation paths in order", covered)
	}
	if strings.Join(uncovered, ",") != "cas/store.go" {
		t.Errorf("uncovered = %q, want the Go path", uncovered)
	}
}
