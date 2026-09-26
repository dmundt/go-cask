package policy

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

// TestVerifyTableIsWellFormed pins the gate table's own shape: every variable is spelled
// as one, the nested module really is a module, and the fuzz set is one the gate can run.
func TestVerifyTableIsWellFormed(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	table := Verify()
	for _, field := range []struct {
		name  string
		value string
	}{
		{"EngineDir", table.EngineDir},
		{"JobsEnv", table.JobsEnv},
		{"ScopeEnv", table.ScopeEnv},
		{"ScopeRule", table.ScopeRule},
		{"FastEnv", table.FastEnv},
		{"SkipTestsEnv", table.SkipTestsEnv},
		{"SkipCoverageEnv", table.SkipCoverageEnv},
		{"SkipFuzzEnv", table.SkipFuzzEnv},
		{"SkipSecurityEnv", table.SkipSecurityEnv},
		{"ReleaseEnv", table.ReleaseEnv},
		{"ReleaseFromEnv", table.ReleaseFromEnv},
	} {
		if field.value == "" {
			t.Errorf("%s is empty", field.name)
		}
	}
	for _, name := range []string{
		table.JobsEnv, table.ScopeEnv, table.FastEnv, table.SkipTestsEnv,
		table.SkipCoverageEnv, table.SkipFuzzEnv, table.SkipSecurityEnv,
		table.ReleaseEnv, table.ReleaseFromEnv,
	} {
		if name != strings.ToUpper(name) {
			t.Errorf("%q is not spelled as an environment variable", name)
		}
	}

	// The nested module is the whole reason the gate names a directory explicitly, so it
	// has to be one: without its own go.mod the engine steps would build the root module
	// twice and the engine would run nowhere.
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(table.EngineDir), "go.mod")); err != nil {
		t.Errorf("%s carries no go.mod: %v", table.EngineDir, err)
	}

	// The automatic scope reads one rule's verdict, so that rule must exist: a name
	// nothing defines would leave the gate silently at the full scope for ever.
	if !slices.Contains(ScopeRuleNames(), table.ScopeRule) {
		t.Errorf("the gate's scope rule %q is not in the scope table %v", table.ScopeRule, ScopeRuleNames())
	}

	// CGO needs a compiler to be named at all, and the fuzz set is what the smoke step
	// iterates: an empty one would make that step a heading with nothing under it.
	if len(table.Compilers) == 0 {
		t.Error("no C compiler is named, so the race and coverage steps could never run")
	}
	if len(table.Fuzz) == 0 {
		t.Fatal("the smoke-fuzz set is empty")
	}
	for _, target := range table.Fuzz {
		if !strings.HasPrefix(target.Package, "./") || !strings.HasSuffix(target.Package, "/") {
			t.Errorf("fuzz target %s names the package %q, want a ./ prefix and a trailing /", target.Target, target.Package)
		}
		if !strings.HasPrefix(target.Target, "Fuzz") {
			t.Errorf("the fuzz target %q is not a fuzz function's name", target.Target)
		}
	}
}

// fuzzFunc matches a fuzz function's declaration, whatever the parameter is called.
var fuzzFunc = regexp.MustCompile(`^func (Fuzz\w+)\([A-Za-z_][A-Za-z0-9_]* \*testing\.F\) \{`)

// fuzzTargetsIn returns the fuzz functions a package's test files declare, read from the
// tracked files so the answer is the repository's rather than the working directory's.
func fuzzTargetsIn(t *testing.T, root, dir string) []string {
	t.Helper()

	var targets []string
	for _, file := range gitList(t, root, "ls-files", dir+"/*_test.go") {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, line := range strings.Split(string(content), "\n") {
			if match := fuzzFunc.FindStringSubmatch(line); match != nil {
				targets = append(targets, match[1])
			}
		}
	}
	sort.Strings(targets)
	return targets
}

// fuzzTargetDir returns the repository-relative directory a smoke-fuzz target lives in.
func fuzzTargetDir(table VerifyTable, target FuzzTarget) string {
	dir := strings.Trim(strings.TrimPrefix(target.Package, "./"), "/")
	if target.Engine {
		return path.Join(table.EngineDir, dir)
	}
	return dir
}

// TestVerifyFuzzTargetsExist pins that every target the gate smoke-fuzzes is really there.
// A renamed or moved fuzz function would otherwise turn a five-second step into a failed
// gate long after the rename, and the failure would name a package rather than the table
// entry that went stale.
func TestVerifyFuzzTargetsExist(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	table := Verify()
	for _, target := range table.Fuzz {
		dir := fuzzTargetDir(table, target)
		if !slices.Contains(fuzzTargetsIn(t, root, dir), target.Target) {
			t.Errorf("the gate smoke-fuzzes %s in %s, which declares no such fuzz function", target.Target, dir)
		}
	}
}

// TestEveryEngineFuzzPackageIsSmokeFuzzed pins the rule internal/build/AGENT.md states:
// an engine package that parses what the repository and its tools hand it carries a fuzz
// target, and the gate runs one of them. Without this, a new engine parser could be fuzzed
// by nothing at all — or by a corpus nothing runs — while every test passed.
func TestEveryEngineFuzzPackageIsSmokeFuzzed(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	table := Verify()
	registered := map[string]bool{}
	for _, target := range table.Fuzz {
		if target.Engine {
			registered[fuzzTargetDir(table, target)] = true
		}
	}

	var fuzzed []string
	for _, file := range gitList(t, root, "ls-files", table.EngineDir+"/*") {
		if !strings.HasSuffix(file, "_test.go") {
			continue
		}
		dir := path.Dir(file)
		if !slices.Contains(fuzzed, dir) && len(fuzzTargetsIn(t, root, dir)) != 0 {
			fuzzed = append(fuzzed, dir)
		}
	}
	sort.Strings(fuzzed)

	for _, dir := range fuzzed {
		if !registered[dir] {
			t.Errorf("%s carries a fuzz target the gate never smoke-fuzzes; add one to policy.Verify().Fuzz", dir)
		}
	}
	if len(fuzzed) == 0 {
		t.Fatal("no engine package declares a fuzz target, which cannot be right")
	}
}

// receiptSuite reads the check names the receipt helper's `suite_full` lists, so the
// script's own idea of the required checks can be compared with the table the gate records
// them from. The helper is still shell — it signs the receipt, which is the part that has
// to stay where the signing key is — and this pins the half of the pair that would
// otherwise drift silently.
func receiptSuite(t *testing.T, root string) []string {
	t.Helper()

	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(Verify().ReceiptScript)))
	if err != nil {
		t.Fatalf("read %s: %v", Verify().ReceiptScript, err)
	}
	source := string(content)
	start := strings.Index(source, "suite_full=(")
	if start < 0 {
		t.Fatalf("%s no longer declares suite_full", Verify().ReceiptScript)
	}
	block := source[start:]
	if end := strings.Index(block, ")"); end >= 0 {
		block = block[:end]
	}

	var suite []string
	for _, line := range strings.Split(block, "\n")[1:] {
		if name := strings.TrimSpace(line); name != "" {
			suite = append(suite, name)
		}
	}
	sort.Strings(suite)
	return suite
}

// TestVerifyChecksMatchTheReceiptSuite pins the two places the check names live against
// each other: the table the gate records them from, and `suite_full`, which is what CI
// requires of a receipt before it may skip its own run. A name that drifts costs a whole CI
// run rather than a missed check — the safe direction, and the reason it could go
// unnoticed for a long time.
func TestVerifyChecksMatchTheReceiptSuite(t *testing.T) {
	t.Parallel()

	want := VerifySuite()
	sort.Strings(want)
	if got := receiptSuite(t, repoRoot(t)); !slices.Equal(got, want) {
		t.Errorf("%s lists\n  %v\nbut the gate records\n  %v", Verify().ReceiptScript, got, want)
	}
}
