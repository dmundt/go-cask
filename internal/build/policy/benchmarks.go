package policy

import "path"

// BenchmarkTable is where the benchmark helpers keep their captures and how they name
// them.
//
// The ownership rule these values serve is the one issue #211 settled: the comparison
// helper used to capture a fresh run before choosing a baseline, so a no-argument run
// compared a capture against itself and overwrote the committed reference on the way.
// The reference dump is a comparison point, not a gate — performance.md §5 forbids
// promising a benchmark threshold and CI runs no `-bench` — so this table holds the two
// helpers' file ownership in one place.
type BenchmarkTable struct {
	// Capture is the `go test` invocation a capture runs: no tests, every benchmark,
	// allocation numbers, one run. `-count=1` is deliberate: a reference dump is
	// compared with a later dump, so it is one reproducible pass rather than a sample
	// a reader cannot reproduce from the command.
	Capture []string
	// Canonical is the committed reference dump. Only a deliberate `bench-baseline`
	// run rewrites it, and it archives the previous one first.
	Canonical string
	// ArchiveDir holds those archived references. An archive is never overwritten: a
	// name already taken moves to the next free one.
	ArchiveDir string
	// Current is the scratch capture a comparison writes, which is what keeps
	// comparing from ever touching Canonical.
	Current string
	// Stem and Extension are the naming convention every capture shares:
	// `<stem><extension>` is the canonical dump, `<stem>-<stamp><extension>` an
	// archived one.
	Stem      string
	Extension string
}

// Benchmarks returns go-cask's benchmark helper table. It is a function rather than a
// package-level variable so a caller cannot mutate the gate's policy by accident, and
// so the slice it hands out is the caller's own.
func Benchmarks() BenchmarkTable {
	return BenchmarkTable{
		Capture:    []string{"test", "./benchmarks", "-run=^$", "-bench=.", "-benchmem", "-count=1"},
		Canonical:  "benchmarks/data/baseline.txt",
		ArchiveDir: "benchmarks/data/archive",
		Current:    "benchmarks/data/current.txt",
		Stem:       "baseline",
		Extension:  ".txt",
	}
}

// BenchmarkArchiveName returns the name an archived capture of one stamp is stored
// under, inside the archive directory: `<stem>-<stamp><extension>`.
func BenchmarkArchiveName(stamp string) string {
	table := Benchmarks()
	return path.Join(table.ArchiveDir, table.Stem+"-"+stamp+table.Extension)
}
