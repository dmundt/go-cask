package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stubCaptureOutput is benchmark-shaped output: a capture file a test can read back
// without running a suite.
const stubCaptureOutput = "goos: linux\n" +
	"goarch: amd64\n" +
	"pkg: example.test/benchmarks\n" +
	"BenchmarkStub-8   1000   1000 ns/op   10 B/op   1 allocs/op\n"

// referenceFixture is the committed reference a fixture starts with.
const referenceFixture = "committed reference capture\n"

// stubDeps returns the benchmark collaborators a test drives: a capture that writes
// fixed output instead of running the suite, and a comparison that records the command
// line it was handed instead of running a tool that need not be installed.
func stubDeps(t *testing.T, output string) (benchDeps, *[]string) {
	t.Helper()
	compared := &[]string{}
	return benchDeps{
		capture: func(root, dest string, out io.Writer) error {
			if err := os.WriteFile(dest, []byte(output), 0o644); err != nil {
				return err
			}
			_, err := io.WriteString(out, output)
			return err
		},
		find: func(name string) (string, bool) { return "/stub/" + name, true },
		compare: func(bin, baseline, current string, out, errOut io.Writer) error {
			*compared = append(*compared, bin, baseline, current)
			return nil
		},
	}, compared
}

// benchFixture creates a throwaway git repository with the benchmark layout the
// commands write into, and makes it the working directory: they resolve the repository
// from Git, so each subtest runs in its own rather than in the checkout.
//
// The test that calls this may not be parallel, because it changes the process's
// working directory.
func benchFixture(t *testing.T) (root, canonical, current string) {
	t.Helper()
	root = t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "benchmarks", "data", "archive"), 0o755); err != nil {
		t.Fatalf("creating the fixture layout: %v", err)
	}
	canonical = filepath.Join(root, "benchmarks", "data", "baseline.txt")
	current = filepath.Join(root, "benchmarks", "data", "current.txt")
	writeFixtureFile(t, canonical, referenceFixture)

	init := exec.Command("git", "init", "-q", "-b", "main")
	init.Dir = root
	if out, err := init.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	t.Chdir(root)
	return root, canonical, current
}

// writeFixtureFile writes a fixture file, creating its directory.
func writeFixtureFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// readFixtureFile returns a fixture file's contents.
func readFixtureFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

// TestBenchBaselineOwnsTheCanonicalReference pins the half of issue #211 that the
// deliberate run owns: it archives the previous reference before replacing it, it
// writes where it was asked to, and --capture-only never touches the reference.
func TestBenchBaselineOwnsTheCanonicalReference(t *testing.T) {
	t.Run("a deliberate run refreshes the reference and archives it", func(t *testing.T) {
		root, canonical, _ := benchFixture(t)
		deps, _ := stubDeps(t, stubCaptureOutput)

		var out, errOut bytes.Buffer
		if err := benchBaseline(nil, &out, &errOut, deps); err != nil {
			t.Fatalf("benchBaseline: %v", err)
		}
		if got := readFixtureFile(t, canonical); got != stubCaptureOutput {
			t.Errorf("the reference dump holds %q, want the capture", got)
		}

		// The run's capture is written into the archive directory under its stamp,
		// and the previous reference is archived beside it under the next free name:
		// never over the capture just written, never over an earlier archive.
		archiveDir := filepath.Join(root, "benchmarks", "data", "archive")
		entries, err := os.ReadDir(archiveDir)
		if err != nil {
			t.Fatalf("reading the archive: %v", err)
		}
		archivedReference, archivedCapture := false, false
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), "baseline-") || !strings.HasSuffix(entry.Name(), ".txt") {
				t.Errorf("archived %q, which is not the capture naming convention", entry.Name())
			}
			switch readFixtureFile(t, filepath.Join(archiveDir, entry.Name())) {
			case referenceFixture:
				archivedReference = true
			case stubCaptureOutput:
				archivedCapture = true
			default:
				t.Errorf("archive %s holds neither the capture nor the previous reference", entry.Name())
			}
		}
		if !archivedReference {
			t.Error("the previous reference was not archived before it was replaced")
		}
		if !archivedCapture {
			t.Error("the run's own capture is not in the archive directory")
		}
	})

	t.Run("--capture-only writes the capture and leaves the reference alone", func(t *testing.T) {
		root, canonical, _ := benchFixture(t)
		deps, _ := stubDeps(t, "capture only\n")
		capture := filepath.Join(root, "benchmarks", "data", "archive", "scratch.txt")

		// The out-file comes before the option here, which the shell helper accepted
		// and which the command keeps accepting.
		var out, errOut bytes.Buffer
		if err := benchBaseline([]string{capture, "--capture-only"}, &out, &errOut, deps); err != nil {
			t.Fatalf("benchBaseline --capture-only: %v", err)
		}
		if got := readFixtureFile(t, capture); got != "capture only\n" {
			t.Errorf("the capture holds %q, want what the run wrote", got)
		}
		if got := readFixtureFile(t, canonical); got != referenceFixture {
			t.Errorf("the reference dump holds %q, want it untouched", got)
		}
		if !strings.Contains(out.String(), "capture-only") {
			t.Errorf("the run did not report that it captured only:\n%s", out.String())
		}
	})

	t.Run("a capture written over the reference is not archived over itself", func(t *testing.T) {
		_, canonical, _ := benchFixture(t)
		deps, _ := stubDeps(t, "self capture\n")

		var out, errOut bytes.Buffer
		if err := benchBaseline([]string{canonical}, &out, &errOut, deps); err != nil {
			t.Fatalf("benchBaseline: %v", err)
		}
		if got := readFixtureFile(t, canonical); got != "self capture\n" {
			t.Errorf("the reference dump holds %q, want the capture written to it", got)
		}
		archiveDir := filepath.Join(filepath.Dir(canonical), "archive")
		if entries, err := os.ReadDir(archiveDir); err == nil {
			for _, entry := range entries {
				if got := readFixtureFile(t, filepath.Join(archiveDir, entry.Name())); got == "self capture\n" {
					t.Errorf("archive %s holds the capture itself; a reference must not be archived over itself", entry.Name())
				}
			}
		}
	})
}

// TestBenchCompareNeverWritesTheCanonicalReference pins the other half of issue #211:
// the comparison chooses its baseline before it captures, it never writes the reference,
// and it refuses a comparison that would report no change.
func TestBenchCompareNeverWritesTheCanonicalReference(t *testing.T) {
	t.Run("the committed reference is the baseline and stays byte-identical", func(t *testing.T) {
		_, canonical, current := benchFixture(t)
		deps, compared := stubDeps(t, stubCaptureOutput+"\n")

		var out, errOut bytes.Buffer
		if err := benchCompare(nil, &out, &errOut, deps); err != nil {
			t.Fatalf("benchCompare: %v", err)
		}
		if got := readFixtureFile(t, canonical); got != referenceFixture {
			t.Errorf("the reference dump changed to %q", got)
		}
		if got := readFixtureFile(t, current); got != stubCaptureOutput+"\n" {
			t.Errorf("the scratch capture holds %q", got)
		}
		if len(*compared) != 3 || (*compared)[1] != canonical || (*compared)[2] != current {
			t.Errorf("compared %q, want the reference against the fresh capture", *compared)
		}
		if !strings.Contains(out.String(), "comparing ") {
			t.Errorf("the run did not report what it compares:\n%s", out.String())
		}
	})

	t.Run("an explicit baseline is used as given", func(t *testing.T) {
		root, _, _ := benchFixture(t)
		older := filepath.Join(root, "benchmarks", "data", "archive", "baseline-old.txt")
		writeFixtureFile(t, older, "older archived reference\n")
		deps, compared := stubDeps(t, stubCaptureOutput)

		var out, errOut bytes.Buffer
		if err := benchCompare([]string{older}, &out, &errOut, deps); err != nil {
			t.Fatalf("benchCompare: %v", err)
		}
		if len(*compared) != 3 || (*compared)[1] != older {
			t.Errorf("compared %q, want the baseline the caller named", *compared)
		}
	})

	t.Run("the newest archive is the fallback when the reference is gone", func(t *testing.T) {
		root, canonical, _ := benchFixture(t)
		if err := os.Remove(canonical); err != nil {
			t.Fatalf("removing the fixture reference: %v", err)
		}
		older := filepath.Join(root, "benchmarks", "data", "archive", "baseline-20200101-000000Z.txt")
		newer := filepath.Join(root, "benchmarks", "data", "archive", "baseline-20260101-000000Z.txt")
		writeFixtureFile(t, older, "old archived reference\n")
		writeFixtureFile(t, newer, "new archived reference\n")
		// The fallback is chronological, so the two write times are set explicitly:
		// two files written in the same instant have no newest among them, and the
		// rule would fall back to ordering them by path.
		oldTime := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
		newTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		for path, when := range map[string]time.Time{older: oldTime, newer: newTime} {
			if err := os.Chtimes(path, when, when); err != nil {
				t.Fatalf("setting the write time of %s: %v", path, err)
			}
		}
		deps, compared := stubDeps(t, stubCaptureOutput)

		var out, errOut bytes.Buffer
		if err := benchCompare(nil, &out, &errOut, deps); err != nil {
			t.Fatalf("benchCompare: %v", err)
		}
		if len(*compared) != 3 || (*compared)[1] != newer {
			t.Errorf("compared %q, want the newest archived capture", *compared)
		}
		if _, err := os.Stat(canonical); !os.IsNotExist(err) {
			t.Error("the comparison invented a canonical reference")
		}
	})

	t.Run("no baseline anywhere fails loudly and captures nothing", func(t *testing.T) {
		root, canonical, current := benchFixture(t)
		if err := os.Remove(canonical); err != nil {
			t.Fatalf("removing the fixture reference: %v", err)
		}
		if entries, err := os.ReadDir(filepath.Join(root, "benchmarks", "data", "archive")); err == nil {
			for _, entry := range entries {
				if err := os.Remove(filepath.Join(root, "benchmarks", "data", "archive", entry.Name())); err != nil {
					t.Fatalf("clearing the archive: %v", err)
				}
			}
		}
		deps, _ := stubDeps(t, stubCaptureOutput)

		var out, errOut bytes.Buffer
		err := benchCompare(nil, &out, &errOut, deps)
		if err == nil {
			t.Fatal("benchCompare succeeded with nothing to compare against")
		}
		if !strings.Contains(err.Error(), "bench-baseline") {
			t.Errorf("the error %q does not point at the command that captures a reference", err)
		}
		if _, statErr := os.Stat(current); !os.IsNotExist(statErr) {
			t.Error("the comparison captured a run before it had a baseline")
		}
	})

	t.Run("comparing a file with itself is refused before anything is captured", func(t *testing.T) {
		_, canonical, current := benchFixture(t)
		deps, compared := stubDeps(t, stubCaptureOutput)

		var out, errOut bytes.Buffer
		err := benchCompare([]string{canonical, canonical}, &out, &errOut, deps)
		if err == nil {
			t.Fatal("benchCompare compared a capture with itself")
		}
		if !strings.Contains(err.Error(), "same file") {
			t.Errorf("the error %q does not explain that the two are one file", err)
		}
		if len(*compared) != 0 {
			t.Errorf("a refused comparison still ran %q", *compared)
		}
		if _, statErr := os.Stat(current); !os.IsNotExist(statErr) {
			t.Error("a refused comparison captured a run")
		}
	})

	t.Run("identical content is called out instead of reported as clean", func(t *testing.T) {
		_, canonical, _ := benchFixture(t)
		writeFixtureFile(t, canonical, stubCaptureOutput)
		deps, _ := stubDeps(t, stubCaptureOutput)

		var out, errOut bytes.Buffer
		if err := benchCompare(nil, &out, &errOut, deps); err != nil {
			t.Fatalf("benchCompare: %v", err)
		}
		if !strings.Contains(errOut.String(), "byte-identical") {
			t.Errorf("the run did not call out an identical capture:\n%s", errOut.String())
		}
	})

	t.Run("a missing benchstat keeps its documented exit status", func(t *testing.T) {
		_, canonical, _ := benchFixture(t)
		deps, _ := stubDeps(t, stubCaptureOutput+"\n")
		deps.find = func(string) (string, bool) { return "", false }

		var out, errOut bytes.Buffer
		err := benchCompare(nil, &out, &errOut, deps)
		var status statusError
		if !errors.As(err, &status) {
			t.Fatalf("benchCompare returned %v, want a status error", err)
		}
		if status.code != 2 {
			t.Errorf("missing benchstat exited %d, want 2", status.code)
		}
		if !strings.Contains(errOut.String(), "diff -u") {
			t.Errorf("the run printed no manual diff hint:\n%s", errOut.String())
		}
		if got := readFixtureFile(t, canonical); got != referenceFixture {
			t.Errorf("a failed comparison changed the reference dump to %q", got)
		}
	})

	t.Run("the installed tool is handed the two captures", func(t *testing.T) {
		_, canonical, current := benchFixture(t)
		deps, _ := stubDeps(t, stubCaptureOutput)
		var ran []string
		deps.compare = func(bin, baseline, cur string, out, errOut io.Writer) error {
			ran = []string{bin, baseline, cur}
			_, err := fmt.Fprintln(out, "benchstat report")
			return err
		}
		deps.find = func(name string) (string, bool) { return "/opt/bin/" + name, true }

		var out, errOut bytes.Buffer
		if err := benchCompare(nil, &out, &errOut, deps); err != nil {
			t.Fatalf("benchCompare: %v", err)
		}
		if len(ran) != 3 || ran[0] != "/opt/bin/benchstat" || ran[1] != canonical || ran[2] != current {
			t.Fatalf("ran %q, want the resolved benchstat on the reference and the fresh capture", ran)
		}
		if !strings.Contains(out.String(), "benchstat report") {
			t.Errorf("the comparison's report did not reach the run's output:\n%s", out.String())
		}
	})
}

// TestBenchCommandsRejectBadInvocations pins the shell helper's exit statuses: an
// unknown option is the caller's mistake (2), and help is not an error and names the
// reference the commands own.
func TestBenchCommandsRejectBadInvocations(t *testing.T) {
	for _, name := range []string{"bench-baseline", "bench-compare"} {
		t.Run(name+" rejects an unknown option", func(t *testing.T) {
			var out, errOut bytes.Buffer
			err := run([]string{name, "--nope"}, &out, &errOut)
			var usage usageError
			if !errors.As(err, &usage) {
				t.Fatalf("run(%s --nope) returned %v, want a usage error", name, err)
			}
			if out.Len() != 0 {
				t.Errorf("run(%s --nope) wrote %q to stdout", name, out.String())
			}
		})

		t.Run(name+" help succeeds and names the reference", func(t *testing.T) {
			var out, errOut bytes.Buffer
			if err := run([]string{name, "--help"}, &out, &errOut); err != nil {
				t.Fatalf("run(%s --help) returned %v, want nil", name, err)
			}
			if usage := out.String(); !strings.Contains(usage, "baseline.txt") {
				t.Errorf("run(%s --help) printed %q, which does not name the reference", name, usage)
			}
		})
	}
}
