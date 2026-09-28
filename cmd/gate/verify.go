package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dmundt/go-cask/internal/build/changes"
	"github.com/dmundt/go-cask/internal/build/gate"
	"github.com/dmundt/go-cask/internal/build/policy"
	"github.com/dmundt/go-cask/internal/build/verify"
	"github.com/dmundt/go-cask/internal/build/worktree"
)

// The gate's two refusals that are about the caller's tree rather than a failed step. Both
// are printed in full because each names something a reader has to do next.
const (
	// verifyWrongTree refuses to gate a tree other than the one the command was started
	// from. A linked worktree whose `.git` link records the other toolchain's absolute
	// path makes git walk up to the primary checkout, and the gate would then verify that
	// tree while the caller believed it was verifying this one.
	verifyWrongTree = `verify: git resolved %q, but the gate was started in %q.
  The worktree's .git file holds an absolute path from the other toolchain, so
  git walks up to the primary checkout and the gate would test the WRONG tree.
  Fix it with the relative form, then re-run:
      go run ./cmd/gate worktree remove <name> && go run ./cmd/gate worktree add <name> <branch>
  (scripts/AGENT.md documents the same fix for a worktree created from WSL.)
`
	// verifyNoCompiler refuses to start the race and coverage steps, which cannot run
	// without cgo and a C compiler. Failing here names the missing piece; failing inside a
	// build names a linker whose diagnosis a reader has to translate.
	verifyNoCompiler = `CGO is required for the race/coverage gate; install a supported C compiler (gcc or clang) and retry.
On Windows, use a Go release with a supported MinGW-w64 or LLVM toolchain; MSVC may reject Go's race-build flags.
`
	// verifyVersionBump is the tail of the version-field refusal: what the gate checked,
	// and the one thing that exhausts a version bump.
	verifyVersionBump = `  Bump the frontmatter ` + "`version:`" + ` of each file above (docs/AGENT.md, "version
  starts at v1; increment by one on material change"). The gate checks that a
  bump happened, not that the change was material.
`
)

// verifyStep is one step of the gate: the heading it prints and the work it does.
type verifyStep struct {
	// Name is the step's name in the report, which is also the heading it prints.
	Name string
	// DocsScope marks the steps the documentation scope runs too. Every other step needs
	// the whole module and the race suite, which is what keeps a documentation-only
	// landing at seconds instead of minutes.
	DocsScope bool
	// Enabled reports whether the step applies to this run at all. A step without one
	// always runs. The release-note sync is the one step that has nothing to check unless
	// a tag is being released, and a heading for a check that did not happen would read as
	// a step that passed.
	Enabled func(*gateRun) bool
	// Run performs the step and returns the failure that ends the run.
	Run func(*gateRun) error
}

// gateRun is one gate run: where it is, what it covers, and what it has skipped.
type gateRun struct {
	out, errOut io.Writer
	table       policy.VerifyTable
	// root is the checkout the gate was started in.
	root  string
	jobs  int
	scope verify.Scope
	// fast is the drop-everything switch, which turns on every escape hatch at once.
	fast bool
	// skipped is what the run dropped, in the order the steps reported it. A run with
	// anything here is not a verification and writes no record.
	skipped []string
	// base and changed are what the run measured the change from and the paths it touched,
	// which the scope decision and the receipt both read.
	base    string
	changed []string
	// checks are the receipt's check names: the checks that actually completed, in the
	// order they did. A skipped step contributes nothing, because a receipt that named a
	// check which did not run would excuse CI from running it.
	checks []string
	// coverageTiers is how many packages the coverage gate covered, recorded with the
	// receipt so a reader can tell a run that measured from one that measured nothing.
	coverageTiers int
}

// runVerify runs the repository's gate.
//
// This is the step list scripts/verify.sh held in bash, moved here when the last rule in
// it turned out to be the list itself. The decisions are not in this file: what a run
// covers, how many packages it may build at once and whether an escape hatch dropped a
// step are internal/build/verify's; the entry points, the variables and the
// smoke-fuzz set are internal/build/policy's; the record it writes is
// internal/build/gate's. What is left is orchestration — running the steps in order,
// streaming their output, and reporting — which is the one thing that cannot live in the
// engine, because the engine is pure functions over caller data and this reads the world.
func runVerify(args []string, out, errOut io.Writer) error {
	mode := gateSlotQueue
	flags := flag.NewFlagSet("verify", flag.ContinueOnError)
	flags.SetOutput(errOut)
	flags.Func("slot", "how to take the local advisory slot: queue (default) waits out a live "+
		"holder and takes over a provably dead one; takeover takes any holder's slot now",
		func(value string) error {
			switch value {
			case "queue":
				mode = gateSlotQueue
			case "takeover":
				mode = gateSlotTakeover
			default:
				return fmt.Errorf("unknown slot mode %q: use queue or takeover", value)
			}
			return nil
		})
	if err := parse(flags, args); err != nil {
		return usageError{err.Error()}
	}
	return verifyGate(mode, out, errOut)
}

// verifyGate is the run itself.
//
// The whole run holds the clone's local advisory slot (go-cask#486). The slot's record
// names the acquiring process's pid, so a holder that lives for exactly one run is what
// makes the record usable for judging liveness — which is what the slot failed at when
// every lane acquired it by hand around a run. No caller can forget it here.
func verifyGate(mode gateSlotMode, out, errOut io.Writer) error {
	release, err := gateHoldSlot(mode, out, errOut)
	if err != nil {
		return err
	}
	if release != nil {
		// Every exit path releases the slot: the ordinary return below, a step's failure,
		// and a panic unwinding through it. A release that fails is reported and never
		// changes the run's verdict — the slot is advisory, and losing it must not turn a
		// green tree red.
		defer func() {
			if err := release(); err != nil {
				fmt.Fprintf(errOut, "verify: %v\n", err)
			}
		}()
	}
	return verifySteps(out, errOut)
}

// verifySteps is the gate's step list and its run, with the slot already held.
func verifySteps(out, errOut io.Writer) error {
	table := policy.Verify()
	run := &gateRun{out: out, errOut: errOut, table: table}

	root, err := repoRoot()
	if err != nil {
		return err
	}
	// The gate has to run in the checkout git resolves. `gate.sh` starts the tool from
	// the checkout that holds the script, so a mismatch means git walked up to a different
	// tree; see checkTree.
	started, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolving the working directory: %w", err)
	}
	if err := checkTree(started, root, errOut); err != nil {
		return err
	}
	run.root = root

	// A tree with no Go source is the one tree the gate has nothing to say about.
	if !hasGoSources(root) {
		fmt.Fprintln(out, "No Go sources; verification skipped.")
		return nil
	}

	// The race suite and the coverage measurement are one run and need cgo and a C
	// compiler, so refuse before spending minutes on the steps that do not.
	if os.Getenv("CGO_ENABLED") != "0" && !anyOnPath(table.Compilers) {
		fmt.Fprint(errOut, verifyNoCompiler)
		return exitStatus(1)
	}

	run.fast = verify.Escape(run.env(table.FastEnv), false)
	jobs, err := verify.Jobs(run.env(table.JobsEnv), table.JobsEnv, runtime.NumCPU())
	if err != nil {
		return usageError{err.Error()}
	}
	run.jobs = jobs

	if err := run.lockWorktrees(); err != nil {
		return err
	}

	// The scope decides which steps run, and the benchmark is the merge base with the
	// remote's main. The classification is not repeated here: internal/build/changes
	// owns the rule and internal/build/policy the pattern list, and continuous
	// integration's scope job asks the same command, so the gate and CI cannot drift apart
	// on a list kept in sync only by a comment.
	base, err := mergeBase(root)
	if err != nil {
		return err
	}
	run.base = base
	if base != "" {
		if run.changed, err = changedPaths(root, base, ""); err != nil {
			return err
		}
	}
	docsOnly := false
	if base != "" {
		docsOnly, err = scopeRule(run.changed, table.ScopeRule)
		if err != nil {
			return err
		}
	}
	scope, err := verify.Requested(run.env(table.ScopeEnv), table.ScopeEnv, docsOnly)
	if err != nil {
		return usageError{err.Error()}
	}
	run.scope = scope
	fmt.Fprintf(out, "== scope: %s ==\n", scope)
	if scope == verify.Docs {
		fmt.Fprintln(out, "documentation-only change: running the documentation gate ("+
			table.ScopeEnv+"=full runs the whole gate)")
	}

	// The version-field rule runs in both scopes: a documentation-only change is exactly
	// where the bump is owed.
	if base != "" {
		if err := run.checkVersionFields(base); err != nil {
			return err
		}
	}

	for _, step := range stepsFor(table, scope) {
		if step.Enabled != nil && !step.Enabled(run) {
			continue
		}
		run.section(step.Name)
		if err := step.Run(run); err != nil {
			return err
		}
	}

	return run.finish()
}

// stepsFor reports the steps a run of this scope performs: every step for the full scope,
// and the documentation steps alone for the documentation scope. It is separate from the
// run so the selection is a decision with a test rather than a branch inside a loop that
// only a whole gate run could exercise.
func stepsFor(table policy.VerifyTable, scope verify.Scope) []verifyStep {
	var steps []verifyStep
	for _, step := range gateSteps(table) {
		if scope == verify.Docs && !step.DocsScope {
			continue
		}
		steps = append(steps, step)
	}
	return steps
}

// checkTree refuses a run that is not in the checkout git resolves, which is the one way
// the gate can silently verify the wrong tree.
//
// A linked worktree whose `.git` link records the other toolchain's absolute path is
// unresolvable here, so git walks up to the primary checkout: `gate.sh` started the
// tool in the worktree, git reports the primary, and a gate that trusted the answer would
// verify a tree nobody asked about. That link is what distinguishes the case from a caller
// who simply started the tool in a subdirectory, which is its own, plainer refusal.
func checkTree(started, root string, errOut io.Writer) error {
	if worktree.SamePath(started, root) {
		return nil
	}
	if _, err := os.Lstat(filepath.Join(started, ".git")); err == nil {
		fmt.Fprintf(errOut, verifyWrongTree, root, started)
		return exitStatus(2)
	}
	fmt.Fprintf(errOut, "verify: run the gate from the repository root (%s), not %s.\n", root, started)
	return exitStatus(2)
}

// gateSteps is the gate's step list, in the order it runs. The steps marked for the
// documentation scope come last because they are the only ones that scope runs.
func gateSteps(table policy.VerifyTable) []verifyStep {
	return []verifyStep{
		{Name: "gofmt", Run: attested(table.Checks.Gofmt, stepGofmt)},
		{Name: "go mod tidy", Run: attested(table.Checks.ModTidy, stepModTidy)},
		{Name: "go build", Run: attested(table.Checks.Build, func(r *gateRun) error { return r.command(r.root, "go", "build", "./...") })},
		{Name: "module graph", Run: attested(table.Checks.ModuleGraph, func(r *gateRun) error { return runModuleGraph(nil, r.out, r.errOut) })},
		{Name: "go vet", Run: attested(table.Checks.Vet, func(r *gateRun) error { return r.command(r.root, "go", "vet", "./...") })},
		{Name: "cross-platform build", Run: stepCrossPlatform},
		{Name: "layer matrix check", Run: attested(table.Checks.LayerMatrix, func(r *gateRun) error { return runLayerMatrix(nil, r.out, r.errOut) })},
		// One step, two checks: the guard is one traversal of the import graph and it
		// answers for both packages, but the receipt's suite names them separately, so
		// both names are recorded.
		{Name: "codec guards", Run: attested(table.Checks.CodecGuards, func(r *gateRun) error {
			return runCodecGuards(nil, r.out, r.errOut)
		})},
		{Name: "lint", Run: stepLint},
		{Name: "govulncheck", Run: stepSecurity},
		{Name: "test -race + coverage gate", Run: stepRaceAndCoverage},
		{Name: "fuzz smoke", Run: stepFuzz},
		{Name: "doc integrity", DocsScope: true, Run: attested(table.Checks.DocIntegrity, func(r *gateRun) error { return runMarkdownIntegrity(nil, r.out, r.errOut) })},
		{Name: "package graph", DocsScope: true, Run: attested(table.Checks.PackageGraph, func(r *gateRun) error { return runDepGraph(nil, r.out, r.errOut) })},
		{Name: "website footer", DocsScope: true, Run: attested(table.Checks.WebsiteFooter, func(r *gateRun) error { return runWebsiteFooter(nil, r.out, r.errOut) })},
		{Name: "website examples", DocsScope: true, Run: attested(table.Checks.WebsiteExamples, func(r *gateRun) error { return runWebsiteExamples(nil, r.out, r.errOut) })},
		{Name: "release note sync", DocsScope: true, Enabled: releasing, Run: stepReleaseNotes},
	}
}

// section prints the heading a step is known by.
func (r *gateRun) section(name string) {
	fmt.Fprintf(r.out, "== %s ==\n", name)
}

// env reads one of the caller's gate variables.
func (r *gateRun) env(name string) string {
	return os.Getenv(name)
}

// escape reports whether an escape hatch dropped a step. When it did, it prints the
// report line and records the label, so the run is reported as incomplete and writes no
// record. The comparison itself is the engine's, because a hatch that reads as set for
// the report and unset for the record is how a half-run acquires a green stamp.
func (r *gateRun) escape(label, env string) bool {
	if !verify.Escape(r.env(env), r.fast) {
		return false
	}
	r.skipped = append(r.skipped, label)
	fmt.Fprintf(r.out, "skipped: %s (%s)\n", label, env)
	return true
}

// mark records a check as completed, under the name the gate receipt's suite lists. It is
// called after the check has actually run — never before, and never by a step that
// skipped — because the receipt is what CI reads to decide it may skip the same work.
func (r *gateRun) mark(check string) {
	r.checks = append(r.checks, check)
}

// attested wraps a step that is exactly one check: the check is recorded only when the
// step succeeded, so a name can never reach the receipt without the work behind it.
func attested(check string, run func(*gateRun) error) func(*gateRun) error {
	return func(r *gateRun) error {
		if err := run(r); err != nil {
			return err
		}
		r.mark(check)
		return nil
	}
}

// command runs one command in a directory, streaming both of its streams to the run's
// writers so a reader watches the gate work rather than waiting for it.
func (r *gateRun) command(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = r.out
	cmd.Stderr = r.errOut
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

// output runs one command in a directory and returns its two streams separately, which a
// caller needs when a failure's meaning is in one of them: `go mod tidy -diff` prints the
// drift on standard output and its own complaint on standard error.
func (r *gateRun) output(dir, name string, args ...string) (string, string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// lockWorktrees locks every linked worktree of this clone before anything else runs.
//
// `git worktree prune` deletes any registration whose admin `gitdir` cannot be resolved,
// and that link can only hold one absolute path form, so every worktree the *other*
// toolchain created looks prunable to this one — deleting the registration and with it
// the index. Prune skips locked worktrees, and locking is additive and idempotent, so the
// gate locks rather than fails: there is nothing to decide and nothing to remember. The
// quiet form reports only the worktrees it had to lock, so a run that changed nothing says
// nothing.
//
// A worktree git registers that cannot be locked — a registration whose admin directory is
// gone — is reported in the run's own output rather than made fatal: nothing is wrong with
// the tree the gate was asked to verify, and a lane cannot act on a name it never sees
// (go-cask#508).
func (r *gateRun) lockWorktrees() error {
	var locked, refused bytes.Buffer
	if err := runWorktree([]string{"lock", "--quiet"}, &locked, &refused); err != nil {
		return err
	}
	report := strings.TrimSpace(locked.String() + refused.String())
	if report == "" {
		return nil
	}
	r.section("worktree locks")
	fmt.Fprintln(r.out, report)
	return nil
}

// checkVersionFields refuses a change that moved a versioned file without moving its
// frontmatter `version:`. It judges only that a bump happened, never whether the change
// deserved one, and it collects the same set the scope decision classified.
func (r *gateRun) checkVersionFields(base string) error {
	r.section("version fields")
	var listed bytes.Buffer
	// The rule prints the paths it found and fails when it found any, so the paths are the
	// report and the error is the same finding. A failure with nothing listed is a
	// different failure — a broken revision, a missing flag — and is reported as itself.
	err := runVersionFields([]string{"--base", base, "--changed"}, &listed, r.errOut)
	if unbumped := strings.TrimSpace(listed.String()); unbumped != "" {
		fmt.Fprintln(r.errOut, "verify: versioned file changed without a version bump:")
		for _, path := range strings.Split(unbumped, "\n") {
			fmt.Fprintf(r.errOut, "  %s\n", path)
		}
		fmt.Fprint(r.errOut, verifyVersionBump)
		return exitStatus(2)
	}
	if err != nil {
		return err
	}
	// The check is recorded only when it actually judged a change set: main's suite lists
	// it, and a receipt that named it without the rule having run would excuse CI from the
	// bump check.
	r.mark(r.table.Checks.VersionFields)
	return nil
}

// stepGofmt reports every first-party file the formatting rule rejects.
//
// The file list is walked rather than handed to `gofmt -l .`, which recurses into
// everything below the root — including `.gocache` and the task worktrees' own parent
// directory. Each worktree holds a second copy of the tree, so a bare `gofmt -l .` checks
// whichever branch another session happens to be working on, and an unformatted file in
// somebody else's worktree fails this gate. One walk covers both modules: `gofmt` follows
// the filesystem, so unlike `go build` and `go test` it does reach the nested engine.
func stepGofmt(r *gateRun) error {
	files, err := goSourceFiles(r.root)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return nil
	}
	stdout, stderr, err := r.output(r.root, "gofmt", append([]string{"-l"}, files...)...)
	if err != nil {
		fmt.Fprint(r.errOut, stderr)
		return fmt.Errorf("gofmt -l: %w", err)
	}
	if strings.TrimSpace(stdout) == "" {
		return nil
	}
	fmt.Fprint(r.out, stdout)
	return errors.New("gofmt needed; run gofmt -w .")
}

// goSourceFiles lists this checkout's own Go files, as paths relative to its root,
// skipping what is not this checkout's source:
//
//   - git's own directory;
//   - the scratch directories that are not this checkout's source: `.gocache`, where the
//     gate's steps and the website's generated output live, and the task worktrees' own
//     parent — a worktree holds a second copy of every file, so walking it would check a
//     branch this run was not asked about, or the gate's own generated output;
//   - any directory that is itself a linked worktree of this repository, which carries a
//     `.git` file rather than a directory.
func goSourceFiles(root string) ([]string, error) {
	var files []string
	// The worktrees' directory comes from the policy table, so this rule follows a
	// worktree wherever that table puts it.
	worktrees := policy.Worktrees().Parent
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path == root {
				return nil
			}
			if entry.Name() == ".git" || entry.Name() == ".gocache" || entry.Name() == worktrees {
				return fs.SkipDir
			}
			if _, err := os.Lstat(filepath.Join(path, ".git")); err == nil {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, relative)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking %s: %w", root, err)
	}
	return files, nil
}

// stepModTidy reports dependency-file drift in the module.
func stepModTidy(r *gateRun) error {
	return tidyModule(r, r.root,
		"go.mod / go.sum drift detected; run go mod tidy and commit the result.")
}

// tidyModule reports drift in one module's dependency files.
func tidyModule(r *gateRun, dir, drift string) error {
	stdout, stderr, err := r.output(dir, "go", "mod", "tidy", "-diff")
	if err != nil && strings.TrimSpace(stdout) == "" {
		// A failure with no diff is the toolchain's own complaint rather than drift.
		fmt.Fprint(r.errOut, stderr)
		return fmt.Errorf("go mod tidy -diff in %s: %w", dir, err)
	}
	if strings.TrimSpace(stdout) == "" {
		return nil
	}
	fmt.Fprintln(r.errOut, drift)
	fmt.Fprint(r.errOut, stdout)
	return errors.New("dependency-file drift")
}

// stepLint runs the pinned static analyzer over the module against the repository's
// committed configuration.
//
// The layer matrix is not this step's: `layer-matrix` owns it, and the analyzer's depguard
// block only mirrors it, so a violation surfaces in an editor rather than after a whole gate
// run.
func stepLint(r *gateRun) error {
	if r.escape("lint", r.table.SkipLintEnv) {
		return nil
	}
	if err := runLint(nil, r.out, r.errOut); err != nil {
		return err
	}
	r.mark(r.table.Checks.Lint)
	return nil
}

// stepSecurity runs the pinned vulnerability scan, which continuous integration also runs
// in its own job.
//
// The receipt's suite deliberately does not list this check — CI owns the scan — so the
// name is recorded for a reader, never required of a run that skipped it.
func stepSecurity(r *gateRun) error {
	if r.escape("govulncheck", r.table.SkipSecurityEnv) {
		return nil
	}
	if err := runSecurity(nil, r.out, r.errOut); err != nil {
		return err
	}
	r.mark(r.table.Checks.Security)
	return nil
}

// stepCrossPlatform cross-builds and vets every target the CI matrix gates, so the
// failures it would find are found before the push instead of after it.
//
// It is the matrix's local twin, not its replacement, and the two gate the same set:
// neither runs a Windows, macOS or arm64 binary, because a cross-build cannot execute
// what it produces. These platforms are therefore compiled and vetted, never executed,
// anywhere (go-cask#390, docs/specs/testing-strategy.md §5). CGO is off because a
// cross-build has no C toolchain for the target, which is also what makes the check
// independent of the host's compiler.
//
// The targets are the policy table's rather than a list of this function's own: the
// workflow holds the same set because it cannot import Go, so a test in
// internal/build/policy pins the two together instead of a comment asking nicely.
//
// The targets are independent, so they run at once; each one's log is printed in the
// table's order once they are all done, so a parallel run logs the same lines in the
// same order as a serial one. The share of the caller's concurrency one target takes
// is passed on to `go` itself, so five targets at once use the machine VERIFY_JOBS
// asked for rather than five of them.
func stepCrossPlatform(r *gateRun) error {
	targets := r.table.Platforms
	width := fanWidth(r.jobs, len(targets))
	share := fanShare(r.jobs, width)
	logs := make([]string, len(targets))
	errs := make([]error, len(targets))
	fanOut(width, len(targets), func(index int) {
		logs[index], errs[index] = r.crossPlatform(targets[index], share)
	})

	for index, target := range targets {
		fmt.Fprintf(r.out, "  %s/%s\n", target.GOOS, target.GOARCH)
		fmt.Fprint(r.out, logs[index])
		if errs[index] != nil {
			return errs[index]
		}
	}
	r.mark(r.table.Checks.CrossPlatform)
	return nil
}

// crossPlatform builds and vets one target and returns the log both commands wrote.
// build comes first so vet reuses what it compiled.
func (r *gateRun) crossPlatform(target policy.PlatformTarget, share int) (string, error) {
	var log strings.Builder
	env := envWithAll([][2]string{
		{"GOOS", target.GOOS}, {"GOARCH", target.GOARCH}, {"CGO_ENABLED", "0"},
	})
	for _, args := range [][]string{
		{"build", "-p", strconv.Itoa(share), "./..."},
		{"vet", "-p", strconv.Itoa(share), "./..."},
	} {
		if err := r.commandInto(&log, r.root, env, "go", args...); err != nil {
			return log.String(), err
		}
	}
	return log.String(), nil
}

// stepHelperScripts and its step are gone, and deliberately not left in place empty: the
// behaviour tests that were shell are Go tests now, beside the port that replaced each one
// — the benchmark pair, the committed graph, the version-field rule, the advisory slot's
// atomic claim, the pull-request lane and the gate receipt — and they run under the race
// suite above. A shell helper added here would have a Go home by the repository's own rule,
// so a step that ran `./scripts/test-*.sh` would have nothing to run.

// killedStepError returns the failure the step carries when its command was killed by a
// signal, and nil when it failed on its own merits. The gate renders the diagnosis as soon
// as the command returns rather than leaving it to the step's error path, because the
// coverage measurement below the suite is what a lane reads first: a run killed here would
// otherwise end at the step's heading with nothing under it (go-cask#486).
func killedStepError(err error, step string) error {
	signal, killed := killedStep(err)
	if !killed {
		return nil
	}
	var report strings.Builder
	gateStepKilledReport(&report, step, signal)
	return errors.New(strings.TrimRight(report.String(), "\n"))
}

// stepRaceAndCoverage is the gate's one composite step: the coverage drift check, the
// race suite and the per-package measurements, with the threshold decision applied
// after the suite.
//
// The suite and the measurement are one run. `go test -race -cover` is the race suite
// with the coverage instrumentation on, and the profile it writes is the measurement:
// the packages are tested once for both verdicts instead of twice, and the profile
// attributes a number to a package by the file it came from rather than by the order a
// log happened to print. Go writes that profile even when a test in the run failed,
// which is what keeps the ordering this step exists for — one run still reports both a
// failing suite and every package that missed its tier.
func stepRaceAndCoverage(r *gateRun) error {
	// The measurement table is read first, and always: an empty one would leave the run
	// below with nothing to attribute and the gate reporting success having measured
	// nothing.
	targets, err := coverageTargets(r)
	if err != nil {
		return err
	}

	r.section("coverage tier check")
	if err := runCoverageTier(nil, r.out, r.errOut); err != nil {
		return err
	}

	// Two escape hatches, one run: either check alone still needs the pass, so it runs
	// whenever either is wanted, carrying the instrumentation only when the measurement
	// is. A measurement taken with the suite skipped keeps the run's failure out of the
	// verdict, which is what skipping the suite means.
	measure := !r.escape(fmt.Sprintf("coverage measurement (%d packages)", len(targets)), r.table.SkipCoverageEnv)
	suite := !r.escape("go test -race ./...", r.table.SkipTestsEnv)
	if !measure && !suite {
		return nil
	}

	profile := ""
	if measure {
		if profile, err = tempProfile(); err != nil {
			return err
		}
		defer os.Remove(profile)
	}

	// -p is how many packages are tested at once. Go keeps its own limit here, lower
	// than the core count on a large machine, and the gate is the one caller that
	// wants the whole machine.
	args := []string{"test", "-race", "-p", strconv.Itoa(r.jobs)}
	if profile != "" {
		args = append(args, "-coverprofile="+profile)
	}
	args = append(args, "./...")
	suiteErr := r.command(r.root, "go", args...)
	if err := killedStepError(suiteErr, "test -race + coverage gate"); err != nil {
		fmt.Fprintf(r.errOut, "%v\n", err)
	}

	var coverageErr error
	if measure {
		r.section("coverage measurement")
		if coverageErr = measureCoverage(r, profile); coverageErr == nil {
			r.coverageTiers = len(targets)
			r.mark(r.table.Checks.CoverageTiers)
		}
	}
	if suite {
		if suiteErr != nil {
			return suiteErr
		}
		r.mark(r.table.Checks.TestRace)
	}
	return coverageErr
}

// coverageTargets reads the policy's measurement table: one "<threshold>|<package>|<tier>"
// line per gated package, already sorted by package path.
func coverageTargets(r *gateRun) ([]string, error) {
	var listed bytes.Buffer
	if err := runCoverageTier([]string{"--list"}, &listed, r.errOut); err != nil {
		return nil, err
	}
	targets := splitLines(listed.String())
	if len(targets) == 0 {
		return nil, errors.New("the coverage policy listed no gated packages; " +
			"refusing to pass a gate that measures nothing")
	}
	return targets, nil
}

// tempProfile is the file the merged test pass writes its coverage profile into. It
// lives in the system temporary directory rather than in the checkout: a run that
// dropped its own scratch file into the tree it gates would be one more thing standing
// between a commit and the receipt that names it.
func tempProfile() (string, error) {
	file, err := os.CreateTemp("", "gate-coverage-*.out")
	if err != nil {
		return "", fmt.Errorf("creating the coverage profile: %w", err)
	}
	name := file.Name()
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("closing the coverage profile %s: %w", name, err)
	}
	return name, nil
}

// measureCoverage reads the profile the merged test pass wrote and hands the
// per-package measurements to the threshold decision.
//
// The targets come from the policy and the numbers from the profile, so a package the
// run never measured is reported as measuring nothing rather than as zero: zero
// coverage and no coverage are different failures, and the check says which. A profile
// that cannot be read at all is the gate's own error rather than a verdict — an
// unreadable record is not evidence that a package met its tier.
func measureCoverage(r *gateRun, profile string) error {
	contents, err := os.ReadFile(profile)
	if err != nil {
		return fmt.Errorf("reading the coverage profile %s: %w", profile, err)
	}
	module, err := modulePath()
	if err != nil {
		return err
	}
	results, err := policy.Coverage().Measure(string(contents), module)
	if err != nil {
		return err
	}

	lines := make([]string, 0, len(results))
	for _, result := range results {
		measured := ""
		if result.HasMeasurement() {
			measured = trimFloat(result.Measured)
		}
		lines = append(lines, trimFloat(result.Threshold)+"|"+result.Package+"|"+measured)
	}
	return runCoverageCheck(nil, strings.NewReader(strings.Join(lines, "\n")+"\n"), r.out, r.errOut)
}

// stepFuzz smoke-fuzzes the policy's targets for a few seconds each: the readers that
// parse what the repository and its tools hand them, from the module that owns them.
//
// Each target is a few seconds of wall time and none of them depends on another, so
// they run at once and each one's log is printed in the table's order afterwards. The
// share of the caller's concurrency one target gets is passed on as -parallel: without
// it N concurrent targets would start N × NumCPU fuzz workers, and the gate would take
// the machine N times over. The seed corpus — the corpus regression a smoke exists to
// catch — runs before any worker does, whatever the share is.
func stepFuzz(r *gateRun) error {
	if r.escape(fmt.Sprintf("fuzz smoke (%d targets)", len(r.table.Fuzz)), r.table.SkipFuzzEnv) {
		return nil
	}
	targets := r.table.Fuzz
	width := fanWidth(r.jobs, len(targets))
	share := fanShare(r.jobs, width)
	logs := make([]string, len(targets))
	errs := make([]error, len(targets))
	fanOut(width, len(targets), func(index int) {
		logs[index], errs[index] = r.fuzzTarget(targets[index], share)
	})

	for index, target := range targets {
		fmt.Fprintf(r.out, "  %s %s\n", target.Package, target.Target)
		fmt.Fprint(r.out, logs[index])
		if errs[index] != nil {
			return errs[index]
		}
	}
	r.mark(r.table.Checks.FuzzSmoke)
	return nil
}

// fuzzTarget runs one smoke-fuzz target and returns the log it wrote.
func (r *gateRun) fuzzTarget(target policy.FuzzTarget, share int) (string, error) {
	var log strings.Builder
	err := r.commandInto(&log, r.root, nil, "go", "test", "-run=^$", "-fuzz="+target.Target,
		"-fuzztime="+verifyFuzzTime, "-parallel", strconv.Itoa(share), target.Package)
	return log.String(), err
}

// commandInto runs one command and adds both of its streams to one log, which a step
// that runs several commands at once needs: two targets streaming into the run's
// writers at the same time would interleave into something no reader can follow. env
// replaces the environment; a nil env inherits the caller's.
func (r *gateRun) commandInto(log io.Writer, dir string, env []string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = log
	cmd.Stderr = log
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

// fanWidth is how many items of a step may run at once: the caller's concurrency, and
// never more items than there are.
func fanWidth(jobs, count int) int {
	width := jobs
	if width > count {
		width = count
	}
	if width < 1 {
		width = 1
	}
	return width
}

// fanShare is how much of the caller's concurrency one item may use while `width`
// items run at once. VERIFY_JOBS is what the caller allows the gate to use in total,
// so a step that fans out divides it instead of multiplying it.
func fanShare(jobs, width int) int {
	if width < 1 {
		return 1
	}
	share := jobs / width
	if share < 1 {
		share = 1
	}
	return share
}

// fanOut calls run once per index, over `width` goroutines at a time. It is the one
// place a step fans out, so every concurrent step divides the caller's concurrency the
// same way.
func fanOut(width, count int, run func(index int)) {
	work := make(chan int)
	var wg sync.WaitGroup
	for worker := 0; worker < width; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range work {
				run(index)
			}
		}()
	}
	for index := 0; index < count; index++ {
		work <- index
	}
	close(work)
	wg.Wait()
}

// envWithAll lives beside envWith in main.go: one environment filter, two shapes, so
// the cross-platform step's three variables and the scanner install's one cannot drift.

// verifyFuzzTime is how long each smoke-fuzz target runs. It is a smoke, not a campaign:
// a corpus regression reproduces in the first second, and the campaign is what a caller
// runs on demand.
const verifyFuzzTime = "5s"

// stepReleaseNotes checks that the changelog section for a tag being released yields a
// note carrying the compare link it must have (docs/specs/versioning.md §4). The body
// itself is discarded: the note is the release's, and this step only
// asks whether it can be rendered at all.
func stepReleaseNotes(r *gateRun) error {
	args := []string{"--tag", r.env(r.table.ReleaseEnv)}
	if from := r.env(r.table.ReleaseFromEnv); from != "" {
		args = append(args, "--from", from)
	}
	if err := runReleaseNotes(args, io.Discard, r.errOut); err != nil {
		return fmt.Errorf("could not generate the release notes for %s: %w",
			r.env(r.table.ReleaseEnv), err)
	}
	return nil
}

// releasing reports whether a tag is being released, which is the only case the
// release-note step has anything to check.
func releasing(r *gateRun) bool {
	return r.env(r.table.ReleaseEnv) != ""
}

// finish records the run and prints the verdict.
//
// A run that skipped a step writes nothing. The record is what authorises a push, so it
// has to mean "the whole gate ran green on this commit" and nothing weaker — otherwise a
// fast run would hand the pre-push hook a green light for a tree the race suite never saw.
func (r *gateRun) finish() error {
	skipped := strings.Join(r.skipped, ", ")
	if len(r.skipped) == 0 {
		if err := r.record(); err != nil {
			return err
		}
		if err := r.receipt(); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(r.errOut, "not verified: skipped %s\n", skipped)
		fmt.Fprintf(r.errOut, "  no gate stamp was written for %s, so .githooks/pre-push will still refuse to push it.\n",
			r.shortHead())
		fmt.Fprint(r.errOut, "  re-run without the skip options before pushing.\n")
	}

	// The closing line is the gate's contract with every document that names it: a run is
	// green only when it ends with this line.
	fmt.Fprintln(r.out, "verification passed")
	if len(r.skipped) != 0 {
		fmt.Fprintf(r.out, "note: this run skipped %s — not a verified run and not stamped\n", skipped)
	}
	return nil
}

// receipt hands CI the same evidence as the stamp, in the one place CI can read it, so a
// green local run is reused instead of repeated. The helper is still shell — it signs the
// receipt as a commit object and CI verifies that signature against a pinned list — and
// the gate's part is only to hand it the facts: this commit, the base the change was
// measured from, the scope, and the checks that actually ran.
//
// It is written only for a clean tree, and never on a runner. A receipt names the tree OF A
// COMMIT, and a run that gated uncommitted changes gated a tree no commit has; a runner
// checks out a pull request's merge commit, which nobody pushes, so a receipt for it could
// never be published by anyone.
func (r *gateRun) receipt() error {
	if r.base == "" || len(r.changed) == 0 || os.Getenv("GITHUB_ACTIONS") == "true" {
		return nil
	}
	head := r.head()
	if head == "" {
		return nil
	}

	r.section("gate receipt")
	if !r.treeIsClean() {
		fmt.Fprintf(r.out, "uncommitted changes in the working tree: no receipt for %s\n", head)
		fmt.Fprintln(r.out, "  commit the change and run the gate again to produce one")
		return nil
	}

	args := []string{"create", "--sha", head, "--base", r.base, "--scope", r.scope.String(),
		"--checks", strings.Join(r.checks, " ")}
	if r.coverageTiers > 0 {
		args = append(args, "--coverage-tiers", strconv.Itoa(r.coverageTiers))
	}
	if err := gateReceiptCommand(r.root, args, r.out, r.out, productionGateReceiptDeps()); err != nil {
		return err
	}
	fmt.Fprintln(r.out, "publish it for CI with `go run ./cmd/gate gate-receipt publish` (the pre-push hook does this)")
	return nil
}

// treeIsClean reports whether the tracked tree has no uncommitted change, which is what
// makes a receipt name a tree a commit actually has. Untracked files do not count: the
// receipt is about the commit's tree, and a scratch file is not in it.
func (r *gateRun) treeIsClean() bool {
	stdout, _, err := r.output(r.root, "git", "status", "--porcelain", "--untracked-files=no")
	return err == nil && strings.TrimSpace(stdout) == ""
}

// head is this commit, in full, as the receipt names it.
func (r *gateRun) head() string {
	out, err := gitPathIn(r.root, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return out
}

// record writes this commit's entry into the shared ledger, so a push of it needs no
// second gate run.
//
// The ledger is appended, not overwritten: every worktree of this clone shares the file,
// and a single-slot stamp let a gate run in one worktree invalidate another branch's
// verified commit and refuse its push. The write is a temp file and a rename, so a reader
// never sees a half-written ledger.
func (r *gateRun) record() error {
	commonDir, err := gitPathIn(r.root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		// No shared git directory is no place to record anything, and the shell said
		// nothing about it rather than failing a green run.
		return nil
	}
	head, err := gitPathIn(r.root, "rev-parse", "HEAD")
	if err != nil {
		return err
	}

	table := policy.Gate()
	path := filepath.Join(commonDir, table.Ledger)
	updated := gate.Append(readFileOrEmpty(path), head, r.scope.String(), time.Now(), table.LedgerKeep)
	if err := os.WriteFile(path+".tmp", []byte(updated), 0o644); err != nil {
		return fmt.Errorf("writing the gate ledger %s: %w", path, err)
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		return fmt.Errorf("replacing the gate ledger %s: %w", path, err)
	}
	return nil
}

// shortHead is this commit, abbreviated for a message.
func (r *gateRun) shortHead() string {
	out, err := gitOutputIn(r.root, "rev-parse", "--short", "HEAD")
	if err != nil {
		return "HEAD"
	}
	return strings.TrimSpace(out)
}

// mergeBase is the revision the change is measured from: the merge base with the remote's
// main. An empty answer is not an error — a lone branch with no remote has no base — and
// every caller treats it as "nothing to classify".
func mergeBase(root string) (string, error) {
	out, err := gitOutputIn(root, "merge-base", "origin/main", "HEAD")
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(out), nil
}

// scopeRule asks one change-set rule for its verdict over the paths a change touched.
func scopeRule(changed []string, name string) (bool, error) {
	results, err := changes.Classify(changed, policy.ScopeRules())
	if err != nil {
		return false, err
	}
	for _, result := range results {
		if result.Name == name {
			return result.Holds, nil
		}
	}
	return false, fmt.Errorf("no scope rule named %q", name)
}

// hasGoSources reports whether the tree carries any first-party Go source, which is the
// one tree the gate has nothing to say about.
func hasGoSources(root string) bool {
	files, err := goSourceFiles(root)
	return err == nil && len(files) != 0
}

// anyOnPath reports whether any of the names resolves to a program.
func anyOnPath(names []string) bool {
	for _, name := range names {
		if _, err := exec.LookPath(name); err == nil {
			return true
		}
	}
	return false
}
