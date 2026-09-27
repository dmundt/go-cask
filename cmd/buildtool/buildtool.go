// Command buildtool holds the repository's build decisions and runs its gate.
//
// scripts/verify.sh enforced several rules with inline bash: a `case` whose arms
// were the dependency-layer matrix, and a comm(1) comparison for the coverage
// tiers. Both are structured data plus rules — logic, not orchestration — and in
// shell they could only be exercised by running the whole gate. They live here
// instead, behind ordinary Go tests, and the shell calls this command.
//
// The step list that ran them was the last rule in that script, and it is here too:
// `verify` runs every step in order, streams its output, and writes the gate stamp. The
// decisions behind it are not in this file — what a run covers, how many packages it builds
// at once, and whether an escape hatch dropped a step are internal/build/core/verify's, and
// go-cask's answers are internal/build/policy's — so what is left is orchestration. The one
// thing that stays in shell is resolving Go itself, because PATH can only be changed in the
// caller's shell (scripts/toolchain.sh).
//
// It is developer tooling, not part of the shipped product: it is under cmd/
// because that is where this repository's command-line entry points live
// (docs/index.md, cmd/**), and nothing in cas/, gitlike/, cmd/cask/ or
// internal/web/ may import it.
//
// Usage:
//
//	go run ./cmd/buildtool verify
//	go run ./cmd/buildtool layer-matrix
//	go run ./cmd/buildtool coverage-tier
//
// Each exits 0 when the rule holds and non-zero when it does not, printing the
// offending packages, so a caller can trust the status.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dmundt/go-cask/internal/build/core/bench"
	"github.com/dmundt/go-cask/internal/build/core/changes"
	"github.com/dmundt/go-cask/internal/build/core/coverage"
	"github.com/dmundt/go-cask/internal/build/core/depgraph"
	"github.com/dmundt/go-cask/internal/build/core/deps"
	"github.com/dmundt/go-cask/internal/build/core/docs"
	"github.com/dmundt/go-cask/internal/build/core/examples"
	"github.com/dmundt/go-cask/internal/build/core/layers"
	"github.com/dmundt/go-cask/internal/build/core/release"
	"github.com/dmundt/go-cask/internal/build/core/toolchain"
	"github.com/dmundt/go-cask/internal/build/core/versioning"
	"github.com/dmundt/go-cask/internal/build/core/website"
	"github.com/dmundt/go-cask/internal/build/policy"
)

// usage is the command's own help text.
const usage = `usage: buildtool <command>

commands:
  verify               run the gate: formatting, module drift, build, vet, the engine
                       module's own suite, the layer matrix, the codec guards, the
                       vulnerability scan, the coverage tiers, the race suite and the
                       smoke fuzz, then the documentation steps. VERIFY_SCOPE selects
                       the scope, VERIFY_JOBS the concurrency, and the VERIFY_SKIP_*
                       hatches drop one expensive step each — a run that skipped
                       anything writes no gate stamp
  layer-matrix         check every package's imports against the dependency-layer
                       matrix (AGENTS.md, "Layers and citizen classes")
  coverage-tier        check that every cas/ package carries a coverage tier or a
                       written exemption (testing-strategy.md §5)
  coverage-check       read measurement lines and enforce the thresholds; used by
                       the gate's coverage loop
  markdown-integrity   check the tracked Markdown files: no raw HTML, no
                       HTML/XML/SVG fences, no dead link or file reference, and
                       the CHANGELOG structure (docs/specs/AGENT.md §9)
  website-examples     materialize every Go fence on the site, check the
                       shipped-package inventory tables, and build/vet the result
  website-footer       check the published footer's one-line contract, and run the
                       site hook's self-test that pins the rendered text
  scope                classify a change set: docs-only, Go, security-relevant or
                       website; --rule prints one verdict alone
  codec-guards         check that gitlike and cas/pack do not reach the codec
                       layer (cas-core §4.12, §7)
  security             install the pinned vulnerability scanner when the installed
                       one is not it, and run it over the module
  bench-baseline       capture benchmark output and refresh the committed reference
                       dump; --capture-only writes the capture alone
  bench-compare        capture a fresh run and diff it against a reference with
                       benchstat; never writes the committed reference
  run-examples         run the example programs that terminate on their own;
                       --list names them and the manual pair
  land-lane            the local advisory slot: status, whoami, acquire, renew,
                       release; it serializes gate runs inside one clone
  pr-lane              the server-side lane: claim, check, status, release, whoami;
                       one open pull request is one lane
  pre-push             the mechanical landing rule a push must satisfy: a green
                       gate stamp for this exact commit, plus the advisory slot note
  worktree             task worktrees both toolchains resolve: add, remove, lock,
                       list; prune refuses and says why
  module-graph         check that go list -m reports this module as the main one
  version-fields       report versioned files whose frontmatter version: did not
                       move with the change (docs/AGENT.md); --base <rev> required,
                       --all compares every tracked Markdown file, --changed the
                       files the change against --base touched
  dep-graph            render the package dependency graph and compare it with
                       docs/design/package-graph.md; --write rewrites that document
                       and is the only mode that touches it
  release-notes        print the GitHub release note for --tag <tag>: its changelog
                       section reshaped, closed with the compare link
  release              the same note, and with --publish create the GitHub release
                       from it after the tag/tree/main guards pass
`

func main() {
	err := run(os.Args[1:], os.Stdout, os.Stderr)
	if err == nil {
		return
	}
	// A usage error is the caller's mistake, not a failed rule; the two are separate
	// exit codes so a script can tell "you called me wrong" from "the tree breaks the
	// rule". A few failures have a status of their own, which the helper they replaced
	// documented and a caller may rely on — and a status with no message has already
	// said everything on stdout, so it reports the code alone.
	var usage usageError
	var status statusError
	switch {
	case errors.As(err, &usage):
		fmt.Fprintf(os.Stderr, "buildtool: %v\n", err)
		os.Exit(2)
	case errors.As(err, &status):
		if status.message != "" {
			fmt.Fprintf(os.Stderr, "buildtool: %v\n", err)
		}
		os.Exit(status.code)
	default:
		fmt.Fprintf(os.Stderr, "buildtool: %v\n", err)
		os.Exit(1)
	}
}

// run dispatches one command. It is separate from main so the command surface
// can be driven from a test without a process, and it writes only through out
// and errOut.
func run(args []string, out, errOut io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(errOut, usage)
		return errors.New("no command given")
	}

	switch args[0] {
	case "verify":
		return runVerify(args[1:], out, errOut)
	case "layer-matrix":
		return runLayerMatrix(args[1:], out, errOut)
	case "coverage-tier":
		return runCoverageTier(args[1:], out, errOut)
	case "coverage-check":
		return runCoverageCheck(args[1:], os.Stdin, out, errOut)
	case "markdown-integrity":
		return runMarkdownIntegrity(args[1:], out, errOut)
	case "website-examples":
		return runWebsiteExamples(args[1:], out, errOut)
	case "website-footer":
		return runWebsiteFooter(args[1:], out, errOut)
	case "scope":
		return runScope(args[1:], out, errOut)
	case "codec-guards":
		return runCodecGuards(args[1:], out, errOut)
	case "security":
		return runSecurity(args[1:], out, errOut)
	case "bench-baseline":
		return runBenchBaseline(args[1:], out, errOut)
	case "bench-compare":
		return runBenchCompare(args[1:], out, errOut)
	case "run-examples":
		return runRunExamples(args[1:], out, errOut)
	case "land-lane":
		return runLandLane(args[1:], out, errOut)
	case "pr-lane":
		return runPRLane(args[1:], out, errOut)
	case "pre-push":
		return runPrePush(args[1:], out, errOut)
	case "worktree":
		return runWorktree(args[1:], out, errOut)
	case "module-graph":
		return runModuleGraph(args[1:], out, errOut)
	case "version-fields":
		return runVersionFields(args[1:], out, errOut)
	case "dep-graph":
		return runDepGraph(args[1:], out, errOut)
	case "release-notes":
		return runReleaseNotes(args[1:], out, errOut)
	case "release":
		return runRelease(args[1:], out, errOut)
	case "-h", "--help", "help":
		fmt.Fprint(out, usage)
		return nil
	default:
		fmt.Fprint(errOut, usage)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

// benchDeps are the two benchmark commands' collaborators: the capture, the lookup for
// the comparison tool, and the comparison itself.
//
// They are fields rather than direct calls so a test can drive the file ownership the
// commands enforce without running a benchmark suite or requiring benchstat to be
// installed — which is what the behaviour test that used stubbed binaries became: an
// ordinary Go test.
type benchDeps struct {
	capture captureBenchmarksFunc
	find    findToolFunc
	compare compareFunc
}

// captureBenchmarksFunc runs the benchmark suite into a destination file, writing the
// raw output to the file and to the caller's stream at once — the `2>&1 | tee` the
// helper did — so a reader watches the capture and the repository keeps it.
type captureBenchmarksFunc func(root, dest string, out io.Writer) error

// findToolFunc reports where an external tool is, or that it is not installed.
type findToolFunc func(name string) (string, bool)

// compareFunc runs the comparison step over two captures.
type compareFunc func(bin, baseline, current string, out, errOut io.Writer) error

// productionBenchDeps returns the collaborators the commands use outside a test.
func productionBenchDeps() benchDeps {
	return benchDeps{capture: captureBenchmarks, find: findTool, compare: runBenchstat}
}

// captureBenchmarks is the production capture: the suite the policy names, run in the
// repository root.
func captureBenchmarks(root, dest string, out io.Writer) error {
	file, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("creating %s: %w", dest, err)
	}
	defer file.Close()

	both := io.MultiWriter(out, file)
	cmd := exec.Command("go", policy.Benchmarks().Capture...)
	cmd.Dir = root
	cmd.Stdout = both
	cmd.Stderr = both
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go %s: %w", strings.Join(policy.Benchmarks().Capture, " "), err)
	}
	return nil
}

// findTool resolves a tool on PATH.
func findTool(name string) (string, bool) {
	path, err := exec.LookPath(name)
	return path, err == nil
}

// runBenchstat runs the comparison tool over the two captures, streaming its report.
func runBenchstat(bin, baseline, current string, out, errOut io.Writer) error {
	cmd := exec.Command(bin, baseline, current)
	cmd.Stdout = out
	cmd.Stderr = errOut
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("benchstat %s %s: %w", baseline, current, err)
	}
	return nil
}

// runRunExamples runs the example programs that terminate on their own, each with the
// arguments that complete it and with its store inside one scratch root inside the
// module that is removed when the run ends: the examples resolve their package path from
// the working directory, and a run must leave the tree untouched.
//
// The manual `api` example is named and never run, because it is a two-process pair
// (examples/api/README.md).
func runRunExamples(args []string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("run-examples", flag.ContinueOnError)
	flags.SetOutput(errOut)
	list := flags.Bool("list", false, "print the examples and exit")
	// The example names are positional arguments, so this command parses for help
	// rather than through the helper that refuses a stray argument.
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return usageError{err.Error()}
	}
	names := flags.Args()

	table := policy.Examples()
	if *list {
		fmt.Fprintln(out, "automated examples:")
		for _, example := range examples.Automated(table) {
			fmt.Fprintf(out, "  %s\n", example.Name)
		}
		fmt.Fprintln(out, "manual examples:")
		for _, example := range examples.Manual(table) {
			fmt.Fprintf(out, "  %s\n", example.Name)
		}
		return nil
	}

	selected, err := examples.Select(names, table)
	if err != nil {
		return err
	}

	root, err := repoRoot()
	if err != nil {
		return err
	}
	cacheDir := filepath.Join(root, ".gocache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", cacheDir, err)
	}
	scratch, err := os.MkdirTemp(cacheDir, "run-examples-*")
	if err != nil {
		return fmt.Errorf("creating the scratch root: %w", err)
	}
	defer func() { _ = os.RemoveAll(scratch) }()

	for _, example := range selected {
		fmt.Fprintf(out, "== %s ==\n", example.Name)
		argv := []string{"run", filepath.Join(root, policy.ExamplesDir, example.Name)}
		if example.Store != "" {
			argv = append(argv, "-store", filepath.Join(scratch, example.Store))
		}
		argv = append(argv, example.Args...)

		run := exec.Command("go", argv...)
		run.Dir = scratch
		run.Stdout = out
		run.Stderr = errOut
		if err := run.Run(); err != nil {
			return fmt.Errorf("go %s: %w", strings.Join(argv, " "), err)
		}
	}

	// A run started with no names covered every automated example, so the ones it did
	// not cover are the manual ones — and a reader who ran nothing needs to know they
	// exist.
	if len(names) == 0 {
		for _, example := range examples.Manual(table) {
			fmt.Fprintf(out, "\nnote: the %s example does not terminate on its own; start it in two terminals:\n", example.Name)
			for _, command := range example.Manual {
				fmt.Fprintf(out, "  %s\n", command)
			}
		}
	}
	return nil
}

// runBenchBaseline captures benchmark output and, unless asked to capture only,
// refreshes the committed reference dump — archiving the previous one first.
func runBenchBaseline(args []string, out, errOut io.Writer) error {
	return benchBaseline(args, out, errOut, productionBenchDeps())
}

// benchBaseline is the command with its collaborators injected.
func benchBaseline(args []string, out, errOut io.Writer, deps benchDeps) error {
	table := policy.Benchmarks()

	usage := func(w io.Writer) {
		fmt.Fprintf(w, "usage: buildtool bench-baseline [out-file] [--capture-only]\n\n")
		fmt.Fprintf(w, "  out-file        where the raw capture is written; default:\n")
		fmt.Fprintf(w, "                  %s with the current UTC stamp\n", table.ArchiveDir)
		fmt.Fprintf(w, "  --capture-only  capture only: the committed %s is left untouched,\n", table.Canonical)
		fmt.Fprintf(w, "                  which is what bench-compare runs\n\n")
		fmt.Fprintf(w, "This is the only writer of %s. A refresh archives the previous dump\n", table.Canonical)
		fmt.Fprintf(w, "under %s first.\n", table.ArchiveDir)
	}

	// The options are read in any position, as the shell helper accepted them: the
	// out-file may come before or after --capture-only.
	captureOnly := false
	outFile := ""
	for _, arg := range args {
		switch {
		case arg == "--capture-only" || arg == "-capture-only":
			captureOnly = true
		case arg == "-h" || arg == "--help":
			usage(out)
			return nil
		case strings.HasPrefix(arg, "-") && arg != "-":
			usage(errOut)
			return usageError{fmt.Sprintf("unknown option: %s", arg)}
		case outFile == "":
			outFile = arg
		default:
			usage(errOut)
			return usageError{fmt.Sprintf("unexpected extra argument: %s", arg)}
		}
	}

	root, err := repoRoot()
	if err != nil {
		return err
	}
	archiveDir := filepath.Join(root, filepath.FromSlash(table.ArchiveDir))
	canonical := filepath.Join(root, filepath.FromSlash(table.Canonical))

	stamp := bench.Stamp(time.Now())
	if outFile == "" {
		outFile = filepath.Join(root, filepath.FromSlash(policy.BenchmarkArchiveName(stamp)))
	}
	for _, dir := range []string{archiveDir, filepath.Dir(outFile)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
	}

	fmt.Fprintf(out, "capturing benchmark output -> %s\n", outFile)
	if err := deps.capture(root, outFile, out); err != nil {
		return err
	}

	if captureOnly {
		fmt.Fprintf(out, "capture-only: left %s untouched\n", canonical)
		return nil
	}

	// Archive the previous reference before replacing it — never overwrite the
	// capture just written, and never overwrite an existing archive.
	if _, err := os.Stat(canonical); err == nil && !samePath(canonical, outFile) {
		archive := bench.UniqueName(archiveDir, table.Stem+"-"+stamp, table.Extension, pathTaken)
		if err := copyFile(canonical, archive); err != nil {
			return err
		}
		fmt.Fprintf(out, "previous canonical baseline archived at %s\n", archive)
	}

	if samePath(canonical, outFile) {
		fmt.Fprintf(out, "baseline saved to %s\n", outFile)
		fmt.Fprintf(out, "canonical baseline already at %s\n", canonical)
		return nil
	}
	if err := copyFile(outFile, canonical); err != nil {
		return err
	}
	fmt.Fprintf(out, "baseline saved to %s\n", outFile)
	fmt.Fprintf(out, "latest baseline updated at %s\n", canonical)
	return nil
}

// runBenchCompare captures a fresh run and diffs it against a reference.
func runBenchCompare(args []string, out, errOut io.Writer) error {
	return benchCompare(args, out, errOut, productionBenchDeps())
}

// benchCompare is the command with its collaborators injected.
func benchCompare(args []string, out, errOut io.Writer, deps benchDeps) error {
	table := policy.Benchmarks()

	usage := func(w io.Writer) {
		fmt.Fprintf(w, "usage: buildtool bench-compare [baseline-file] [current-file]\n\n")
		fmt.Fprintf(w, "  baseline-file  reference capture; default %s, else the newest\n", table.Canonical)
		fmt.Fprintf(w, "                 file in %s\n", table.ArchiveDir)
		fmt.Fprintf(w, "  current-file   fresh capture; default %s\n\n", table.Current)
		fmt.Fprintf(w, "Never writes %s; capture a new reference with\n", table.Canonical)
		fmt.Fprintf(w, "`buildtool bench-baseline`.\n")
	}

	var positional []string
	for _, arg := range args {
		switch {
		case arg == "-h" || arg == "--help":
			usage(out)
			return nil
		case strings.HasPrefix(arg, "-") && arg != "-":
			usage(errOut)
			return usageError{fmt.Sprintf("unknown option: %s", arg)}
		default:
			positional = append(positional, arg)
		}
	}
	if len(positional) > 2 {
		usage(errOut)
		return usageError{fmt.Sprintf("unexpected extra argument: %s", positional[2])}
	}

	root, err := repoRoot()
	if err != nil {
		return err
	}
	archiveDir := filepath.Join(root, filepath.FromSlash(table.ArchiveDir))
	canonical := filepath.Join(root, filepath.FromSlash(table.Canonical))

	explicit := ""
	current := ""
	if len(positional) > 0 {
		explicit = positional[0]
	}
	if len(positional) > 1 {
		current = positional[1]
	}
	if current == "" {
		current = filepath.Join(root, filepath.FromSlash(table.Current))
	}

	// Choose the baseline before anything is captured: a capture written first would
	// otherwise become its own comparison point.
	archived, err := archivedCaptures(archiveDir)
	if err != nil {
		return err
	}
	baseline, found := bench.Baseline(explicit, canonical, archived, pathExists)
	if !found {
		return fmt.Errorf("no baseline found: %s does not exist and %s holds no captures\n"+
			"  capture a reference first with: go run ./cmd/buildtool bench-baseline", canonical, table.ArchiveDir)
	}
	if !pathExists(baseline) {
		return fmt.Errorf("baseline not found: %s\n"+
			"  pass an existing capture, or generate one with: go run ./cmd/buildtool bench-baseline", baseline)
	}
	if samePath(baseline, current) {
		return fmt.Errorf("baseline and current are the same file: %s\n"+
			"  comparing a capture with itself always reports no change; pick a different baseline", baseline)
	}

	fmt.Fprintf(out, "comparing %s against %s\n", current, baseline)
	if err := os.MkdirAll(filepath.Dir(current), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(current), err)
	}
	if err := deps.capture(root, current, out); err != nil {
		return err
	}

	if identical, err := sameContent(baseline, current); err == nil && identical {
		fmt.Fprintf(errOut, "warning: %s is byte-identical to %s; the diff below compares no change\n", current, baseline)
	}

	benchstat, found := deps.find("benchstat")
	if !found {
		fmt.Fprintln(errOut, "benchstat is not installed. Compare the files manually:")
		fmt.Fprintf(errOut, "  diff -u '%s' '%s'\n", baseline, current)
		return statusError{code: 2, message: "benchstat is not installed"}
	}
	return deps.compare(benchstat, baseline, current, out, errOut)
}

// archivedCaptures lists the captures in an archive directory with their write times,
// which is the order the fallback baseline is chosen in. A directory that does not
// exist yet holds no captures rather than being an error.
func archivedCaptures(dir string) ([]bench.Capture, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}
	var captures []bench.Capture
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		captures = append(captures, bench.Capture{
			Path:    filepath.Join(dir, entry.Name()),
			ModTime: info.ModTime(),
		})
	}
	return captures, nil
}

// pathTaken reports whether a path exists, which is the question bench.UniqueName
// asks.
func pathTaken(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// pathExists reports whether a path exists, so a baseline that was named explicitly is
// reported as missing instead of being used.
func pathExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// samePath reports whether two paths name the same file. An existing pair is compared
// by identity — the `-ef` test the shell helper used — so a relative and an absolute
// spelling of one file are the same file; a pair that does not both exist is compared
// by its cleaned absolute form, which is all that can be said about a path that is not
// there.
func samePath(a, b string) bool {
	if left, err := os.Stat(a); err == nil {
		if right, err := os.Stat(b); err == nil {
			return os.SameFile(left, right)
		}
	}
	left, errLeft := filepath.Abs(a)
	right, errRight := filepath.Abs(b)
	if errLeft != nil || errRight != nil {
		return false
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

// sameContent reports whether two files hold the same bytes, which is how a comparison
// that compares no change is called out instead of being reported as a clean diff.
func sameContent(a, b string) (bool, error) {
	left, err := os.ReadFile(a)
	if err != nil {
		return false, err
	}
	right, err := os.ReadFile(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(left, right), nil
}

// copyFile writes src's bytes to dst, creating dst or replacing it. A capture is small
// enough to copy in one read, and the archive must be a complete file rather than a
// half-written one if the copy fails.
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("reading %s: %w", src, err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", dst, err)
	}
	return nil
}

// runReleaseNotes prints the GitHub release note for a tag: the changelog section
// reshaped and closed with the compare link.
func runReleaseNotes(args []string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("release-notes", flag.ContinueOnError)
	flags.SetOutput(errOut)
	tag := flags.String("tag", "", "the release tag (required)")
	fromTag := flags.String("from", "", "the tag to compare from; omitted, the previous tag is used")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *tag == "" {
		return usageError{"release-notes requires --tag <tag>"}
	}

	root, err := repoRoot()
	if err != nil {
		return err
	}
	from, err := resolveFromTag(root, *fromTag, *tag)
	if err != nil {
		return err
	}
	notes, err := notesFor(root, *tag, from)
	if err != nil {
		return err
	}
	fmt.Fprint(out, notes)
	return nil
}

// runRelease prints a tag's release note and, with --publish, creates the GitHub
// release from it. Publishing goes through the script rather than a hand-typed
// `gh release create` because of the guards, which are the reason the release
// record matches the tree.
func runRelease(args []string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("release", flag.ContinueOnError)
	flags.SetOutput(errOut)
	tag := flags.String("tag", "", "the release tag (required)")
	fromTag := flags.String("from", "", "the tag to compare from; omitted, the previous tag is used")
	publish := flags.Bool("publish", false, "create the GitHub release from the notes")
	dryRun := flags.Bool("dry-run", false, "print the notes and write or publish nothing")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *tag == "" {
		return usageError{"release requires --tag <tag>"}
	}

	root, err := repoRoot()
	if err != nil {
		return err
	}

	// Resolve the publishing tool before anything else in the publish path: the
	// guards are about the tree, and learning that `gh` is missing only after they
	// pass is a wasted diagnosis. The note generation below does not need it.
	var gh string
	if *publish && !*dryRun {
		if gh, err = ghBinary(); err != nil {
			return err
		}
	}

	from, err := resolveFromTag(root, *fromTag, *tag)
	if err != nil {
		return err
	}

	if *publish {
		state, err := publishState(root, *tag)
		if err != nil {
			return err
		}
		if err := release.ValidatePublish(*tag, state); err != nil {
			return err
		}
	}

	notes, err := notesFor(root, *tag, from)
	if err != nil {
		return err
	}
	if !*publish || *dryRun {
		fmt.Fprint(out, notes)
		return nil
	}

	cmd := exec.Command(gh, "release", "create", *tag, "--title", *tag, "--notes-file", "-", "--verify-tag")
	cmd.Stdin = strings.NewReader(notes)
	cmd.Stdout = out
	cmd.Stderr = errOut
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gh release create %s: %w", *tag, err)
	}
	return nil
}

// notesFor reads the changelog and renders the notes for a tag.
func notesFor(root, tag, fromTag string) (string, error) {
	changelog, err := os.ReadFile(filepath.Join(root, "CHANGELOG.md"))
	if err != nil {
		return "", fmt.Errorf("reading CHANGELOG.md: %w", err)
	}
	return release.Build(string(changelog), tag, fromTag)
}

// resolveFromTag returns the from-tag: the one given, or the previous tag as Git
// orders tags by version, so the compare link names the release this one follows.
func resolveFromTag(root, fromTag, tag string) (string, error) {
	if fromTag != "" {
		return fromTag, nil
	}
	listed, err := gitOutputIn(root, "tag", "--sort=-version:refname")
	if err != nil {
		return "", err
	}
	previous := release.PreviousTag(splitLines(listed), tag)
	if previous == "" {
		return "", errors.New("no previous tag found; pass --from explicitly")
	}
	return previous, nil
}

// publishState gathers what the publish guards decide on: the tree's cleanliness,
// the tag's identity, and whether it sits on main.
func publishState(root, tag string) (release.State, error) {
	var state release.State

	status, err := gitOutputIn(root, "status", "--porcelain")
	if err != nil {
		return state, err
	}
	state.Dirty = strings.TrimSpace(status) != ""

	tagRevision, err := gitOutputIn(root, "rev-list", "-n", "1", tag)
	if err != nil {
		// A tag that does not resolve is the "does not exist locally" case, not a
		// failure to ask: the guard reports it with its own message.
		return state, nil
	}
	state.TagExists = true
	state.TagRevision = strings.TrimSpace(tagRevision)

	head, err := gitOutputIn(root, "rev-parse", "HEAD")
	if err != nil {
		return state, err
	}
	state.HeadRevision = strings.TrimSpace(head)

	// `main` is required by the guards, so a missing one is a state the guard
	// reports rather than an error here.
	if _, err := gitOutputIn(root, "rev-parse", "--verify", "--quiet", "main^{commit}"); err == nil {
		state.MainExists = true
		// is-ancestor exits non-zero when the tag is not an ancestor, which is the
		// state the guard reports.
		if _, err := gitOutputIn(root, "merge-base", "--is-ancestor", tag, "main"); err == nil {
			state.TagOnMain = true
		}
	}
	return state, nil
}

// ghBinary resolves the GitHub CLI, accepting the Windows executable name so the
// command works from a POSIX shell on Windows as well as on Linux and macOS.
func ghBinary() (string, error) {
	for _, name := range []string{"gh", "gh.exe"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", errors.New("gh is required for --publish")
}

// gitOutputIn runs one `git` invocation with its working directory set to the
// repository root, so a caller does not depend on its own working directory.
func gitOutputIn(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// runDepGraph regenerates or checks docs/design/package-graph.md.
//
// The two modes are the artifact-ownership split scripts/AGENT.md requires: only
// --write touches the document, and --check — which the gate runs — regenerates
// and compares without ever writing it, so a stale document can be reported
// without a way to overwrite it.
func runDepGraph(args []string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("dep-graph", flag.ContinueOnError)
	flags.SetOutput(errOut)
	write := flags.Bool("write", false,
		"rewrite the document; without it the graph is rendered and compared, and nothing is written")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() > 0 {
		return usageError{fmt.Sprintf("unexpected argument %q", flags.Arg(0))}
	}

	root, err := repoRoot()
	if err != nil {
		return err
	}
	module, err := modulePath()
	if err != nil {
		return err
	}
	packages, err := listPackages()
	if err != nil {
		return err
	}
	// The two rules read the same `go list` shape; each declares its own type at
	// the consumer side rather than importing the other's, so the conversion is
	// explicit here.
	graphPackages := make([]depgraph.Package, 0, len(packages))
	for _, pkg := range packages {
		graphPackages = append(graphPackages, depgraph.Package{
			ImportPath: pkg.ImportPath,
			Imports:    pkg.Imports,
		})
	}
	graph := depgraph.Derive(module, graphPackages)

	docPath := filepath.Join(root, filepath.FromSlash(policy.GraphDocPath))
	committed, readErr := os.ReadFile(docPath)
	exists := readErr == nil

	// A document without a readable version starts at v1, like every generated
	// file; the current version is what the render compares against.
	version := depgraph.Version(string(committed))
	if version == "" {
		version = "v1"
	}

	if !*write {
		if exists && depgraph.Document(policy.GraphDoc(), graph, version) == string(committed) {
			fmt.Fprintf(out, "dep-graph: %s is up to date\n", policy.GraphDocPath)
			return nil
		}
		return fmt.Errorf("%s is stale: the module's local package graph changed since it was generated\n"+
			"  regenerate it with: go run ./cmd/buildtool dep-graph --write", policy.GraphDocPath)
	}

	if exists && depgraph.Document(policy.GraphDoc(), graph, version) == string(committed) {
		fmt.Fprintf(out, "dep-graph: %s is up to date — nothing written\n", policy.GraphDocPath)
		return nil
	}

	// The body changed (or the file is new), so the version moves with the
	// artifact: a new document starts at v1, an existing one moves by one.
	next := version
	if exists {
		next, err = depgraph.Bump(version)
		if err != nil {
			return fmt.Errorf("%s: %w", policy.GraphDocPath, err)
		}
	}
	if err := os.WriteFile(docPath, []byte(depgraph.Document(policy.GraphDoc(), graph, next)), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", policy.GraphDocPath, err)
	}
	fmt.Fprintf(out, "dep-graph: wrote %s\n", policy.GraphDocPath)
	return nil
}

// usageError marks an invocation error, which the gate distinguishes from a rule
// failure: the helper this replaced exited 2 for usage and 1 for a reported file,
// and the gate relies on the difference.
type usageError struct{ message string }

// Error implements error.
func (e usageError) Error() string { return e.message }

// statusError marks a failure that has its own documented exit status. The benchmark
// comparison is the case: a missing `benchstat` is neither a broken rule nor a wrong
// invocation, and the helper it replaced exited 2 for it, which a script may rely on.
// An empty message is the verdict-without-a-complaint form: `land-lane status` reports
// on stdout and carries its answer in the exit status.
type statusError struct {
	code    int
	message string
}

// Error implements error.
func (e statusError) Error() string { return e.message }

// exitStatus returns a failure whose only content is its exit status, for a command
// that has already printed its verdict.
func exitStatus(code int) error { return statusError{code: code} }

// runVersionFields reports the versioned files whose frontmatter `version:` did
// not move. It prints one path per line and fails when it found any, so a caller's
// `$(...)` sees only the paths.
func runVersionFields(args []string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("version-fields", flag.ContinueOnError)
	flags.SetOutput(errOut)
	base := flags.String("base", "", "revision to compare the versions against (required)")
	all := flags.Bool("all", false, "compare every tracked versioned file, not just the paths given")
	changed := flags.Bool("changed", false, "compare every versioned file the change against --base touched, instead of the paths given")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *base == "" {
		return usageError{"version-fields requires --base <revision>"}
	}

	root, err := repoRoot()
	if err != nil {
		return err
	}

	paths := flags.Args()
	if *all {
		tracked, err := markdownFiles()
		if err != nil {
			return err
		}
		paths = tracked
	}
	if *changed {
		touched, err := changedPaths(root, *base, "")
		if err != nil {
			return err
		}
		paths = append(paths, touched...)
	}
	if len(paths) == 0 {
		return nil
	}

	results := make([]versioning.Result, 0, len(paths))
	for _, path := range paths {
		before, err := fileAtRevision(root, *base, path)
		if err != nil {
			// A path the base revision does not contain has nothing to compare,
			// which is the rule's own boundary rather than a failure.
			results = append(results, versioning.Result{Path: path})
			continue
		}
		after, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			// A path that no longer exists on disk cannot have moved anything.
			results = append(results, versioning.Result{Path: path})
			continue
		}
		was, now := versioning.Field(before), versioning.Field(string(after))
		results = append(results, versioning.Result{
			Path:   path,
			Before: was,
			After:  now,
			// The rule applies only when the base carried a version.
			Judged: was != "",
		})
	}

	unbumped := versioning.Unbumped(results)
	for _, path := range unbumped {
		fmt.Fprintln(out, path)
	}
	if len(unbumped) != 0 {
		return fmt.Errorf("%d versioned file(s) changed without a version bump", len(unbumped))
	}
	return nil
}

// fileAtRevision returns a file's contents at a revision, or an error when the
// revision does not contain it.
func fileAtRevision(root, revision, path string) (string, error) {
	cmd := exec.Command("git", "show", revision+":"+path)
	cmd.Dir = root
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git show %s:%s: %w: %s", revision, path, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// runCodecGuards checks that the packages which must stay independent of the
// codec layer really are, transitively.
func runCodecGuards(args []string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("codec-guards", flag.ContinueOnError)
	flags.SetOutput(errOut)
	if err := parse(flags, args); err != nil {
		return err
	}

	module, err := modulePath()
	if err != nil {
		return err
	}
	dependencies := map[string][]string{}
	for _, guard := range policy.CodecGuards() {
		listed, err := goOutput("go", "list", "-deps", guard.Package)
		if err != nil {
			return err
		}
		dependencies[guard.Package] = relativePaths(module, splitLines(listed))
	}

	violations := deps.CheckCodecDeps(policy.CodecGuards(), dependencies)
	if len(violations) == 0 {
		fmt.Fprintf(out, "codec guards: %d package(s) checked, no violations\n", len(dependencies))
		return nil
	}
	for _, violation := range violations {
		fmt.Fprintln(errOut, violation)
	}
	return fmt.Errorf("%d package(s) depend on the codec layer they must not reach", len(violations))
}

// runSecurity installs the pinned vulnerability scanner when the installed one is not
// it, then runs it over the module.
//
// The pin is `internal/build/policy`'s, so the gate and CI scan with the same release
// and a result can be compared with the last one. The installed binary is trusted only
// when its own `-version` report names that release: a `govulncheck` of unknown
// provenance produces a result nobody can compare, so it is installed over.
func runSecurity(args []string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("security", flag.ContinueOnError)
	flags.SetOutput(errOut)
	if err := parse(flags, args); err != nil {
		return err
	}

	scanner := policy.Scanner()
	version := os.Getenv(scanner.VersionEnv)
	if version == "" {
		version = scanner.Version
	}

	root, err := repoRoot()
	if err != nil {
		return err
	}
	binDir, err := scannerBinDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", binDir, err)
	}

	bin, err := installedScanner(binDir, scanner.Name, version)
	if err != nil {
		return err
	}
	if bin == "" {
		fmt.Fprintf(out, "installing %s@%s into %s\n", scanner.Package, version, binDir)
		install := exec.Command("go", "install", scanner.Package+"@"+version)
		install.Dir = root
		install.Env = envWith("GOBIN", binDir)
		install.Stdout, install.Stderr = out, errOut
		if err := install.Run(); err != nil {
			return fmt.Errorf("go install %s@%s: %w", scanner.Package, version, err)
		}
		if bin, err = installedScanner(binDir, scanner.Name, version); err != nil {
			return err
		}
		if bin == "" {
			return fmt.Errorf("%s was not installed into %s", scanner.Name, binDir)
		}
	}

	scan := exec.Command(bin, "./...")
	scan.Dir = root
	scan.Stdout, scan.Stderr = out, errOut
	if err := scan.Run(); err != nil {
		return fmt.Errorf("%s ./...: %w", scanner.Name, err)
	}
	return nil
}

// scannerBinDir resolves where the scanner is installed: GOBIN from the environment,
// otherwise `go env GOBIN`, otherwise the bin directory of `go env GOPATH`. The
// result is translated only inside WSL, where the `go` on PATH may be the Windows one
// and answers with a Windows path.
func scannerBinDir() (string, error) {
	gobin := os.Getenv("GOBIN")
	if gobin == "" {
		reported, err := goOutput("go", "env", "GOBIN")
		if err != nil {
			return "", err
		}
		gobin = strings.TrimSpace(reported)
	}
	gopath := ""
	if gobin == "" {
		reported, err := goOutput("go", "env", "GOPATH")
		if err != nil {
			return "", err
		}
		gopath = strings.TrimSpace(reported)
	}

	dir := toolchain.BinDir(gobin, gopath)
	if dir == "" {
		return "", errors.New("neither GOBIN nor GOPATH resolves a bin directory")
	}
	if toolchain.NeedsMount(runtime.GOOS, kernelRelease()) {
		dir = toolchain.MountPath(dir)
	}
	return strings.ReplaceAll(dir, `\`, "/"), nil
}

// kernelRelease returns the local kernel release, which is how a WSL host identifies
// itself; an unreadable file is an empty string, and no platform but Linux reads it.
func kernelRelease() string {
	data, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return ""
	}
	return string(data)
}

// installedScanner returns the path of an installed scanner whose report names the
// wanted version, or "" when none is installed. A binary that cannot be run is
// treated as not installed, because the caller's remedy is the same: install the
// pinned one.
func installedScanner(binDir, name, version string) (string, error) {
	for _, candidate := range toolchain.Candidates(name) {
		path := filepath.Join(binDir, candidate)
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		report, err := toolOutput(path, "-version")
		if err != nil {
			continue
		}
		if toolchain.Pinned(report, name, version) {
			return path, nil
		}
	}
	return "", nil
}

// envWith returns the process environment with one variable set, replacing any
// existing value rather than appending a second copy of the name.
func envWith(name, value string) []string {
	env := os.Environ()
	kept := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if strings.HasPrefix(entry, name+"=") {
			continue
		}
		kept = append(kept, entry)
	}
	return append(kept, name+"="+value)
}

// toolOutput runs one tool invocation and returns its combined output, which is how a
// `-version` report is read: the tools in question print it to either stream.
func toolOutput(path string, args ...string) (string, error) {
	cmd := exec.Command(path, args...)
	var combined strings.Builder
	cmd.Stdout = &combined
	cmd.Stderr = &combined
	if err := cmd.Run(); err != nil {
		return combined.String(), fmt.Errorf("%s %s: %w", filepath.Base(path), strings.Join(args, " "), err)
	}
	return combined.String(), nil
}

// runModuleGraph checks that `go list -m -json all` names this module as the main
// module, so a later step reading that graph is reading this repository.
func runModuleGraph(args []string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("module-graph", flag.ContinueOnError)
	flags.SetOutput(errOut)
	if err := parse(flags, args); err != nil {
		return err
	}

	module, err := modulePath()
	if err != nil {
		return err
	}
	graph, err := goOutput("go", "list", "-m", "-json", "all")
	if err != nil {
		return err
	}
	if err := deps.CheckModuleGraph(module, graph); err != nil {
		return err
	}
	fmt.Fprintf(out, "module graph: %s is the main module\n", module)
	return nil
}

// relativePaths strips the module prefix from Go import paths, so a rule can be
// written against repository-relative names such as `cas/codec` and stay
// independent of where the module lives.
func relativePaths(module string, paths []string) []string {
	relative := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == module {
			relative = append(relative, ".")
			continue
		}
		relative = append(relative, strings.TrimPrefix(path, module+"/"))
	}
	return relative
}

// runWebsiteExamples materializes every Go fence on the published site into one
// package per block, checks the shipped-package inventory tables against the
// tree, and then builds and vets the materialized set. The build and vet are the
// point of materializing at all: a fence that does not compile is documentation
// that lies about the library.
func runWebsiteExamples(args []string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("website-examples", flag.ContinueOnError)
	flags.SetOutput(errOut)
	scratch := flags.String("scratch", ".gocache/website-examples",
		"directory the Go fences are materialized into")
	if err := parse(flags, args); err != nil {
		return err
	}

	root, err := repoRoot()
	if err != nil {
		return err
	}
	websiteRoot := filepath.Join(root, "website")
	scratchRoot := filepath.Join(root, filepath.FromSlash(*scratch))

	// The scratch tree must not survive the run, on any path out. It holds
	// generated Go files inside the module, so leaving it behind makes the very
	// next gate step fail: `gofmt -l .` walks it and reports the materialized
	// copies as unformatted. The previous shell block removed it explicitly for
	// this reason.
	defer func() { _ = os.RemoveAll(scratchRoot) }()

	pages, written, findings := website.Materialize(websiteRoot, scratchRoot)
	for _, inventory := range policy.Inventories() {
		findings = append(findings, website.CheckInventory(root, websiteRoot, inventory)...)
	}
	sort.Strings(findings)
	for _, finding := range findings {
		fmt.Fprintf(errOut, "Website example error: %s\n", finding)
	}
	if len(findings) != 0 {
		return fmt.Errorf("%d website example error(s)", len(findings))
	}

	fmt.Fprintf(out, "materialized %d Go blocks from %d Markdown pages\n", len(written), pages)
	fmt.Fprintln(out, "shipped-package inventory tables match the tree")

	// The materialized set is built and vetted here rather than returned for the
	// caller to run: a fence that does not compile is documentation that lies
	// about the library, and this command owns that verdict.
	if err := buildAndVet(scratchRoot); err != nil {
		return err
	}
	return nil
}

// buildAndVet compiles and vets the materialized website examples. A failure
// names the module and package, which is the file a reader has to fix.
func buildAndVet(dir string) error {
	for _, args := range [][]string{{"build", "./..."}, {"vet", "./..."}} {
		cmd := exec.Command("go", args...)
		cmd.Dir = dir
		var stdout, stderr strings.Builder
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("go %s on the materialized website examples: %w\n%s",
				strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
		}
	}
	return nil
}

// runScope classifies the change set against go-cask's scope rules and prints one
// "<name>=<true|false>" line per rule, in the table's order, so CI can append the
// block straight to $GITHUB_OUTPUT.
//
// With --head the set is one `git diff` between two revisions, which is what CI
// classifies for a pushed range. Without it the set is the branch's own commits
// measured from --base plus anything staged or unstaged, which is what the gate is
// about to verify. With --rule the command prints that one rule's verdict as a bare
// true/false, which is the form the gate's scope decision needs.
func runScope(args []string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("scope", flag.ContinueOnError)
	flags.SetOutput(errOut)
	base := flags.String("base", "", "the revision the change is measured from (required)")
	head := flags.String("head", "", "the revision the change ends at; omitted, the working tree and the branch's commits are measured")
	rule := flags.String("rule", "", "print only this rule's verdict, as true or false")
	if err := parse(flags, args); err != nil {
		return err
	}
	if *base == "" {
		return usageError{"scope requires --base <revision>"}
	}

	root, err := repoRoot()
	if err != nil {
		return err
	}
	paths, err := changedPaths(root, *base, *head)
	if err != nil {
		return err
	}
	results, err := changes.Classify(paths, policy.ScopeRules())
	if err != nil {
		return err
	}

	if *rule != "" {
		for _, result := range results {
			if result.Name == *rule {
				fmt.Fprintln(out, strconv.FormatBool(result.Holds))
				return nil
			}
		}
		return usageError{fmt.Sprintf("no scope rule named %q", *rule)}
	}
	for _, result := range results {
		if _, err := fmt.Fprintf(out, "%s=%t\n", result.Name, result.Holds); err != nil {
			return err
		}
	}
	return nil
}

// changedPaths collects the set a scope decision covers. With a head revision it is
// the one diff between the two revisions; without one it is the branch's commits
// measured from base, the working tree and the index — the three diffs that together
// are everything the gate verifies.
func changedPaths(root, base, head string) ([]string, error) {
	specs := [][]string{
		{"diff", "--name-only", base, "HEAD"},
		{"diff", "--name-only"},
		{"diff", "--name-only", "--cached"},
	}
	if head != "" {
		specs = [][]string{{"diff", "--name-only", base, head}}
	}

	var paths []string
	for _, spec := range specs {
		listed, err := gitOutputIn(root, spec...)
		if err != nil {
			return nil, err
		}
		paths = append(paths, splitLines(listed)...)
	}
	// A path the branch committed and then touched again appears in two of the
	// diffs, and a caller reports and judges paths, so the set is sorted and
	// deduplicated — the `| sort -u` the old shell did before passing it on.
	sort.Strings(paths)
	unique := paths[:0]
	for i, path := range paths {
		if i == 0 || path != paths[i-1] {
			unique = append(unique, path)
		}
	}
	return unique, nil
}

// runWebsiteFooter checks the published footer, which the one-line redesign (#224)
// reduced to a value mkdocs.yml declares and the site hook completes from the
// checked-out revision. Three things are checked, in the order a reader debugs them:
// the declared base line is exactly the pinned one, the machinery the redesign
// deleted has not come back, and the hook's own self-test — the authority on the
// rendered text — composes the pinned line for fixed inputs.
func runWebsiteFooter(args []string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("website-footer", flag.ContinueOnError)
	flags.SetOutput(errOut)
	if err := parse(flags, args); err != nil {
		return err
	}

	root, err := repoRoot()
	if err != nil {
		return err
	}
	footer := policy.SiteFooter()

	document, err := readFileLF(filepath.Join(root, filepath.FromSlash(footer.Config)))
	if err != nil {
		return err
	}
	base, found := website.FoldedScalar(document, footer.Spec.Key)
	if !found {
		return fmt.Errorf("%s declares no folded `%s: >-` value; the footer line has nowhere to live",
			footer.Config, footer.Spec.Key)
	}

	var findings []string
	if base != footer.Expected {
		findings = append(findings, fmt.Sprintf("%s %s = %q, want %q",
			footer.Config, footer.Spec.Key, base, footer.Expected))
	}
	// The live revision exercises the path the site actually builds with: the line
	// is composed from what git answers, and the rule reports a composition that
	// shows its revision, loses its link or invents a zone. A revision git cannot
	// answer with degrades to the base line, which is the case the line is pinned for.
	date, revision := footerRevision(root)
	findings = append(findings, website.FooterFindings(base, date, revision, footer.Spec)...)

	contents := map[string]string{}
	for _, guard := range footer.Guards {
		content, err := readFileLF(filepath.Join(root, filepath.FromSlash(guard.Path)))
		if err != nil {
			findings = append(findings, fmt.Sprintf("%s: %v", guard.Path, err))
			continue
		}
		contents[guard.Path] = content
	}
	findings = append(findings, website.CheckSourceGuards(contents, footer.Guards)...)
	findings = append(findings, website.CheckAbsentPaths(footer.RemovedPaths, func(path string) bool {
		_, err := os.Stat(filepath.Join(root, filepath.FromSlash(path)))
		return err == nil
	})...)

	for _, finding := range findings {
		fmt.Fprintf(errOut, "Website footer error: %s\n", finding)
	}
	if len(findings) != 0 {
		return fmt.Errorf("%d website footer error(s)", len(findings))
	}
	fmt.Fprintf(out, "website footer: one line, %d guard(s) honoured\n", len(footer.Guards))

	// The self-test is the authority on the rendered text, so it runs last: a
	// footer that passed the checks above still fails here if the hook's own
	// composition moved. The gate always has an interpreter; a checkout without
	// one fails rather than silently skipping the only check of the rendered line.
	python := pythonInterpreter()
	if python == "" {
		return errors.New("no usable python3/python interpreter; the footer's self-test is the only check of the rendered line")
	}
	cmd := exec.Command(python, footer.Selftest...)
	cmd.Dir = root
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w\n%s", strings.Join(footer.Selftest, " "), err, strings.TrimSpace(stderr.String()))
	}
	if !strings.Contains(stdout.String(), footer.SelftestConfirmation) {
		return fmt.Errorf("%s printed no confirmation of the pinned line:\n%s",
			strings.Join(footer.Selftest, " "), stdout.String())
	}
	fmt.Fprintf(out, "website footer: %s\n", strings.TrimSpace(stdout.String()))
	return nil
}

// footerRevision reads the checked-out revision the site's footer hook reads: one
// `git log -1 --format=%h %cs` call answers both the date and the short revision. A
// revision that cannot be read yields two empty values, which the rule renders as
// the base line rather than as a guess.
func footerRevision(root string) (date, revision string) {
	answer, err := gitOutputIn(root, "log", "-1", "--format=%h %cs")
	if err != nil {
		return "", ""
	}
	fields := strings.Fields(answer)
	if len(fields) != 2 {
		return "", ""
	}
	return fields[1], fields[0]
}

// pythonInterpreter returns the first python3/python on PATH that actually runs. A
// Windows "app execution alias" for python3 exists but only prints an error and
// exits non-zero, so a name on PATH is not enough.
func pythonInterpreter() string {
	for _, name := range []string{"python3", "python"} {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		if err := exec.Command(path, "--version").Run(); err == nil {
			return path
		}
	}
	return ""
}

// readFileLF reads a text file with its line endings normalised, so a check that
// matches a line does not depend on the checkout's autocrlf setting.
func readFileLF(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n"), nil
}

// runCoverageCheck reads "<threshold>|<package>|<measured>" lines and enforces
// the thresholds, so the comparison is a tested function rather than a shell
// one-liner. The measured field may be empty, which reports a package whose run
// produced no coverage line at all.
func runCoverageCheck(args []string, in io.Reader, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("coverage-check", flag.ContinueOnError)
	flags.SetOutput(errOut)
	if err := parse(flags, args); err != nil {
		return err
	}

	lines, err := readLines(in)
	if err != nil {
		return err
	}
	if len(lines) == 0 {
		// No measurements means the loop measured nothing, and passing here would
		// report a green coverage gate that never ran.
		return errors.New("no coverage measurements were supplied")
	}

	results := make([]coverage.Result, 0, len(lines))
	for _, line := range lines {
		result, err := coverage.ParseResult(line)
		if err != nil {
			return err
		}
		results = append(results, result)
	}

	failures := coverage.CheckResults(results)
	for _, failure := range failures {
		fmt.Fprintln(errOut, failure)
	}
	if len(failures) != 0 {
		return fmt.Errorf("%d package(s) did not meet their coverage tier", len(failures))
	}
	fmt.Fprintf(out, "coverage: %d package(s) met their tier\n", len(results))
	return nil
}

// readLines reads non-empty lines from a reader, which is how the gate hands over
// its measurements.
func readLines(in io.Reader) ([]string, error) {
	data, err := io.ReadAll(in)
	if err != nil {
		return nil, fmt.Errorf("reading coverage measurements: %w", err)
	}
	return splitLines(string(data)), nil
}

// runMarkdownIntegrity checks every tracked Markdown file: no raw HTML, no
// HTML/XML/SVG fences, and no link or file reference that resolves to nothing,
// plus the CHANGELOG structure rules. The file list is Git's, so the check
// covers exactly what is committed.
func runMarkdownIntegrity(args []string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("markdown-integrity", flag.ContinueOnError)
	flags.SetOutput(errOut)
	if err := parse(flags, args); err != nil {
		return err
	}

	root, err := repoRoot()
	if err != nil {
		return err
	}
	files, err := markdownFiles()
	if err != nil {
		return err
	}

	var findings []docs.Finding
	for _, name := range files {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		findings = append(findings, docs.CheckMarkdown(root, docs.File{
			Path:    name,
			Content: string(content),
		})...)
	}

	lines := docs.Report(findings)
	for _, line := range lines {
		fmt.Fprintf(errOut, "Markdown integrity error: %s\n", line)
	}
	if len(lines) != 0 {
		return fmt.Errorf("%d Markdown integrity error(s)", len(lines))
	}
	fmt.Fprintf(out, "markdown integrity: %d files checked\n", len(files))
	return nil
}

// markdownFiles lists the tracked Markdown files, as Git reports them, which is
// the same set the gate's other documentation steps walk.
func markdownFiles() ([]string, error) {
	output, err := gitOutput("ls-files", "*.md")
	if err != nil {
		return nil, err
	}
	return splitLines(output), nil
}

// repoRoot resolves the repository root from Git, so a link that starts with "/"
// is resolved the way the published site serves it.
func repoRoot() (string, error) {
	output, err := gitOutput("rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	root := strings.TrimSpace(output)
	if root == "" {
		return "", errors.New("git rev-parse --show-toplevel printed nothing")
	}
	return root, nil
}

// runLayerMatrix checks the import matrix over the module's real packages and
// fails when any import breaks it.
func runLayerMatrix(args []string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("layer-matrix", flag.ContinueOnError)
	flags.SetOutput(errOut)
	if err := parse(flags, args); err != nil {
		return err
	}

	module, err := modulePath()
	if err != nil {
		return err
	}
	packages, err := listPackages()
	if err != nil {
		return err
	}

	violations := layers.Check(module, policy.Matrix(), packages)
	if len(violations) == 0 {
		fmt.Fprintf(out, "layer matrix: %d packages checked, no violations\n", len(packages))
		return nil
	}
	for _, violation := range violations {
		fmt.Fprintln(errOut, violation)
	}
	return fmt.Errorf("%d import(s) break the layer matrix", len(violations))
}

// runCoverageTier checks that the coverage policy covers every cas/ package.
//
// With --list it instead prints the gated targets, one "<threshold>|<package>|<tier>"
// per line, so the gate can measure each one without keeping its own copy of the
// table (scripts/verify.sh).
func runCoverageTier(args []string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("coverage-tier", flag.ContinueOnError)
	flags.SetOutput(errOut)
	list := flags.Bool("list", false, "print the gated targets instead of checking")
	if err := parse(flags, args); err != nil {
		return err
	}

	cov := policy.Coverage()
	if *list {
		if err := cov.Validate(); err != nil {
			return err
		}
		return writeTargetList(out, cov)
	}

	discovered, err := listCasPackages()
	if err != nil {
		return err
	}
	module, err := modulePath()
	if err != nil {
		return err
	}
	relative, err := coverage.StripModule(module, discovered)
	if err != nil {
		return err
	}

	missing, err := policy.Coverage().Uncovered(relative)
	if err != nil {
		return err
	}
	if len(missing) == 0 {
		fmt.Fprintf(out, "coverage tiers: %d cas/ packages, all covered\n", len(discovered))
		return nil
	}
	for _, pkg := range missing {
		fmt.Fprintln(errOut, pkg)
	}
	return fmt.Errorf("%d cas/ package(s) carry no coverage tier and no exemption", len(missing))
}

// writeTargetList prints the gate's measurement table, one
// "<threshold>|<package>|<tier>" per line.
//
// The "./" prefix is not decoration: `go test -race -cover cas/backend/fs` fails
// with "package cas/backend/fs is not in std", so a list without it makes every
// measured package fail. The policy stores paths without the prefix, which is why
// the argument the gate receives is formed here, once, and pinned by
// TestWriteTargetList.
func writeTargetList(out io.Writer, cov coverage.Policy) error {
	for _, target := range cov.Gated() {
		if _, err := fmt.Fprintf(out, "%s|./%s|%s\n", trimFloat(target.Threshold), target.Package, target.Tier); err != nil {
			return err
		}
	}
	return nil
}

// trimFloat renders a threshold as the table writes it: "90", not "90.000000",
// because the value is a whole percentage and the gate parses this back.
func trimFloat(value float64) string {
	return strconv.FormatFloat(value, 'g', -1, 64)
}

// parse runs a FlagSet and treats help as success, because a help flag that
// exits non-zero reads as a broken tool.
func parse(flags *flag.FlagSet, args []string) error {
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	return nil
}

// modulePath reads the module path from go.mod, which is what the matrix keys
// its import prefixes on.
func modulePath() (string, error) {
	out, err := goList("list", "-m", "-f", "{{.Path}}")
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(out)
	if path == "" {
		return "", errors.New("go list -m reported an empty module path")
	}
	return path, nil
}

// listPackages asks Go for every package in the module and the imports it uses.
//
// `go list`'s .Imports omits imports that appear solely in _test.go files, which
// is the intended scope: a cas/** test may keep importing internal/test.
func listPackages() ([]layers.Package, error) {
	out, err := goList("list", "-f", `{{.ImportPath}}|{{join .Imports " "}}`, "./...")
	if err != nil {
		return nil, err
	}
	return parsePackages(out)
}

// parsePackages reads the "path|import import" shape that `go list -f` prints.
func parsePackages(list string) ([]layers.Package, error) {
	var packages []layers.Package
	for _, line := range splitLines(list) {
		path, imports, _ := strings.Cut(line, "|")
		if path == "" {
			return nil, fmt.Errorf("malformed go list line %q", line)
		}
		packages = append(packages, layers.Package{
			ImportPath: path,
			Imports:    strings.Fields(imports),
		})
	}
	return packages, nil
}

// listCasPackages asks Go for the packages under cas/, which is the tree the
// coverage policy is total over.
func listCasPackages() ([]string, error) {
	out, err := goList("list", "./cas/...")
	if err != nil {
		return nil, err
	}
	return splitLines(out), nil
}

// goOutput runs one external command and returns its standard output, failing
// with the command's own stderr so the cause is visible. The arguments are
// constant, so there is no shell to escape and no user input to quote.
func goOutput(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// goList runs one `go list` invocation.
func goList(args ...string) (string, error) {
	return goOutput("go", args...)
}

// gitOutput runs one `git` invocation.
func gitOutput(args ...string) (string, error) {
	return goOutput("git", args...)
}

// splitLines splits command output on newlines, dropping the empty tail a
// trailing newline produces so it is never read as a package.
func splitLines(list string) []string {
	if list == "" {
		return nil
	}
	lines := strings.Split(strings.TrimSuffix(list, "\n"), "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if line != "" {
			kept = append(kept, line)
		}
	}
	return kept
}
