// Package coverage holds the repository's coverage policy: which packages the
// gate measures, at what threshold, and which packages are deliberately ungated.
//
// docs/specs/testing-strategy.md §5 states the rule and deliberately omits the
// list, so a threshold change touches one file. That file was scripts/verify.sh,
// where the table was a bash array and the drift check a comm(1) comparison —
// readable only by running the gate, and unenforced against the table's own
// shape.
//
// The table lives here as data with its format checked (Policy.Validate), and the
// comparison is a pure function over the package list Go reports, so both are
// tested directly. The gate itself runs `go test -race -cover` once over the
// module and reads the profile that run writes (Policy.Measure): one pass answers
// for the suite and for every tier, and that is orchestration, which lives in
// cmd/buildtool beside the rest of the step list.
//
// The tier thresholds are the gate's contract with docs/specs/testing-strategy.md
// §5 — the table documents the tier, the threshold enforces it. Editing a number
// here changes what the gate accepts, so it is a specification change, not a
// refactor.
package coverage

import (
	"errors"
	"fmt"
	"math"
	"path"
	"sort"
	"strconv"
	"strings"
)

// Target is one gated package: a threshold and the tier that documents it.
type Target struct {
	// Threshold is the minimum coverage percentage the gate enforces.
	Threshold float64
	// Package is the package path as `go list` reports it, without a leading
	// "./" — the form both Go and this table normalize to.
	Package string
	// Tier is the tier name. It documents the row; it does not enforce anything.
	Tier string
}

// Exemption is one deliberately ungated package and the written reason why.
type Exemption struct {
	// Package is the package path, normalized as in Target.Package.
	Package string
	// Reason is why the package is gated by nothing. A reason is required: the
	// point of the register is that leaving a package ungated is a decision
	// someone wrote down, never an omission.
	Reason string
}

// Policy is the coverage table.
type Policy struct {
	// Targets are the gated packages.
	Targets []Target
	// Exempt are the packages deliberately left ungated. Empty today, which is
	// the intended state: every cas/ package carries a numeric gate.
	Exempt []Exemption
}

// Validate reports whether the policy is well formed. It exists because the
// table is hand-maintained: without it, a malformed row would be read as
// "no threshold" and the package it names would silently leave the gate.
func (p Policy) Validate() error {
	seen := map[string]string{}
	for _, target := range p.Targets {
		if target.Package == "" {
			return fmt.Errorf("a target row names no package")
		}
		if target.Threshold <= 0 || target.Threshold > 100 {
			return fmt.Errorf("target %s has threshold %v, which is not a percentage", target.Package, target.Threshold)
		}
		if target.Tier == "" {
			return fmt.Errorf("target %s names no tier; the tier documents the row", target.Package)
		}
		if previous, ok := seen[target.Package]; ok {
			return fmt.Errorf("package %s is listed twice (%s and %s)", target.Package, previous, target.Tier)
		}
		seen[target.Package] = target.Tier
	}
	for _, exempt := range p.Exempt {
		if exempt.Package == "" {
			return fmt.Errorf("an exemption names no package")
		}
		if exempt.Reason == "" {
			return fmt.Errorf("exemption %s gives no reason; an ungated package needs a written decision", exempt.Package)
		}
		if previous, ok := seen[exempt.Package]; ok {
			return fmt.Errorf("package %s is both gated (%s) and exempt", exempt.Package, previous)
		}
		seen[exempt.Package] = "exempt"
	}
	return nil
}

// StripModule reduces Go's import paths to the module-relative form the policy
// uses. A discovered package outside the module is an error: it can only be a
// caller passing the wrong list, and silently dropping it would let a whole tree
// escape the check.
func StripModule(modulePath string, discovered []string) ([]string, error) {
	relative := make([]string, 0, len(discovered))
	for _, pkg := range discovered {
		name := normalize(pkg)
		if name == "" {
			continue
		}
		if name == modulePath {
			relative = append(relative, name)
			continue
		}
		if !strings.HasPrefix(name, modulePath+"/") {
			return nil, fmt.Errorf("discovered package %q is not inside module %q", pkg, modulePath)
		}
		relative = append(relative, strings.TrimPrefix(name, modulePath+"/"))
	}
	return relative, nil
}

// Uncovered is the same check against a specific policy, and is what the tests
// drive.
func (p Policy) Uncovered(discovered []string) ([]string, error) {
	if err := p.Validate(); err != nil {
		return nil, fmt.Errorf("coverage policy: %w", err)
	}

	covered := make(map[string]bool, len(p.Targets)+len(p.Exempt))
	for _, target := range p.Targets {
		covered[normalize(target.Package)] = true
	}
	for _, exempt := range p.Exempt {
		covered[normalize(exempt.Package)] = true
	}

	missing := []string{}
	seen := map[string]bool{}
	for _, pkg := range discovered {
		name := normalize(pkg)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		if !covered[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing, nil
}

// Gated returns the gated targets sorted by package path. It exists so the gate
// can measure each one — running `go test -cover` is orchestration — while the list
// itself has exactly one owner. The order is stable so a gate log reads the same
// way twice, and it is the order the gate runs them in and reports them in.
func (p Policy) Gated() []Target {
	targets := append([]Target(nil), p.Targets...)
	sort.Slice(targets, func(i, j int) bool { return targets[i].Package < targets[j].Package })
	return targets
}

// Meets reports whether a measured percentage satisfies a threshold.
func Meets(measured, threshold float64) bool {
	return measured >= threshold
}

// Result is one measured package, as the gate's measurement loop produces it.
type Result struct {
	// Threshold is the percentage the policy requires.
	Threshold float64
	// Package is the package that was measured.
	Package string
	// Measured is the coverage the run reported, or -1 when it reported none — a
	// compile failure, a panic, a test binary that exited before printing, or a
	// profile holding no block for this package. It is deliberately not 0: zero
	// coverage and no coverage are different failures, and the second one means the
	// measurement never happened.
	Measured float64
}

// HasMeasurement reports whether the run produced a coverage number.
func (r Result) HasMeasurement() bool {
	return r.Measured >= 0
}

// Failure explains why one result does not satisfy the policy.
func (r Result) Failure() string {
	if !r.HasMeasurement() {
		return fmt.Sprintf("coverage output missing for %s", r.Package)
	}
	return fmt.Sprintf("coverage %g%% below %g%% for %s", r.Measured, r.Threshold, r.Package)
}

// CheckResults returns the failures among measured results: a package that
// reported no coverage, and a package below its threshold.
//
// The comparison lives here rather than in the gate's shell for the reason the
// policy does: `awk` comparing two numbers in a one-liner is a rule no test
// covers, while this is a function whose boundary case — a measurement exactly on
// the threshold passes — is pinned by TestMeets.
func CheckResults(results []Result) []string {
	failures := []string{}
	for _, result := range results {
		if !result.HasMeasurement() || !Meets(result.Measured, result.Threshold) {
			failures = append(failures, result.Failure())
		}
	}
	return failures
}

// ParseResult reads one measurement line, "<threshold>|<package>|<measured>",
// where <measured> may be empty when the run reported none. It is the format the
// gate writes, so the parsing is pinned rather than assumed.
func ParseResult(line string) (Result, error) {
	fields := strings.Split(line, "|")
	if len(fields) != 3 {
		return Result{}, fmt.Errorf("measurement %q is not <threshold>|<package>|<measured>", line)
	}
	if fields[1] == "" {
		return Result{}, fmt.Errorf("measurement %q names no package", line)
	}
	threshold, err := ParseThreshold(fields[0])
	if err != nil {
		return Result{}, err
	}
	measured := -1.0
	if text := strings.TrimSpace(fields[2]); text != "" {
		value, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return Result{}, fmt.Errorf("measurement for %s is not a number: %w", fields[1], err)
		}
		if value < 0 || value > 100 {
			return Result{}, fmt.Errorf("measurement for %s is %v, which is not a percentage", fields[1], value)
		}
		measured = value
	}
	return Result{Threshold: threshold, Package: fields[1], Measured: measured}, nil
}

// Measure reads a `go test -coverprofile` profile and reports one result per gated
// target, in the policy's own order.
//
// The profile is the run's own record — one line per basic block,
// "<file>:<span> <statements> <count>" — so a package's number is read from what Go
// wrote rather than guessed from what it printed, and one test pass can answer for
// every target instead of one run per package. A target the profile never mentions
// measured nothing, and is reported as such rather than as zero.
//
// modulePath is what the profile's file paths are prefixed with. A file that does
// not carry it belongs to another module and is not this policy's to measure; a
// profile that names blocks but not one of them inside the module is refused rather
// than reported as a run that measured nothing, because that shape means the file
// names and the module path disagree.
func (p Policy) Measure(contents, modulePath string) ([]Result, error) {
	if err := p.Validate(); err != nil {
		return nil, fmt.Errorf("coverage policy: %w", err)
	}
	parsed, err := parseProfile(contents, strings.TrimSuffix(modulePath, "/"))
	if err != nil {
		return nil, err
	}
	if parsed.blocks > 0 && len(parsed.packages) == 0 {
		return nil, fmt.Errorf(
			"the coverage profile names %d block(s) and none inside module %q; its file paths cannot be matched against the policy",
			parsed.blocks, modulePath)
	}

	results := make([]Result, 0, len(p.Targets))
	for _, target := range p.Gated() {
		result := Result{Threshold: target.Threshold, Package: target.Package, Measured: -1}
		if measured, ok := parsed.packages[normalize(target.Package)]; ok && measured.total > 0 {
			result.Measured = measured.percentage()
		}
		results = append(results, result)
	}
	return results, nil
}

// profile is a read coverage profile: one tally per package inside the module, and
// how many block lines it held in total, wherever they pointed.
type profile struct {
	packages map[string]tally
	blocks   int
}

// tally is the statements a package's blocks hold and how many of them ran.
type tally struct {
	covered int
	total   int
}

// percentage is the covered share of the statements, to the one decimal
// `go test -cover` prints: the number the decision reads and the number a reader
// sees come from the same pair, so a package cannot be reported at 90.0% and judged
// below 90.
func (t tally) percentage() float64 {
	return math.Round(float64(t.covered)/float64(t.total)*1000) / 10
}

// parseProfile reads Go's coverage profile into one tally per package inside the
// module.
//
// The format is Go's: a mode line, then one line per basic block. A file outside the
// module is counted in blocks and tallied nowhere — it cannot be a target of this
// policy, and the count is what lets Measure tell a profile it cannot read from one
// that measured nothing.
func parseProfile(contents, modulePath string) (profile, error) {
	lines := strings.Split(contents, "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "mode: ") {
		return profile{}, errors.New("the coverage profile does not start with a mode line")
	}

	parsed := profile{packages: map[string]tally{}}
	for index, line := range lines[1:] {
		number := index + 2
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return profile{}, fmt.Errorf(
				"coverage profile line %d is not \"<file>:<span> <statements> <count>\": %q", number, line)
		}
		statements, err := strconv.Atoi(fields[1])
		if err != nil || statements < 0 {
			return profile{}, fmt.Errorf(
				"coverage profile line %d holds %q statements, which is not a count", number, fields[1])
		}
		count, err := strconv.Atoi(fields[2])
		if err != nil || count < 0 {
			return profile{}, fmt.Errorf(
				"coverage profile line %d holds %q as its execution count, which is not a count", number, fields[2])
		}
		parsed.blocks++

		pkg, ok := packageOf(fields[0], modulePath)
		if !ok {
			continue
		}
		running := parsed.packages[pkg]
		running.total += statements
		if count > 0 {
			running.covered += statements
		}
		parsed.packages[pkg] = running
	}
	return parsed, nil
}

// packageOf reports the module-relative package a profile line's file belongs to,
// and whether it is inside the module at all. The file is written as
// "<import path>/<file>.go:<span>", so the package is the directory in front of the
// file name, with the module path stripped.
func packageOf(field, modulePath string) (string, bool) {
	file, _, ok := strings.Cut(field, ":")
	if !ok {
		return "", false
	}
	dir := path.Dir(file)
	if dir == "." {
		return "", false
	}
	relative, ok := strings.CutPrefix(dir, modulePath+"/")
	if !ok || relative == "" {
		return "", false
	}
	return relative, true
}

// ParseThreshold reads a percentage as the table writes it. It exists so a
// threshold that is not a number is an error a test can pin, rather than a
// comparison that quietly evaluates false.
func ParseThreshold(text string) (float64, error) {
	value, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	if err != nil {
		return 0, fmt.Errorf("threshold %q is not a number: %w", text, err)
	}
	if value <= 0 || value > 100 {
		return 0, fmt.Errorf("threshold %q is not a percentage between 0 and 100", text)
	}
	return value, nil
}

// normalize reduces a package path to its comparable form: Go's own form, with no
// leading "./", so the table may be written either way without creating a
// package that looks uncovered when it is gated.
func normalize(pkg string) string {
	return strings.TrimPrefix(strings.TrimSpace(pkg), "./")
}
