package policy

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/internal/build/core/changes"
)

// ciWorkflowPath is the workflow whose scope job consumes ScopeRules.
const ciWorkflowPath = ".github/workflows/ci.yml"

// scopeOutput matches a reference to one scope rule's job output.
var scopeOutput = regexp.MustCompile(`steps\.scope\.outputs\.([A-Za-z0-9_]+)`)

// TestScopeRulesAreWellFormed pins the table's own shape: every rule names itself
// once, the table as a whole is one the engine accepts, and the documentation list
// is not empty — an empty pattern list would make every change documentation-only.
func TestScopeRulesAreWellFormed(t *testing.T) {
	t.Parallel()

	rules := ScopeRules()
	if len(rules) == 0 {
		t.Fatal("the scope table is empty")
	}
	if len(DocsPaths()) == 0 || len(WebsitePaths()) == 0 {
		t.Fatal("a pattern list is empty, so the rule it feeds would decide nothing")
	}

	// Classify validates the names, the modes and the implication order; a probe
	// path it accepts proves the table is one the command can read.
	if _, err := changes.Classify([]string{"cas/store.go"}, rules); err != nil {
		t.Fatalf("the scope table is not a table the engine accepts: %v", err)
	}

	if got := ScopeRuleNames(); len(got) != len(rules) {
		t.Errorf("ScopeRuleNames returned %d names for %d rules", len(got), len(rules))
	}
}

// TestScopeRulesMatchTheWorkflow checks the table against its consumer in both
// directions: a rule the workflow never reads decides nothing, and a workflow output
// the table does not define is a job gate that can never be reasoned about.
func TestScopeRulesMatchTheWorkflow(t *testing.T) {
	t.Parallel()

	workflow := readRepoFile(t, repoRoot(t), ciWorkflowPath)
	known := map[string]bool{}
	for _, name := range ScopeRuleNames() {
		known[name] = true
		if !strings.Contains(workflow, "steps.scope.outputs."+name) {
			t.Errorf("%s never reads the %q rule, so the table row decides nothing", ciWorkflowPath, name)
		}
	}
	for _, match := range scopeOutput.FindAllStringSubmatch(workflow, -1) {
		if !known[match[1]] {
			t.Errorf("%s reads scope output %q, which ScopeRules does not define", ciWorkflowPath, match[1])
		}
	}
}

// TestDocsPathsCoverTheDocumentationTrees runs the pattern list against the tracked
// tree rather than against examples: everything the site is built from, the docs/
// tree and every Markdown file must classify as documentation, so a pattern that
// stops covering a real tree fails here.
func TestDocsPathsCoverTheDocumentationTrees(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	groups := [][]string{
		{"website"},
		{"docs"},
		{"*.md"},
		{"mkdocs.yml", "requirements-docs.txt", "requirements-docs.lock"},
	}
	for _, group := range groups {
		args := append([]string{"ls-files"}, group...)
		tracked := gitList(t, root, args...)
		if len(tracked) == 0 {
			t.Errorf("git ls-files %s listed nothing, so this test would prove nothing", strings.Join(group, " "))
			continue
		}
		covered, uncovered := changes.Select(tracked, DocsPaths())
		if len(uncovered) != 0 {
			t.Errorf("docs-only patterns miss %d tracked path(s) under %s: %s",
				len(uncovered), strings.Join(group, " "), strings.Join(uncovered, ", "))
		}
		if len(covered) != len(tracked) {
			t.Errorf("%s: %d of %d paths classified as documentation", strings.Join(group, " "), len(covered), len(tracked))
		}
	}
}

// gitList runs one `git ls-files` in the repository root and returns its lines.
func gitList(t *testing.T, root string, args ...string) []string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSuffix(string(out), "\n"), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
