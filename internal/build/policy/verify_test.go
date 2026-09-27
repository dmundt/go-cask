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

// TestVerifySuiteHasNoDuplicateOrEmptyName pins the list CI requires of a receipt: it is
// asked for by name, so a repeated name would make a receipt look complete that is missing
// another, and an empty one could never be recorded — the gate's own check names are what
// fills it, and the step list is the one owner now that the helper that also listed them is
// Go.
func TestVerifySuiteHasNoDuplicateOrEmptyName(t *testing.T) {
	t.Parallel()

	suite := VerifySuite()
	if len(suite) == 0 {
		t.Fatal("the receipt suite is empty, so no receipt could ever satisfy CI")
	}
	seen := map[string]bool{}
	for _, name := range suite {
		if name == "" {
			t.Error("the receipt suite carries an empty name")
			continue
		}
		if seen[name] {
			t.Errorf("the receipt suite lists %q twice", name)
		}
		seen[name] = true
	}
	// Every name must be one the gate can actually record: the suite is a subset of the
	// check names, and `govulncheck` is deliberately outside it.
	recordable := map[string]bool{}
	for _, name := range strings.Fields(strings.Join([]string{
		Verify().Checks.Gofmt, Verify().Checks.ModTidy, Verify().Checks.Build,
		Verify().Checks.ModuleGraph, Verify().Checks.Vet, Verify().Checks.CrossPlatform,
		Verify().Checks.LayerMatrix, Verify().Checks.CodecGuards, Verify().Checks.Security,
		Verify().Checks.TestRace, Verify().Checks.CoverageTiers, Verify().Checks.FuzzSmoke,
		Verify().Checks.VersionFields, Verify().Checks.DocIntegrity,
		Verify().Checks.PackageGraph, Verify().Checks.WebsiteFooter,
		Verify().Checks.WebsiteExamples,
	}, " ")) {
		recordable[name] = true
	}
	for _, name := range suite {
		if !recordable[name] {
			t.Errorf("the receipt suite requires %q, which is not a name the gate records", name)
		}
	}
}

// platformMatrixJob is the workflow job whose matrix holds the cross-compilation targets
// the gate's cross-platform step must match.
const platformMatrixJob = "platform-matrix"

// jobBoundary matches the line that opens the next job in a workflow: exactly two spaces of
// indentation followed by a non-space. A job's own keys are indented further, so the first
// such line after a job's header is where that job ends.
var jobBoundary = regexp.MustCompile(`(?m)^  \S`)

// matrixGOOS and matrixGOARCH match the two values one matrix entry declares. The leading
// class is spaces and tabs rather than `\s`, so a match cannot walk across a line break.
var (
	matrixGOOS   = regexp.MustCompile(`(?m)^[ \t]+goos:[ \t]*(\S+)[ \t]*$`)
	matrixGOARCH = regexp.MustCompile(`(?m)^[ \t]+goarch:[ \t]*(\S+)[ \t]*$`)
)

// matrixTargets returns the "goos/goarch" pairs a workflow job's matrix declares, sorted.
//
// The workflow is YAML and this repository carries no YAML dependency — one would need the
// coding-guidelines §3 exception process — so this reads the text, the way
// TestScopeRulesMatchTheWorkflow reads it. Every entry declares exactly one goos and one
// goarch, in that order, which is what makes the pairing sound without a real parser; the
// counts are compared first so a half-edited entry is a loud failure rather than a
// mispaired target.
func matrixTargets(t *testing.T, workflow, job string) []string {
	t.Helper()

	// A Windows checkout can carry CRLF, and the anchors below are what makes the pairing
	// sound, so the line endings are normalised rather than guessed at.
	workflow = strings.ReplaceAll(workflow, "\r\n", "\n")

	header := "\n  " + job + ":\n"
	start := strings.Index(workflow, header)
	if start < 0 {
		t.Fatalf("%s declares no %s job", ciWorkflowPath, job)
	}
	block := workflow[start+len(header):]
	if end := jobBoundary.FindStringIndex(block); end != nil {
		block = block[:end[0]]
	}

	gooses := matrixGOOS.FindAllStringSubmatch(block, -1)
	goarches := matrixGOARCH.FindAllStringSubmatch(block, -1)
	if len(gooses) == 0 {
		t.Fatalf("%s: the %s job's matrix declares no goos value", ciWorkflowPath, job)
	}
	if len(gooses) != len(goarches) {
		t.Fatalf("%s: the %s job's matrix declares %d goos and %d goarch values; every entry needs one of each",
			ciWorkflowPath, job, len(gooses), len(goarches))
	}

	targets := make([]string, 0, len(gooses))
	for index := range gooses {
		targets = append(targets, gooses[index][1]+"/"+goarches[index][1])
	}
	sort.Strings(targets)
	return targets
}

// TestPlatformTargetsMatchTheWorkflow pins the cross-compilation target set to its one
// owner, in both directions.
//
// The gate's cross-platform step reads Verify().Platforms; the CI platform-matrix job holds
// the same set because a workflow is YAML and cannot read the table. Neither side fails
// loudly when the two drift — whichever list is not updated simply gates less than the
// other — and the quiet direction is the one that matters: a target the gate cross-builds
// but the matrix never builds is a platform nobody builds in CI at all, while the reverse
// costs a target its local cross-build only. So the pairs are compared here, and a failure
// names the target that just one side gates.
func TestPlatformTargetsMatchTheWorkflow(t *testing.T) {
	t.Parallel()

	table := Verify().Platforms
	if len(table) == 0 {
		t.Fatal("the gate cross-builds no platform, so this test would compare nothing")
	}
	want := make([]string, 0, len(table))
	for _, target := range table {
		want = append(want, target.GOOS+"/"+target.GOARCH)
	}
	sort.Strings(want)

	got := matrixTargets(t, readRepoFile(t, repoRoot(t), ciWorkflowPath), platformMatrixJob)
	for _, target := range want {
		if !slices.Contains(got, target) {
			t.Errorf("the gate cross-builds %s, which the %s job's matrix does not", target, platformMatrixJob)
		}
	}
	for _, target := range got {
		if !slices.Contains(want, target) {
			t.Errorf("the %s job's matrix cross-builds %s, which Verify().Platforms does not gate", platformMatrixJob, target)
		}
	}
}
