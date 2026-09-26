package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/internal/build/core/gate"
	"github.com/dmundt/go-cask/internal/build/core/verify"
	"github.com/dmundt/go-cask/internal/build/policy"
)

// TestGateStepsAreWellFormed pins the step list's own shape: every step names itself once
// and does something, the documentation steps come last so the scope is a prefix-to-suffix
// split, and the release-note sync is the only step that decides for itself whether it
// applies.
func TestGateStepsAreWellFormed(t *testing.T) {
	t.Parallel()

	steps := gateSteps(policy.Verify())
	if len(steps) == 0 {
		t.Fatal("the gate has no steps")
	}
	seen := map[string]bool{}
	for _, step := range steps {
		if step.Name == "" {
			t.Error("a step has no name, so its heading and its skip report would be blank")
		}
		if seen[step.Name] {
			t.Errorf("the step %q is listed twice", step.Name)
		}
		seen[step.Name] = true
		if step.Run == nil {
			t.Errorf("the step %q does nothing", step.Name)
		}
		if step.Enabled != nil && !step.DocsScope {
			t.Errorf("the step %q decides whether it applies but runs only in the full scope", step.Name)
		}
	}
	for _, want := range []string{
		"gofmt", "go mod tidy", "go build", "go vet", "build engine module",
		"layer matrix check", "codec guards", "govulncheck", "test -race + coverage gate",
		"fuzz smoke", "doc integrity", "package graph", "website footer",
		"website examples", "release note sync",
	} {
		if !seen[want] {
			t.Errorf("the gate no longer has a %q step", want)
		}
	}

	// The documentation steps are the tail of the list, which is what makes the scope a
	// split rather than a filter: everything before the first one needs the whole module.
	first := len(steps)
	for i, step := range steps {
		if step.DocsScope {
			first = i
			break
		}
	}
	if first == len(steps) {
		t.Fatal("no step runs in the documentation scope")
	}
	for _, step := range steps[first:] {
		if !step.DocsScope {
			t.Errorf("the step %q follows a documentation step but does not run in that scope", step.Name)
		}
	}

	enabled := 0
	for _, step := range steps {
		if step.Enabled != nil {
			enabled++
		}
	}
	if enabled != 1 {
		t.Errorf("%d steps decide whether they apply, want exactly the release-note sync", enabled)
	}
}

// TestStepsForTheDocumentationScope pins the scope's shape: it is a strict subset of the
// full gate, it keeps the checks a Markdown-only change can break, and it drops the ones
// that need the module, the race suite and the vulnerability scan.
func TestStepsForTheDocumentationScope(t *testing.T) {
	t.Parallel()

	full := stepsFor(policy.Verify(), verify.Full)
	docs := stepsFor(policy.Verify(), verify.Docs)
	if len(full) != len(gateSteps(policy.Verify())) {
		t.Errorf("the full scope runs %d of %d steps", len(full), len(gateSteps(policy.Verify())))
	}
	if len(docs) == 0 || len(docs) >= len(full) {
		t.Fatalf("the documentation scope runs %d of %d steps, want a strict subset", len(docs), len(full))
	}

	names := map[string]bool{}
	for _, step := range docs {
		names[step.Name] = true
	}
	for _, want := range []string{"doc integrity", "package graph", "website footer", "website examples", "release note sync"} {
		if !names[want] {
			t.Errorf("the documentation scope does not run %q", want)
		}
	}
	for _, unwanted := range []string{"gofmt", "go mod tidy", "go vet", "layer matrix check", "govulncheck", "test -race + coverage gate", "fuzz smoke"} {
		if names[unwanted] {
			t.Errorf("the documentation scope runs %q, which needs the whole module", unwanted)
		}
	}
}

// TestGateRecordsOnlyACompleteRun pins the gate's one promise. The record it writes is what
// authorises a push, so a run that skipped a step must leave the ledger alone and say so;
// otherwise a fast run would hand the pre-push hook a green light for a tree the race suite
// never saw.
func TestGateRecordsOnlyACompleteRun(t *testing.T) {
	table := policy.Verify()

	t.Run("a complete run is recorded", func(t *testing.T) {
		toplevel, head := hookRepo(t)
		var out, errOut bytes.Buffer
		run := &gateRun{out: &out, errOut: &errOut, table: table, root: toplevel, scope: verify.Full}
		if err := run.finish(); err != nil {
			t.Fatalf("finish: %v", err)
		}

		ledger := readFileOrEmpty(ledgerPath(t, toplevel))
		if !gate.Verified(ledger, head) {
			t.Errorf("the ledger %q does not carry %s", ledger, head)
		}
		entry, ok := gate.Parse(strings.TrimSpace(ledger))
		if !ok || entry.Scope != verify.Full.String() {
			t.Errorf("the ledger entry is %+v, want the run's scope", entry)
		}
		if !strings.Contains(out.String(), "verification passed") {
			t.Errorf("a complete run printed %q", out.String())
		}
		if strings.Contains(errOut.String(), "not verified") {
			t.Errorf("a complete run reported itself incomplete: %q", errOut.String())
		}
	})

	t.Run("a run that skipped a step is not recorded", func(t *testing.T) {
		toplevel, head := hookRepo(t)
		t.Setenv(table.SkipSecurityEnv, "true")

		var out, errOut bytes.Buffer
		run := &gateRun{out: &out, errOut: &errOut, table: table, root: toplevel, scope: verify.Full}
		if !run.escape("govulncheck", table.SkipSecurityEnv) {
			t.Fatal("the escape hatch did not drop the step")
		}
		if !strings.Contains(out.String(), "skipped: govulncheck") {
			t.Errorf("the skipped step was not reported: %q", out.String())
		}
		if err := run.finish(); err != nil {
			t.Fatalf("finish: %v", err)
		}

		if ledger := readFileOrEmpty(ledgerPath(t, toplevel)); ledger != "" {
			t.Errorf("an incomplete run wrote the ledger: %q", ledger)
		}
		if gate.Verified(readFileOrEmpty(ledgerPath(t, toplevel)), head) {
			t.Error("an incomplete run recorded the commit as verified")
		}
		if !strings.Contains(errOut.String(), "not verified") {
			t.Errorf("an incomplete run did not refuse on stderr: %q", errOut.String())
		}
		if !strings.Contains(out.String(), "not stamped") {
			t.Errorf("an incomplete run did not say it was not stamped: %q", out.String())
		}
	})

	t.Run("the drop-everything switch drops a step too", func(t *testing.T) {
		toplevel, _ := hookRepo(t)
		var out, errOut bytes.Buffer
		run := &gateRun{
			out: &out, errOut: &errOut, table: table, root: toplevel,
			scope: verify.Full, fast: true,
		}
		if !run.escape("go test -race ./...", table.SkipTestsEnv) {
			t.Error("the drop-everything switch did not drop the step, though its own variable is unset")
		}
	})
}

// TestCheckTreeRefusesTheWrongTree pins the guard against the one way the gate can verify a
// tree nobody asked about — a linked worktree whose `.git` link this toolchain cannot
// resolve, so git answers with the primary checkout — and against the plainer case of a
// caller who started the tool in a subdirectory.
func TestCheckTreeRefusesTheWrongTree(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	linked := filepath.Join(root, "linked")
	for _, dir := range []string{sub, linked} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("creating %s: %v", dir, err)
		}
	}
	// A checkout of its own carries a `.git` entry — here a link git cannot resolve, which
	// is what makes it walk up to the primary.
	if err := os.WriteFile(filepath.Join(linked, ".git"), []byte("gitdir: /nonexistent\n"), 0o644); err != nil {
		t.Fatalf("writing the link: %v", err)
	}

	cases := []struct {
		name    string
		started string
		want    string
	}{
		{name: "the checkout git resolved", started: root},
		{name: "a subdirectory", started: sub, want: "repository root"},
		{name: "a checkout git walked up from", started: linked, want: "WRONG tree"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var errOut bytes.Buffer
			err := checkTree(tc.started, root, &errOut)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("checkTree = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("checkTree(%s) = nil, want a refusal", tc.started)
			}
			var status statusError
			if !errors.As(err, &status) || status.code != 2 {
				t.Errorf("checkTree(%s) = %v, want the usage status", tc.started, err)
			}
			if !strings.Contains(errOut.String(), tc.want) {
				t.Errorf("the refusal %q does not name %q", errOut.String(), tc.want)
			}
		})
	}
}

// TestVerifyRejectsBadInvocations pins the command's surface: the gate takes no arguments,
// and a stray one is refused before a single step runs.
func TestVerifyRejectsBadInvocations(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"verify", "extra"},
		{"verify", "--nope"},
	} {
		var out, errOut bytes.Buffer
		if err := run(args, &out, &errOut); err == nil {
			t.Errorf("run(%q) succeeded, want an error", args)
		}
		if out.Len() != 0 {
			t.Errorf("run(%q) wrote %q to stdout despite failing", args, out.String())
		}
	}
}
