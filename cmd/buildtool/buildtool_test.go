package main

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/internal/build/policy"
)

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
