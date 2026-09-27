package policy

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// TestBenchmarksTableIsWellFormed pins the table's own shape and the invariants that
// tie it together: the canonical dump is the stem the archived captures share, and every
// capture file lives in one directory, so a helper cannot write outside the benchmark
// data tree.
func TestBenchmarksTableIsWellFormed(t *testing.T) {
	t.Parallel()

	table := Benchmarks()
	if len(table.Capture) == 0 || table.Canonical == "" || table.ArchiveDir == "" || table.Current == "" ||
		table.Stem == "" || table.Extension == "" {
		t.Fatalf("the benchmark table is incomplete: %+v", table)
	}

	for _, field := range []struct {
		name  string
		value string
	}{
		{"Canonical", table.Canonical},
		{"ArchiveDir", table.ArchiveDir},
		{"Current", table.Current},
	} {
		if path.IsAbs(field.value) || strings.Contains(field.value, `\`) {
			t.Errorf("%s = %q, want a repository-relative path with forward slashes", field.name, field.value)
		}
	}

	dataDir := path.Dir(table.Canonical)
	if got := path.Base(table.Canonical); got != table.Stem+table.Extension {
		t.Errorf("the canonical dump is named %q, want %q", got, table.Stem+table.Extension)
	}
	if got := path.Dir(table.ArchiveDir); got != dataDir {
		t.Errorf("the archive lives in %q, want the canonical dump's directory %q", got, dataDir)
	}
	if got := path.Dir(table.Current); got != dataDir {
		t.Errorf("the scratch capture lives in %q, want the canonical dump's directory %q", got, dataDir)
	}
	if got, want := BenchmarkArchiveName("20260925-124649"), dataDir+"/archive/"+table.Stem+"-20260925-124649"+table.Extension; got != want {
		t.Errorf("BenchmarkArchiveName = %q, want %q", got, want)
	}

	// The capture invocation must name the suite it claims to measure, or a capture
	// would silently benchmark the wrong package.
	names := false
	for _, arg := range table.Capture {
		if arg == "./benchmarks" {
			names = true
		}
	}
	if !names {
		t.Errorf("the capture invocation %q does not name ./benchmarks", table.Capture)
	}
}

// TestBenchmarksTableMatchesTheTree runs the table against the real repository: the
// committed reference dump is there and holds captures, and the archive path is a
// directory when it exists — the archive is created by the first capture that needs it,
// because Git does not track an empty directory.
func TestBenchmarksTableMatchesTheTree(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	table := Benchmarks()

	canonical, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(table.Canonical)))
	if err != nil {
		t.Fatalf("the committed reference dump is missing: %v", err)
	}
	if !strings.Contains(string(canonical), "ns/op") {
		t.Errorf("%s holds no benchmark line, so it is not a reference dump", table.Canonical)
	}
	if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(table.ArchiveDir))); err == nil && !info.IsDir() {
		t.Errorf("%s exists and is not a directory", table.ArchiveDir)
	}
}
