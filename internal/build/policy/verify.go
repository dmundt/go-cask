package policy

import "strings"

// VerifyTable is the gate's own layout: where the nested module is, which variable carries
// each escape hatch, and the smoke-fuzz set it runs.
//
// It is go-cask's answer, not the engine's: another repository would name a different
// module directory, different variables and a different fuzz set. The gate reads it through
// `cmd/buildtool verify`, which is where the step list itself lives.
type VerifyTable struct {
	// EngineDir is the nested module the gate must name explicitly. `go build ./...`,
	// `go vet ./...`, `go test ./...` and `gofmt -l .` all skip a nested module, so a step
	// that does not name this directory lets the engine rot while the gate stays green.
	EngineDir string
	// JobsEnv carries how many packages may be built and tested at once.
	JobsEnv string
	// ScopeEnv carries the requested scope, and ScopeRule is the change-set rule whose
	// verdict decides the automatic one.
	ScopeEnv  string
	ScopeRule string
	// FastEnv drops every expensive step at once.
	FastEnv string
	// The escape hatches, one per expensive step. Each names the step it drops in the
	// gate's own report, so the report and the variable a caller set cannot disagree.
	SkipTestsEnv    string
	SkipCoverageEnv string
	SkipFuzzEnv     string
	SkipSecurityEnv string
	// Compilers are the C compilers CGO needs. The race and coverage steps cannot run
	// without one, so the gate refuses up front rather than failing inside a build.
	Compilers []string
	// Fuzz is the smoke-fuzz set: one entry per target the gate runs, in the order it
	// runs them. A target that is added here without existing fails the policy tests, and
	// so does an engine package whose targets are not represented at all.
	Fuzz []FuzzTarget
	// ReleaseEnv names a tag being released, and ReleaseFromEnv the tag to compare it
	// against. The gate syncs the release note for that tag when the first is set.
	ReleaseEnv     string
	ReleaseFromEnv string
	// ReceiptScript writes, signs and verifies the gate receipt: the same evidence as the
	// stamp, made portable so CI can reuse a green local run. The gate calls `create` with
	// it and the pre-push hook calls `publish`.
	ReceiptScript string
	// Checks names the checks a run records in that receipt. They live here rather than at
	// the call sites because gate-receipt.sh's `suite_full` lists the same names and CI
	// skips its own run on the strength of them, so a rename that misses one of the two
	// places costs a full CI run — the policy test pins the pair.
	Checks VerifyCheckNames
}

// VerifyCheckNames are the names a gate run records, one field per check. The receipt is a
// line-oriented record, so a name may not contain a space: the codec guard is one step that
// answers for two packages, and it records both names.
type VerifyCheckNames struct {
	Gofmt           string
	ModTidy         string
	Build           string
	ModuleGraph     string
	Vet             string
	CrossPlatform   string
	LayerMatrix     string
	CodecGuards     string
	Security        string
	TestRace        string
	CoverageTiers   string
	FuzzSmoke       string
	HelperScripts   string
	VersionFields   string
	DocIntegrity    string
	PackageGraph    string
	WebsiteFooter   string
	WebsiteExamples string
}

// VerifySuite returns the check names a full-scope receipt must list, in the order the
// receipt states them. CI asks gate-receipt.sh for `--require-suite full`, which is this
// set: a run that did not record one of them leaves CI to run the whole gate.
//
// `govulncheck` is deliberately absent, and so is the engine module's own suite: CI runs
// the vulnerability scan in its own required job, so a receipt must not be able to excuse
// it, and the engine is covered by the root suite's recipe rather than by a receipt name.
func VerifySuite() []string {
	checks := Verify().Checks
	names := []string{
		checks.Gofmt, checks.ModTidy, checks.Build, checks.ModuleGraph, checks.Vet,
		checks.CrossPlatform, checks.LayerMatrix, checks.TestRace, checks.CoverageTiers,
		checks.FuzzSmoke, checks.HelperScripts, checks.VersionFields, checks.DocIntegrity,
		checks.PackageGraph, checks.WebsiteFooter, checks.WebsiteExamples,
	}
	// The codec guard records two names from one step; both are required.
	return append(names, strings.Fields(checks.CodecGuards)...)
}

// FuzzTarget is one smoke-fuzz target the gate runs: the package argument `go test` takes,
// the fuzz function's name, and whether the target lives in the nested engine module —
// which decides the directory the gate runs it from, not the package argument.
type FuzzTarget struct {
	// Package is the `go test` package argument, "./"-suffixed so it names a path.
	Package string
	// Target is the fuzz function's name in that package.
	Target string
	// Engine is true when the target lives in the nested engine module.
	Engine bool
}

// Verify returns go-cask's gate table. It is a function rather than a package-level
// variable so a caller cannot mutate the gate's policy by accident.
func Verify() VerifyTable {
	return VerifyTable{
		EngineDir:       "internal/build/core",
		JobsEnv:         "VERIFY_JOBS",
		ScopeEnv:        "VERIFY_SCOPE",
		ScopeRule:       "docs_only",
		FastEnv:         "VERIFY_FAST",
		SkipTestsEnv:    "VERIFY_SKIP_TESTS",
		SkipCoverageEnv: "VERIFY_SKIP_COVERAGE",
		SkipFuzzEnv:     "VERIFY_SKIP_FUZZ",
		SkipSecurityEnv: "VERIFY_SKIP_SECURITY",
		Compilers:       []string{"gcc", "clang", "cc"},
		ReceiptScript:   "scripts/gate-receipt.sh",
		Checks: VerifyCheckNames{
			Gofmt:           "gofmt",
			ModTidy:         "go-mod-tidy",
			Build:           "go-build",
			ModuleGraph:     "module-graph",
			Vet:             "go-vet",
			CrossPlatform:   "cross-platform",
			LayerMatrix:     "layer-matrix",
			CodecGuards:     "gitlike-codec-guard pack-codec-guard",
			Security:        "govulncheck",
			TestRace:        "go-test-race",
			CoverageTiers:   "coverage-tiers",
			FuzzSmoke:       "fuzz-smoke",
			HelperScripts:   "helper-scripts",
			VersionFields:   "version-fields",
			DocIntegrity:    "doc-integrity",
			PackageGraph:    "package-graph",
			WebsiteFooter:   "website-footer",
			WebsiteExamples: "website-examples",
		},
		Fuzz: []FuzzTarget{
			// The four targets docs/specs/testing-strategy.md §5 names as the smoke set,
			// in the order it lists them.
			{Package: "./cas/", Target: "FuzzParseDigest"},
			{Package: "./cas/backend/fs/", Target: "FuzzPathRoundTrip"},
			{Package: "./cas/backend/fs/", Target: "FuzzVerify"},
			{Package: "./cas/codec/json/", Target: "FuzzCodecRoundTrip"},
			// One target per engine package that parses what the repository and its tools
			// hand it: measurement lines, frontmatter, YAML scalars, slot records, ledger
			// lines, a scanner's version report, a changed path, a coordination ref and the
			// gate's own concurrency setting. `docs` carries two because its frontmatter
			// reader decides which files the version rule judges at all.
			{Package: "./coverage/", Target: "FuzzParseResult", Engine: true},
			{Package: "./versioning/", Target: "FuzzField", Engine: true},
			{Package: "./docs/", Target: "FuzzFields", Engine: true},
			{Package: "./docs/", Target: "FuzzFrontmatterRoundTrip", Engine: true},
			{Package: "./lane/", Target: "FuzzParseHolder", Engine: true},
			{Package: "./gate/", Target: "FuzzParse", Engine: true},
			{Package: "./toolchain/", Target: "FuzzMountPath", Engine: true},
			{Package: "./changes/", Target: "FuzzPatternMatches", Engine: true},
			{Package: "./claim/", Target: "FuzzIssueOf", Engine: true},
			{Package: "./verify/", Target: "FuzzJobs", Engine: true},
		},
		ReleaseEnv:     "CASK_RELEASE_TAG",
		ReleaseFromEnv: "CASK_RELEASE_FROM_TAG",
	}
}
