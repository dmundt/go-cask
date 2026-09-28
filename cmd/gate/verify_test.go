package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dmundt/go-cask/internal/build/landing"
	"github.com/dmundt/go-cask/internal/build/policy"
	"github.com/dmundt/go-cask/internal/build/scope"
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
		"gofmt", "go mod tidy", "go build", "go vet",
		"layer matrix check", "codec guards", "lint", "govulncheck", "test -race + coverage gate",
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

	full := stepsFor(policy.Verify(), scope.Full)
	docs := stepsFor(policy.Verify(), scope.Docs)
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
		run := &gateRun{out: &out, errOut: &errOut, table: table, root: toplevel, scope: scope.Full}
		if err := run.finish(); err != nil {
			t.Fatalf("finish: %v", err)
		}

		ledger := readFileOrEmpty(ledgerPath(t, toplevel))
		if !landing.Verified(ledger, head) {
			t.Errorf("the ledger %q does not carry %s", ledger, head)
		}
		entry, ok := landing.ParseEntry(strings.TrimSpace(ledger))
		if !ok || entry.Scope != scope.Full.String() {
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
		run := &gateRun{out: &out, errOut: &errOut, table: table, root: toplevel, scope: scope.Full}
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
		if landing.Verified(readFileOrEmpty(ledgerPath(t, toplevel)), head) {
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
			scope: scope.Full, fast: true,
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

// TestFanOutDividesTheCallersConcurrency pins the arithmetic every concurrent step
// divides the machine with: a step may use the concurrency it was given and no more,
// and each item running at once takes an equal share of it — never zero, which would
// hand a command "-p 0".
func TestFanOutDividesTheCallersConcurrency(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		jobs, count int
		wantWidth   int
		wantShare   int
	}{
		{name: "one item takes the whole allowance", jobs: 20, count: 1, wantWidth: 1, wantShare: 20},
		{name: "the allowance caps the width", jobs: 3, count: 15, wantWidth: 3, wantShare: 1},
		{name: "a wider allowance is divided evenly", jobs: 32, count: 8, wantWidth: 8, wantShare: 4},
		{name: "a serial caller stays serial", jobs: 1, count: 5, wantWidth: 1, wantShare: 1},
		{name: "an allowance below the item count never reaches zero", jobs: 2, count: 5, wantWidth: 2, wantShare: 1},
		{name: "no items still runs one worker", jobs: 20, count: 0, wantWidth: 1, wantShare: 20},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			width := fanWidth(test.jobs, test.count)
			if width != test.wantWidth {
				t.Errorf("fanWidth(%d, %d) = %d, want %d", test.jobs, test.count, width, test.wantWidth)
			}
			if share := fanShare(test.jobs, width); share != test.wantShare {
				t.Errorf("fanShare(%d, %d) = %d, want %d", test.jobs, width, share, test.wantShare)
			}
		})
	}

	if share := fanShare(20, 0); share != 1 {
		t.Errorf("fanShare with no width = %d, want 1 so a command never gets \"-p 0\"", share)
	}
}

// TestFanOutRunsEveryIndexOnce pins that the fan-out is a work-sharing loop rather than
// a batch split: every index runs exactly once, whatever the width, so a step cannot
// skip a target because another one was still running.
func TestFanOutRunsEveryIndexOnce(t *testing.T) {
	t.Parallel()

	const count = 17
	var (
		mutex sync.Mutex
		seen  []int
	)
	fanOut(3, count, func(index int) {
		mutex.Lock()
		defer mutex.Unlock()
		seen = append(seen, index)
	})

	if len(seen) != count {
		t.Fatalf("fanOut ran %d of %d items", len(seen), count)
	}
	runs := map[int]int{}
	for _, index := range seen {
		runs[index]++
	}
	for index := 0; index < count; index++ {
		if runs[index] != 1 {
			t.Errorf("index %d ran %d times, want once", index, runs[index])
		}
	}
}

// fakeGateSlot is a slot a test owns: it says whether this process already holds the slot,
// decides a scripted holder with the engine's own rule, and records whether it was released.
//
// The decision is the engine's rather than a scripted answer, so a test drives the gate's
// acquisition — the wait, the takeover and the refusal — while `internal/build/landing` stays
// the one owner of what may be done with a holder.
type fakeGateSlot struct {
	heldValue bool
	// pending is the holder the slot holds, nil when it is free.
	pending *landing.Holder
	// holderNow is what the slot reports after the claim, so a test can describe a
	// takeover race the claim lost.
	holderNow *landing.Holder
	// staleMinutes and deadGrace are the idle window and the grace the decision reads.
	staleMinutes int
	deadGrace    int
	// who is this worktree's identity, and mine its outstanding token.
	who  string
	mine string
	// takesOver counts the evictions the claim performed, so the record a real takeover
	// leaves can be pinned without a slot directory.
	takesOver int
	releases  int
	resolved  string
}

func (s *fakeGateSlot) resolve(label string) error {
	s.resolved = label
	return nil
}

func (s *fakeGateSlot) held() bool { return s.heldValue }

func (s *fakeGateSlot) holder() *landing.Holder { return s.holderNow }

func (s *fakeGateSlot) release() error {
	s.releases++
	return nil
}

func (s *fakeGateSlot) claim(force, staleDead bool) (gateClaim, error) {
	decision := landing.Decide(s.pending, s.who, s.mine,
		time.Duration(s.staleMinutes)*time.Minute, time.Duration(s.deadGrace)*time.Second,
		force, staleDead, landing.LivenessOf(derefHolder(s.pending), currentHost(), processStartFunc), now())
	switch decision.Outcome {
	case landing.SlotAlreadyMine, landing.SlotRefusedSameIdentity, landing.SlotRefusedFresh:
		return gateClaim{holder: &decision.Found}, nil
	case landing.SlotUnreadable:
		return gateClaim{}, nil
	}
	if decision.Outcome.TakesOver() {
		s.takesOver++
	}
	s.pending = nil
	return gateClaim{held: true}, nil
}

// gateRunWithSlot drives one whole gate run against a slot a test owns, restoring the real
// one when it ends. The gate run it drives is the real one, so the acquisition, the report
// and the release are the code the command runs.
func gateRunWithSlot(t *testing.T, mode gateSlotMode, slot gateSlot) (stdout, stderr string, err error) {
	t.Helper()
	previous := gateSlotFor
	gateSlotFor = func() gateSlot { return slot }
	t.Cleanup(func() { gateSlotFor = previous })

	var out, errOut bytes.Buffer
	err = verifyGate(mode, &out, &errOut)
	return out.String(), errOut.String(), err
}

// slotRepo builds a repository the gate will work on: `hookRepo` carries no Go source, and a
// tree with none is the one tree the gate answers "verification skipped" for, which would let
// a slot test pass without the gate ever reaching its steps.
func slotRepo(t *testing.T) string {
	t.Helper()
	root, _ := hookRepo(t)
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("writing the fixture's Go source: %v", err)
	}
	return root
}

// TestVerifyGateHoldsTheAdvisorySlotForTheWholeRun pins go-cask#486's first fix: the gate
// takes the local advisory slot for its whole run, so the slot's record names a pid that is
// alive for exactly as long as the run. Every lane used to take the slot by hand around a
// run, one lane's holder looked dead, a second gate started, and both runs died at the
// coverage step with nothing to read.
//
// The run is stopped at its scope decision — after the slot, before its first step — by a
// scope name no run accepts, so the gate's own acquisition and release are exercised without
// a toolchain and without a suite.
func TestVerifyGateHoldsTheAdvisorySlotForTheWholeRun(t *testing.T) {
	t.Chdir(slotRepo(t))
	t.Setenv(policy.Verify().ScopeEnv, "no-such-scope")

	for _, tc := range []struct {
		name      string
		held      bool
		wantClaim bool
	}{
		{name: "a free slot is claimed and released", held: false, wantClaim: true},
		{name: "a slot this process already holds is neither claimed nor released", held: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			slot := &fakeGateSlot{heldValue: tc.held, takesOver: 0}
			out, errOut, err := gateRunWithSlot(t, gateSlotQueue, slot)
			// The run stops at its scope decision, which is after the slot it is here to
			// exercise and before its first step.
			if err == nil {
				t.Fatalf("a scope no run accepts was accepted\n%s", errOut)
			}
			if tc.wantClaim {
				if !strings.Contains(out, "holding the land-lane slot for this gate run") {
					t.Errorf("the run did not report the slot it took:\n%s", out)
				}
				if slot.releases != 1 {
					t.Errorf("the run released the slot %d times, want exactly once", slot.releases)
				}
				return
			}
			// The reentrancy guard: claiming a slot this very process holds either
			// deadlocks or hands the outer run's slot away, so neither happens.
			if slot.releases != 0 {
				t.Errorf("a run that already held the slot released it %d times", slot.releases)
			}
			if strings.Contains(out, "holding the land-lane slot") {
				t.Errorf("a run that already held the slot claimed it again:\n%s", out)
			}
		})
	}
}

// TestVerifyGateRefusesWhileAnotherRunHoldsTheSlot pins the rule the slot exists for
// (coordination.md §5): a run that cannot take the slot says it is waiting, and a holder
// still there when the bounded wait ends is refused with its own status — an operational
// answer, never a red gate that names a step the run never reached.
func TestVerifyGateRefusesWhileAnotherRunHoldsTheSlot(t *testing.T) {
	root, _ := hookRepo(t)
	t.Chdir(root)
	// The wait window is the slot's own; a test cannot spend ten minutes in it.
	t.Setenv(policy.LandLane().WaitEnv, "0")

	holder := &landing.Holder{
		PID: "4242", Since: now(), Label: "verify /somewhere/else abc1234",
		Who: "clone#primary:wt-other#main", Token: "tok", Host: "africa",
	}
	slot := &fakeGateSlot{
		pending: holder, staleMinutes: 90, deadGrace: 60,
		who: "clone#primary:wt-mine#main", mine: landing.NoneToken,
	}
	out, errOut, err := gateRunWithSlot(t, gateSlotQueue, slot)

	var status statusError
	if !errors.As(err, &status) || status.code != 3 {
		t.Fatalf("a run that could not take the slot = %v, want the documented status 3\n%s", err, errOut)
	}
	for _, want := range []string{"waiting for", "verify /somewhere/else abc1234", "idle 0m"} {
		if !strings.Contains(out, want) {
			t.Errorf("the wait was not reported: %q does not carry %q", out, want)
		}
	}
	if !strings.Contains(err.Error(), "NOT a verdict on the tree") {
		t.Errorf("the refusal %q does not say it is not a verdict", err)
	}
	if !strings.Contains(err.Error(), "land-lane wait") {
		t.Errorf("the refusal %q does not say how to queue for the slot", err)
	}
	if slot.releases != 0 {
		t.Errorf("a run that took no slot released one %d times", slot.releases)
	}
	// Nothing was verified: the run never reached its first step, so a lane that reads
	// this cannot misattribute it to the tree.
	if strings.Contains(out, "== scope:") {
		t.Errorf("the refused run started gating:\n%s", out)
	}
}

// TestVerifyGateTakesOverADeadHoldersSlot pins the other half of the bounded wait: a holder
// whose process is provably gone is taken over at once, because waiting out a window nobody
// will ever renew holds the clone's whole queue for a run that cannot finish.
func TestVerifyGateTakesOverADeadHoldersSlot(t *testing.T) {
	t.Chdir(slotRepo(t))
	t.Setenv(policy.Verify().ScopeEnv, "no-such-scope")

	// A pid no process holds, on this host, stamped now: provably gone, past a zero-second
	// grace. Only a POSIX host carries a process start time, and the engine refuses to call
	// a holder dead anywhere else — so the case has nothing to assert on Windows.
	if _, ok := processStartFunc("99999999"); !ok {
		t.Skip("this host cannot report process start times, so no holder is provably gone")
	}
	dead := &landing.Holder{
		PID: "99999999", Since: now(), Label: "verify /gone", Who: "clone#primary:wt-gone#main",
		Token: "tok", Host: currentHost(), Start: "1",
	}
	slot := &fakeGateSlot{
		pending: dead, staleMinutes: 90, deadGrace: 0,
		who: "clone#primary:wt-mine#main", mine: landing.NoneToken,
	}
	out, errOut, err := gateRunWithSlot(t, gateSlotQueue, slot)
	if err == nil {
		t.Fatalf("a scope no run accepts was accepted\n%s", errOut)
	}
	if !strings.Contains(out, "holding the land-lane slot for this gate run") {
		t.Errorf("the run did not take the dead holder's slot:\n%s", out)
	}
	if slot.takesOver == 0 {
		t.Error("the dead holder's slot was granted without the eviction the engine's rule asks for")
	}
	if slot.releases != 1 {
		t.Errorf("the run released the slot %d times, want exactly once", slot.releases)
	}
	if strings.Contains(out, "waiting for") {
		t.Errorf("the run waited for a holder whose process is gone:\n%s", out)
	}
}

// TestVerifyGateReleasesTheSlotWhenItFails pins the release on the failure path: a run that
// stops at a step must not leave the clone's slot held by a process that has already
// finished, or every later run waits out a window for nobody.
func TestVerifyGateReleasesTheSlotWhenItFails(t *testing.T) {
	t.Chdir(slotRepo(t))
	// An unreadable scope stops the run before its first step, which is the last thing this
	// test needs a toolchain for.
	t.Setenv(policy.Verify().ScopeEnv, "not-a-scope")

	slot := &fakeGateSlot{}
	_, errOut, err := gateRunWithSlot(t, gateSlotQueue, slot)
	if err == nil {
		t.Fatalf("an invalid %s was accepted", policy.Verify().ScopeEnv)
	}
	if slot.releases != 1 {
		t.Errorf("a failed run released the slot %d times, want exactly once\n%s", slot.releases, errOut)
	}
}

// TestGateStepKilledNamesTheSignal pins the diagnosis go-cask#486 asks for: a step killed by
// a signal is reported as a kill — the signal, how many concurrent verify processes this
// host can see, and the advice to hold the slot — instead of exiting 15 with an empty tail.
func TestGateStepKilledNamesTheSignal(t *testing.T) {
	killed := fakeExitError("KILL")
	if killed == nil {
		t.Log("this platform carries no signal in a process status; the kill path has nothing to read here")
	} else if signal, ok := killedStep(killed); !ok || signal != "SIGKILL" {
		t.Errorf("a SIGKILL-killed command = (%q, %t), want SIGKILL", signal, ok)
	}
	if signal, ok := killedStep(errors.New("go test: exit status 1")); ok {
		t.Errorf("an ordinary failure was read as a kill by %s", signal)
	}
	if signal, ok := killedStep(nil); ok {
		t.Errorf("a successful command was read as a kill by %s", signal)
	}

	previous := gateVerifyProcessesFunc
	gateVerifyProcessesFunc = func() int { return 2 }
	t.Cleanup(func() { gateVerifyProcessesFunc = previous })

	var report bytes.Buffer
	gateStepKilledReport(&report, "test -race + coverage gate", "SIGKILL")
	for _, want := range []string{
		"the test -race + coverage gate step was killed by SIGKILL",
		"concurrent `gate verify` processes visible on this host: 2",
		"land-lane wait",
		"Nothing is wrong with",
	} {
		if !strings.Contains(report.String(), want) {
			t.Errorf("the diagnosis %q does not carry %q", report.String(), want)
		}
	}

	// The step carries the same diagnosis as its own failure, so the path that returns it
	// prints the signal, the count and the advice rather than a bare exit status.
	if killed == nil {
		return
	}
	stepErr := killedStepError(killed, "test -race + coverage gate")
	if stepErr == nil {
		t.Fatal("a killed step carried no diagnosis")
	}
	for _, want := range []string{"killed by SIGKILL", "land-lane wait", "processes visible on this host"} {
		if !strings.Contains(stepErr.Error(), want) {
			t.Errorf("the step's diagnosis %q does not carry %q", stepErr, want)
		}
	}
}

// TestKilledStepErrorIsSilentForAnOrdinaryFailure pins the other side of the diagnosis: the
// gate reports a step's own failure and never dresses it as a kill.
func TestKilledStepErrorIsSilentForAnOrdinaryFailure(t *testing.T) {
	if err := killedStepError(errors.New("exit status 1"), "lint"); err != nil {
		t.Errorf("an ordinary failure was diagnosed as a kill: %v", err)
	}
	if err := killedStepError(nil, "lint"); err != nil {
		t.Errorf("a successful step was diagnosed as a kill: %v", err)
	}
}

// TestIsVerifyCommandReadsAProcessArgv pins the counting rule the diagnosis reports with:
// only a gate invocation whose next argument is `verify` is a concurrent gate run, so
// the number a lane reads counts runs rather than every Go process on the host.
func TestIsVerifyCommandReadsAProcessArgv(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		want bool
	}{
		{argv: []string{"/tmp/go-build1/b001/exe/gate", "verify"}, want: true},
		{argv: []string{"go", "run", "./cmd/gate", "verify"}, want: true},
		{argv: []string{`C:\Users\x\gate.exe`, "verify"}, want: true},
		{argv: []string{"/tmp/go-build1/b001/exe/gate", "lint"}, want: false},
		{argv: []string{"go", "run", "./cmd/gate"}, want: false},
		{argv: []string{"/usr/bin/ps", "-ef"}, want: false},
		{argv: nil, want: false},
	} {
		if got := isVerifyCommand(tc.argv); got != tc.want {
			t.Errorf("isVerifyCommand(%q) = %t, want %t", tc.argv, got, tc.want)
		}
	}
}

// TestMeasureCoverageReadsTheRunsOwnRecord pins where a measurement comes from now that
// the race suite and the measurement are one pass: the profile the run wrote, matched
// against the policy's own targets. A package the profile never mentions is reported as
// unmeasured — the failure the gate must not confuse with zero coverage — and a profile
// that cannot be read is the gate's error rather than a verdict.
func TestMeasureCoverageReadsTheRunsOwnRecord(t *testing.T) {
	t.Parallel()

	module, err := modulePath()
	if err != nil {
		t.Fatalf("modulePath: %v", err)
	}
	profile := filepath.Join(t.TempDir(), "coverage.out")
	// One block for one gated package, never executed: 0% against the 90 tier.
	contents := "mode: atomic\n" + module + "/cas/digest.go:10.2,12.4 4 0\n"
	if err := os.WriteFile(profile, []byte(contents), 0o644); err != nil {
		t.Fatalf("writing the profile: %v", err)
	}

	var out, errOut bytes.Buffer
	if err := measureCoverage(&gateRun{out: &out, errOut: &errOut}, profile); err == nil {
		t.Fatal("a profile measuring nothing met every tier, want a failure")
	}
	for _, want := range []string{
		"coverage 0% below 90% for cas",
		"coverage output missing for",
	} {
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("the measurement report %q does not carry %q", errOut.String(), want)
		}
	}

	t.Run("a profile that cannot be read is the gate's error", func(t *testing.T) {
		t.Parallel()
		var out, errOut bytes.Buffer
		err := measureCoverage(&gateRun{out: &out, errOut: &errOut}, filepath.Join(t.TempDir(), "absent.out"))
		if err == nil {
			t.Fatal("a missing profile was read as a clean run, want an error")
		}
		if !strings.Contains(err.Error(), "reading the coverage profile") {
			t.Errorf("the error %q does not name the read that failed", err)
		}
	})
}
