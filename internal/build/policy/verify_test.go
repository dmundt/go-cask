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
// as one, and the fuzz set is one the gate can run.
func TestVerifyTableIsWellFormed(t *testing.T) {
	t.Parallel()

	table := Verify()
	for _, field := range []struct {
		name  string
		value string
	}{
		{"JobsEnv", table.JobsEnv},
		{"ScopeEnv", table.ScopeEnv},
		{"ScopeRule", table.ScopeRule},
		{"FastEnv", table.FastEnv},
		{"SkipTestsEnv", table.SkipTestsEnv},
		{"SkipCoverageEnv", table.SkipCoverageEnv},
		{"SkipFuzzEnv", table.SkipFuzzEnv},
		{"SkipSecurityEnv", table.SkipSecurityEnv},
		{"SkipLintEnv", table.SkipLintEnv},
		{"ReleaseEnv", table.ReleaseEnv},
		{"ReleaseFromEnv", table.ReleaseFromEnv},
	} {
		if field.value == "" {
			t.Errorf("%s is empty", field.name)
		}
	}
	for _, name := range []string{
		table.JobsEnv, table.ScopeEnv, table.FastEnv, table.SkipTestsEnv,
		table.SkipCoverageEnv, table.SkipFuzzEnv, table.SkipSecurityEnv, table.SkipLintEnv,
		table.ReleaseEnv, table.ReleaseFromEnv,
	} {
		if name != strings.ToUpper(name) {
			t.Errorf("%q is not spelled as an environment variable", name)
		}
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
func fuzzTargetDir(target FuzzTarget) string {
	return strings.Trim(strings.TrimPrefix(target.Package, "./"), "/")
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
		dir := fuzzTargetDir(target)
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
		registered[fuzzTargetDir(target)] = true
	}

	var fuzzed []string
	for _, file := range gitList(t, root, "ls-files", "internal/build/*") {
		if !strings.HasSuffix(file, "_test.go") {
			continue
		}
		dir := path.Dir(file)
		// policy holds go-cask's answers rather than a rule the engine ships, so it is not
		// one of the packages this rule is about.
		if dir == "internal/build/policy" {
			continue
		}
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
		Verify().Checks.ModuleGraph, Verify().Checks.Vet, Verify().Checks.Lint,
		Verify().Checks.CrossPlatform,
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

// runnerValue matches one `runs-on:` value in a workflow, at any indentation, stopping at a
// space or a `#` so a trailing comment cannot hide the value. A line whose first non-blank
// character is `#` is a comment, not a declaration, and does not match.
var runnerValue = regexp.MustCompile(`(?m)^[ \t]*runs-on:[ \t]*([^ \t#]+)`)

// nonLinuxRunner matches the hosted runner families go-cask#390 removed. A matrix value
// (`${{ matrix.runs-on }}`) is deliberately unmatched: the values it expands to are literal
// `runs-on:` lines in the same file, which is how the Windows entry was spelled before
// #419, so the check covers that form through the literal it points at.
var nonLinuxRunner = regexp.MustCompile(`^(windows|macos)`)

// TestNoWorkflowUsesAWindowsOrMacOSRunner pins the other half of the platform decision: no
// job spends a Windows or a macOS runner, because those targets are cross-compiled on the
// one Linux runner instead (go-cask#390).
//
// Nothing else would notice. The matrix says `ubuntu-latest` and nothing reads it, and the
// smoke job #390 removed failed for a reason worth remembering: the tests it ran arrange
// POSIX filesystem restrictions Windows does not reproduce, so a hosted runner reappearing
// here is a decision to write down in #390's terms rather than a silent reintroduction of
// a job that cannot mean what it looks like. The workflows are read from the tracked files,
// as this package's other tree-reading tests do, so the answer is the repository's rather
// than the working directory's.
func TestNoWorkflowUsesAWindowsOrMacOSRunner(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	workflows := gitList(t, root, "ls-files", ".github/workflows/*.yml")
	if len(workflows) == 0 {
		t.Fatal("no workflow is tracked, so this test would prove nothing")
	}

	declared := 0
	for _, file := range workflows {
		// A Windows checkout can carry CRLF, and the line anchor above depends on it.
		content := strings.ReplaceAll(readRepoFile(t, root, file), "\r\n", "\n")
		for _, match := range runnerValue.FindAllStringSubmatch(content, -1) {
			declared++
			if nonLinuxRunner.MatchString(match[1]) {
				t.Errorf("%s runs a job on %s: go-cask#390 cross-compiles those platforms on the one Linux runner, so a hosted Windows or macOS runner here is a decision to record, not a default", file, match[1])
			}
		}
	}
	if declared == 0 {
		t.Fatal("no tracked workflow declares a runs-on:, so this test would prove nothing")
	}
}
