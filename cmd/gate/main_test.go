package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/internal/build/policy"
)

// sourceFixture is a throwaway repository the version and Markdown-list tests ask their
// questions of: one commit holding a versioned Markdown file at the root, a second one in a
// subdirectory, and a file the version rule does not apply to.
//
// It is a real repository because the answers under test come from Git — `ls-files` and the
// three diffs that are a change — and a fake would pin the test's idea of Git rather than
// Git's.
type sourceFixture struct {
	root  string
	git   func(args ...string) string
	write func(rel, content string)
}

// versionedDocument renders a Markdown document carrying the frontmatter version the rule
// reads, so a test can edit one and leave its version where it was.
func versionedDocument(body string) string {
	return "---\ntype: Guide\ntitle: x\ndescription: y\nversion: v1\n---\n\n# " + body + "\n"
}

func newSourceFixture(t *testing.T) *sourceFixture {
	t.Helper()
	root := t.TempDir()

	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), root, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("creating %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("writing %s: %v", rel, err)
		}
	}

	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	write("README.md", versionedDocument("the root document"))
	write("sub/inner.md", versionedDocument("a document in a subdirectory"))
	write("main.go", "package main\n")
	run("add", "README.md", "sub/inner.md", "main.go")
	run("commit", "-qm", "seed")

	return &sourceFixture{root: root, git: run, write: write}
}

// TestMarkdownFilesAnswersForTheRepositoryRoot pins the scope of the tracked Markdown list: it
// is the repository's, not the directory the caller stands in. Asked from a subdirectory the
// listing held that directory's files alone — a confident empty answer for a consumer that
// then checked nothing (go-cask#495).
func TestMarkdownFilesAnswersForTheRepositoryRoot(t *testing.T) {
	fixture := newSourceFixture(t)
	want := []string{"README.md", "sub/inner.md"}

	fromRoot, err := markdownFiles(fixture.root)
	if err != nil {
		t.Fatalf("markdownFiles from the root: %v", err)
	}
	if strings.Join(fromRoot, ",") != strings.Join(want, ",") {
		t.Fatalf("markdownFiles from the root = %q, want %q", fromRoot, want)
	}

	t.Chdir(filepath.Join(fixture.root, "sub"))
	fromSub, err := markdownFiles(fixture.root)
	if err != nil {
		t.Fatalf("markdownFiles from a subdirectory: %v", err)
	}
	if strings.Join(fromSub, ",") != strings.Join(want, ",") {
		t.Errorf("markdownFiles from a subdirectory = %q, want the whole repository %q", fromSub, want)
	}
}

// TestVersionFieldsFromASubdirectorySeesTheWholeChange pins the symptom #495 was reported for:
// run from a subdirectory the command answered with that directory's files, so a change that
// broke the rule at the repository root was reported as clean, exit 0.
func TestVersionFieldsFromASubdirectorySeesTheWholeChange(t *testing.T) {
	fixture := newSourceFixture(t)
	base := fixture.git("rev-parse", "HEAD")
	fixture.write("README.md", versionedDocument("the root document, edited with no bump"))
	t.Chdir(filepath.Join(fixture.root, "sub"))

	var out, errOut bytes.Buffer
	if err := run([]string{"version-fields", "--base", base, "--all"}, &out, &errOut); err == nil {
		t.Fatal("a versioned file edited with no bump was accepted when the caller stood in a subdirectory")
	}
	if !strings.Contains(out.String(), "README.md") {
		t.Errorf("the finding printed %q, want the root document named", out.String())
	}
}

// TestVersionFieldsAllJudgesOnlyWhatTheChangeTouched pins --all's meaning: the tracked
// versioned files the change against --base touched. It used to mean every tracked file, so a
// change that touched no versioned file failed on files byte-identical to the base, which is
// not a rule any of them broke (go-cask#496).
func TestVersionFieldsAllJudgesOnlyWhatTheChangeTouched(t *testing.T) {
	fixture := newSourceFixture(t)
	base := fixture.git("rev-parse", "HEAD")
	t.Chdir(fixture.root)

	// A committed change touching no versioned file: nothing to report, and no failure.
	fixture.write("main.go", "package main\n\nfunc main() {}\n")
	fixture.git("add", "main.go")
	fixture.git("commit", "-qm", "touch only the Go file")

	var out, errOut bytes.Buffer
	if err := run([]string{"version-fields", "--base", base, "--all"}, &out, &errOut); err != nil {
		t.Fatalf("--all over a change that touched no versioned file = %v, want no finding\n%s", err, errOut.String())
	}
	if out.Len() != 0 {
		t.Errorf("--all over a change that touched no versioned file printed %q, want nothing", out.String())
	}

	// The same change with a versioned file edited and its version left where it was: the
	// mode still judges it, so narrowed is not the same as blind.
	fixture.write("sub/inner.md", versionedDocument("edited with no bump"))
	out.Reset()
	errOut.Reset()
	if err := run([]string{"version-fields", "--base", base, "--all"}, &out, &errOut); err == nil {
		t.Fatal("--all accepted an edited versioned file that did not move its version")
	}
	if !strings.Contains(out.String(), "sub/inner.md") {
		t.Errorf("the finding printed %q, want the edited file named", out.String())
	}
}

// TestWriteTargetList pins the form of the table the gate measures from, because
// getting it wrong fails silently rather than loudly: a package without the "./"
// prefix makes `go test -race -cover` fail with "is not in std", and the gate's
// loop then reported no coverage for it. That is exactly the shape of bug this
// test exists to catch.
func TestWriteTargetList(t *testing.T) {
	t.Parallel()

	policy := policy.Coverage()
	var out bytes.Buffer
	if err := writeTargetList(&out, policy); err != nil {
		t.Fatalf("writeTargetList: %v", err)
	}

	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != len(policy.Gated()) {
		t.Fatalf("wrote %d lines for %d gated targets", len(lines), len(policy.Gated()))
	}

	for i, line := range lines {
		fields := strings.Split(line, "|")
		if len(fields) != 3 {
			t.Fatalf("line %d is %q, want three |-separated fields", i+1, line)
		}
		threshold, pkg, tier := fields[0], fields[1], fields[2]

		value, err := strconv.ParseFloat(threshold, 64)
		if err != nil {
			t.Errorf("line %d threshold %q is not a number: %v", i+1, threshold, err)
		}
		if value <= 0 || value > 100 {
			t.Errorf("line %d threshold %q is not a percentage", i+1, threshold)
		}
		// The prefix the gate's `go test` invocation depends on.
		if !strings.HasPrefix(pkg, "./") {
			t.Errorf("line %d package %q lacks the ./ prefix `go test` needs", i+1, pkg)
		}
		if strings.TrimPrefix(pkg, "./") == "" {
			t.Errorf("line %d package %q names nothing", i+1, pkg)
		}
		if tier == "" {
			t.Errorf("line %d names no tier", i+1)
		}
	}
}

// TestTargetListMatchesPolicy pins that the printed table is the policy, so a
// caller cannot end up measuring a different set from the one the drift check
// validated.
func TestTargetListMatchesPolicy(t *testing.T) {
	t.Parallel()

	policy := policy.Coverage()
	var out bytes.Buffer
	if err := writeTargetList(&out, policy); err != nil {
		t.Fatalf("writeTargetList: %v", err)
	}

	printed := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n") {
		fields := strings.Split(line, "|")
		printed[strings.TrimPrefix(fields[1], "./")] = true
	}
	for _, target := range policy.Gated() {
		if !printed[target.Package] {
			t.Errorf("gated package %s is missing from the printed list", target.Package)
		}
	}
	if len(printed) != len(policy.Gated()) {
		t.Errorf("printed %d distinct packages for %d gated targets", len(printed), len(policy.Gated()))
	}
}

// TestRunRejectsBadInvocations pins the command contract: a caller that gets the
// invocation wrong must fail loudly with nothing on stdout, so the gate never
// reads a scope or a table out of a run that did not succeed.
func TestRunRejectsBadInvocations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{name: "no command", args: nil},
		{name: "unknown command", args: []string{"nope"}},
		{name: "layer-matrix with an unknown flag", args: []string{"layer-matrix", "--nope"}},
		{name: "coverage-tier with an unknown flag", args: []string{"coverage-tier", "--nope"}},
		{name: "layer-matrix with a stray argument", args: []string{"layer-matrix", "extra"}},
		{name: "coverage-tier with a stray argument", args: []string{"coverage-tier", "extra"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var out, errOut bytes.Buffer
			if err := run(test.args, &out, &errOut); err == nil {
				t.Errorf("run(%q) succeeded, want an error", test.args)
			}
			if out.Len() != 0 {
				t.Errorf("run(%q) wrote %q to stdout despite failing", test.args, out.String())
			}
		})
	}
}

// TestHelpSucceeds pins that help is not an error: a help flag that exits
// non-zero reads as a broken tool.
func TestHelpSucceeds(t *testing.T) {
	t.Parallel()

	for _, arg := range []string{"-h", "--help", "help"} {
		t.Run(arg, func(t *testing.T) {
			t.Parallel()
			var out, errOut bytes.Buffer
			if err := run([]string{arg}, &out, &errOut); err != nil {
				t.Fatalf("run(%q) returned %v, want nil", arg, err)
			}
			if !strings.Contains(out.String(), "layer-matrix") {
				t.Errorf("run(%q) printed %q, which does not describe the commands", arg, out.String())
			}
		})
	}
}

// TestParsePackages pins the `go list -f` parsing: a malformed line must be an
// error rather than a package with an empty path, which no layer owns and which
// would therefore be checked by nothing.
func TestParsePackages(t *testing.T) {
	t.Parallel()

	packages, err := parsePackages("example.com/mod/cas|context io\nexample.com/mod/cas/backend|example.com/mod/cas\n")
	if err != nil {
		t.Fatalf("parsePackages: %v", err)
	}
	if len(packages) != 2 {
		t.Fatalf("parsed %d packages, want 2", len(packages))
	}
	if packages[0].ImportPath != "example.com/mod/cas" {
		t.Errorf("first package = %q", packages[0].ImportPath)
	}
	if len(packages[0].Imports) != 2 || packages[0].Imports[0] != "context" {
		t.Errorf("first package imports = %q, want [context io]", packages[0].Imports)
	}
	if packages[1].Imports[0] != "example.com/mod/cas" {
		t.Errorf("second package imports = %q", packages[1].Imports)
	}

	// A package with no imports is the common case and must parse, not fail.
	empty, err := parsePackages("example.com/mod/benchmarks|\n")
	if err != nil {
		t.Fatalf("parsePackages with no imports: %v", err)
	}
	if len(empty) != 1 || len(empty[0].Imports) != 0 {
		t.Errorf("package with no imports parsed as %+v", empty)
	}

	if _, err := parsePackages("|context\n"); err == nil {
		t.Error("parsePackages accepted a line with an empty package path")
	}
}

// TestGoListArgsDisablesBuildVCS pins that every `go list` the tool runs carries
// `-buildvcs=false`, in the position `go` needs it.
//
// `go list` stamps VCS metadata for a main package by shelling out to git, and every lane
// here shares one `.git`: under a busy landing pipeline those git calls contend and the
// package-graph step fails with "error obtaining VCS status: exit status 128". The graph
// this tool reads needs no VCS metadata, so the flag removes the dependency instead of
// retrying the flake. It is asserted here because the failure is concurrency-dependent -
// no test can reproduce the contention - so the argument list is what pins the fix, and a
// call site that loses the flag fails this test rather than waiting for the next busy night.
func TestGoListArgsDisablesBuildVCS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "package graph",
			args: []string{"-f", `{{.ImportPath}}|{{join .Imports " "}}`, "./..."},
			want: []string{"list", "-buildvcs=false", "-f", `{{.ImportPath}}|{{join .Imports " "}}`, "./..."},
		},
		{
			name: "codec guard dependencies",
			args: []string{"-deps", "example.com/mod/gitlike"},
			want: []string{"list", "-buildvcs=false", "-deps", "example.com/mod/gitlike"},
		},
		{
			name: "module graph",
			args: []string{"-m", "-json", "all"},
			want: []string{"list", "-buildvcs=false", "-m", "-json", "all"},
		},
		{
			name: "module path",
			args: []string{"-m", "-f", "{{.Path}}"},
			want: []string{"list", "-buildvcs=false", "-m", "-f", "{{.Path}}"},
		},
		{
			name: "cas packages",
			args: []string{"./cas/..."},
			want: []string{"list", "-buildvcs=false", "./cas/..."},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := goListArgs(test.args)
			if strings.Join(got, " ") != strings.Join(test.want, " ") {
				t.Fatalf("goListArgs(%q) = %q, want %q", test.args, got, test.want)
			}
			// The position is the rule, not just the presence: `go` parses flags only
			// before the first positional argument, so a late flag is a package name.
			if got[0] != "list" || got[1] != "-buildvcs=false" {
				t.Errorf("goListArgs(%q) = %q, want the flag directly after list", test.args, got)
			}
			// The caller's arguments survive in order, so the flag can never displace one.
			for i, arg := range test.args {
				if got[i+2] != arg {
					t.Errorf("goListArgs(%q)[%d] = %q, want %q", test.args, i+2, got[i+2], arg)
				}
			}
		})
	}
}

// TestSplitLines pins command-output parsing: a trailing newline must not become
// an empty entry, which would be read as a package path that names nothing.
func TestSplitLines(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		list string
		want int
	}{
		{name: "empty output", list: "", want: 0},
		{name: "one line", list: "a\n", want: 1},
		{name: "several lines", list: "a\nb\n", want: 2},
		{name: "no trailing newline", list: "a", want: 1},
		{name: "blank lines are dropped", list: "a\n\nb\n", want: 2},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := len(splitLines(test.list)); got != test.want {
				t.Errorf("splitLines(%q) returned %d lines, want %d", test.list, got, test.want)
			}
		})
	}
}
