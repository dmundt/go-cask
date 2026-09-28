package policy

import "strings"

// VerifyTable is the gate's own layout: which variable carries each escape hatch, and the
// smoke-fuzz set it runs.
//
// It is go-cask's answer, not the engine's: another repository would name different
// variables and a different fuzz set. The gate reads it through `cmd/gate verify`,
// which is where the step list itself lives.
type VerifyTable struct {
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
	SkipLintEnv     string
	// Compilers are the C compilers CGO needs. The race and coverage steps cannot run
	// without one, so the gate refuses up front rather than failing inside a build.
	Compilers []string
	// Platforms are the cross-compilation targets the cross-platform step cross-builds
	// and vets, in the order it runs them. The CI platform-matrix job gates the same set,
	// and a workflow is YAML that cannot read this table, so a test in this package pins
	// the two together (TestPlatformTargetsMatchTheWorkflow).
	Platforms []PlatformTarget
	// Fuzz is the smoke-fuzz set: one entry per target the gate runs, in the order it
	// runs them. A target that is added here without existing fails the policy tests, and
	// so does an engine package whose targets are not represented at all.
	Fuzz []FuzzTarget
	// ReleaseEnv names a tag being released, and ReleaseFromEnv the tag to compare it
	// against. The gate syncs the release note for that tag when the first is set.
	ReleaseEnv     string
	ReleaseFromEnv string
	// Checks names the checks a run records in its gate receipt. They live here rather than
	// at the call sites because the receipt's suite — and CI, which skips its own run on the
	// strength of that suite — reads the same names, so a step renamed in one place and not
	// the other costs a full CI run. `VerifySuite` is the one owner now that the helper that
	// also listed them is Go.
	Checks VerifyCheckNames
}

// PlatformTarget is one cross-compilation target: the GOOS and GOARCH pair the CI
// platform-matrix job cross-builds and the gate's cross-platform step cross-builds and
// vets.
//
// The pair is the whole identity — the matrix's display name is derived from it, so
// editing a display name can never desynchronise the two lists — and it is held as the
// two values `go build` takes rather than as one "os/arch" string, because the step sets
// them as separate environment variables.
type PlatformTarget struct {
	// GOOS is the target operating system, as `go build` spells it.
	GOOS string
	// GOARCH is the target architecture, as `go build` spells it.
	GOARCH string
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
	Lint            string
	CrossPlatform   string
	LayerMatrix     string
	CodecGuards     string
	Security        string
	TestRace        string
	CoverageTiers   string
	FuzzSmoke       string
	VersionFields   string
	DocIntegrity    string
	PackageGraph    string
	WebsiteFooter   string
	WebsiteExamples string
}

// VerifySuite returns the check names a full-scope receipt must list, in the order the
// receipt states them. CI asks the gate-receipt command for `--require-suite full`, which is
// this set: a run that did not record one of them leaves CI to run the whole gate.
//
// `govulncheck` is deliberately absent: CI runs the vulnerability scan in its own required
// job, so a receipt must not be able to excuse it. Every other check the gate runs is here,
// so a receipt that does not list one leaves CI to run the whole gate.
func VerifySuite() []string {
	checks := Verify().Checks
	names := []string{
		checks.Gofmt, checks.ModTidy, checks.Build, checks.ModuleGraph, checks.Vet,
		checks.Lint, checks.CrossPlatform, checks.LayerMatrix, checks.TestRace,
		checks.CoverageTiers, checks.FuzzSmoke, checks.VersionFields, checks.DocIntegrity,
		checks.PackageGraph, checks.WebsiteFooter, checks.WebsiteExamples,
	}
	// The codec guard records two names from one step; both are required.
	return append(names, strings.Fields(checks.CodecGuards)...)
}

// FuzzTarget is one smoke-fuzz target the gate runs: the package argument `go test` takes,
// and the fuzz function's name.
type FuzzTarget struct {
	// Package is the `go test` package argument, "./"-suffixed so it names a path.
	Package string
	// Target is the fuzz function's name in that package.
	Target string
}

// Verify returns go-cask's gate table. It is a function rather than a package-level
// variable so a caller cannot mutate the gate's policy by accident.
func Verify() VerifyTable {
	return VerifyTable{
		JobsEnv:         "VERIFY_JOBS",
		ScopeEnv:        "VERIFY_SCOPE",
		ScopeRule:       "docs_only",
		FastEnv:         "VERIFY_FAST",
		SkipTestsEnv:    "VERIFY_SKIP_TESTS",
		SkipCoverageEnv: "VERIFY_SKIP_COVERAGE",
		SkipFuzzEnv:     "VERIFY_SKIP_FUZZ",
		SkipSecurityEnv: "VERIFY_SKIP_SECURITY",
		SkipLintEnv:     "VERIFY_SKIP_LINT",
		Compilers:       []string{"gcc", "clang", "cc"},
		Checks: VerifyCheckNames{
			Gofmt:           "gofmt",
			ModTidy:         "go-mod-tidy",
			Build:           "go-build",
			ModuleGraph:     "module-graph",
			Vet:             "go-vet",
			Lint:            "golangci-lint",
			CrossPlatform:   "cross-platform",
			LayerMatrix:     "layer-matrix",
			CodecGuards:     "gitlike-codec-guard pack-codec-guard",
			Security:        "govulncheck",
			TestRace:        "go-test-race",
			CoverageTiers:   "coverage-tiers",
			FuzzSmoke:       "fuzz-smoke",
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
			// lines, a scanner's version report, a changed path, a coordination ref, a
			// receipt and the gate's own concurrency setting. `docs` carries two because its
			// frontmatter reader decides which files the version rule judges at all.
			{Package: "./internal/build/coverage/", Target: "FuzzParseResult"},
			{Package: "./internal/build/versioning/", Target: "FuzzField"},
			{Package: "./internal/build/docs/", Target: "FuzzFields"},
			{Package: "./internal/build/docs/", Target: "FuzzFrontmatterRoundTrip"},
			{Package: "./internal/build/landing/", Target: "FuzzParseHolder"},
			{Package: "./internal/build/landing/", Target: "FuzzParseEntry"},
			{Package: "./internal/build/toolchain/", Target: "FuzzMountPath"},
			{Package: "./internal/build/scope/", Target: "FuzzPatternMatches"},
			{Package: "./internal/build/landing/", Target: "FuzzIssueOf"},
			{Package: "./internal/build/scope/", Target: "FuzzJobs"},
			{Package: "./internal/build/landing/", Target: "FuzzReceiptParse"},
		},
		ReleaseEnv:     "CASK_RELEASE_TAG",
		ReleaseFromEnv: "CASK_RELEASE_FROM_TAG",
		// The set the gate and the CI matrix both gate: every target the project ships
		// for — the two desktop targets no runner of their own is spent on, the arm64
		// Linux target the host cannot build, and linux/amd64, the host's own platform,
		// which the plain `go build ./...` builds with the host's CGO setting and this
		// set builds with CGO off.
		Platforms: []PlatformTarget{
			{GOOS: "windows", GOARCH: "amd64"},
			{GOOS: "darwin", GOARCH: "amd64"},
			{GOOS: "darwin", GOARCH: "arm64"},
			{GOOS: "linux", GOARCH: "amd64"},
			{GOOS: "linux", GOARCH: "arm64"},
		},
	}
}
