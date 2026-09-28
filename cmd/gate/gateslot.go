package main

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dmundt/go-cask/internal/build/landing"
	"github.com/dmundt/go-cask/internal/build/policy"
)

// gateSlotName names the slot in the gate's own reports. The slot is `land-lane`'s
// (`internal/build/policy`'s table says where it lives); this is only the word the gate
// prints when it takes, waits for or releases it.
const gateSlotName = "land-lane"

// gateSlot is the local advisory slot as the gate uses it: acquired once for a whole run
// and freed when the run ends. It is an interface because a gate run holds it for minutes
// and because a test has to drive the three things the gate does with it — claim, report
// who it is waiting for, and release — without a repository and without a second gate run
// to contend with.
//
// The slot is advisory to the push and never a condition for it (coordination.md §5): it
// keeps two gate runs in ONE clone from overlapping, and a run that cannot take it waits
// or says it is waiting rather than gating alongside the holder.
type gateSlot interface {
	// resolve locates the slot and gives this run its label and identity.
	resolve(label string) error
	// held reports whether this process already holds the slot. It is the in-process
	// guard: the record was written by this very command, so re-claiming it would wait
	// for a holder that is this process. A hard kill leaves the token behind, but the
	// record's pid is then not this one, so it never reports true for a dead run's slot.
	held() bool
	// claim takes a free or expired slot, takes over a provably gone holder when
	// staleDead is set, takes any holder when force is set, and otherwise reports the
	// holder it would not evict.
	claim(force, staleDead bool) (gateClaim, error)
	// holder returns the current holder, or nil when the slot is free.
	holder() *landing.Holder
	// release frees the slot this process holds, and does nothing when it holds none.
	release() error
}

// gateSlotMode is how a run asks for the slot. The modes are distinct because they are
// three different questions about another run's claim, and the safe one is the default.
type gateSlotMode int

const (
	// gateSlotQueue waits out a live holder and takes over a provably dead one. It is the
	// default: a dead holder's run can never finish, so waiting its idle window out would
	// hold the clone's queue for a run nobody is going to end.
	gateSlotQueue gateSlotMode = iota
	// gateSlotTakeover takes any holder's slot at once, whatever its idle time. It is for
	// the session that knows the recorded holder is gone — a clone whose pid was recycled,
	// a report it can see is stale — and it is the one mode that evicts a live run.
	gateSlotTakeover
)

// gateSlotFor builds the production slot. It is a variable so a test can replace it and
// drive the gate's own paths — the acquisition, the wait and the release — while the real
// claim's concurrency is already pinned by landlane_test.go.
var gateSlotFor = func() gateSlot { return &gateLaneSlot{} }

// gateLaneSlot is the production gateSlot: the land-lane slot plus the in-process guard.
//
// gateMutex makes the guard a serialization point rather than a flag. Two gate runs cannot
// meaningfully overlap in one process — the second would contend for the machine with the
// first while holding a slot it believes is its own — so it waits for the first to
// release. The token is the on-disk half of the same guard: a hard kill leaves it behind,
// which is why a later process cannot take over a fresh holder on the strength of the
// token alone.
type gateLaneSlot struct {
	lane *landLaneSlot
}

var gateMutex sync.Mutex

// resolve locates the slot for this clone and labels this run.
func (s *gateLaneSlot) resolve(label string) error {
	slot, err := resolveLandLane("")
	if err != nil {
		return err
	}
	slot.label = label
	s.lane = slot
	return nil
}

// held reports whether this process already holds the slot. The lock is taken and released
// for the read, so this process's judge-and-claim is serialized against its own runs.
func (s *gateLaneSlot) held() bool {
	gateMutex.Lock()
	defer gateMutex.Unlock()
	if s.lane == nil {
		return false
	}
	holder := s.lane.readSafe()
	return holder != nil && s.lane.holds(*holder)
}

// gateClaim is what one non-blocking attempt at the slot produced: whether this process
// now holds it, and — when it does not and the reason is another holder — who that holder
// is. The holder is reported as data rather than as an error to parse, because what the
// caller does next depends on WHO holds the slot, and a message is not an answer.
type gateClaim struct {
	held   bool
	holder *landing.Holder
}

// claim takes the slot through the same decision every other acquisition uses.
func (s *gateLaneSlot) claim(force, staleDead bool) (gateClaim, error) {
	gateMutex.Lock()
	defer gateMutex.Unlock()
	if s.lane == nil {
		return gateClaim{}, fmt.Errorf("the %s slot has not been resolved", gateSlotName)
	}
	return s.lane.claimForGate(s.lane.label, force, staleDead)
}

// holder reports the current holder.
func (s *gateLaneSlot) holder() *landing.Holder {
	gateMutex.Lock()
	defer gateMutex.Unlock()
	if s.lane == nil {
		return nil
	}
	return s.lane.readSafe()
}

// release frees the slot this process holds. It is safe to call on every exit path: a slot
// another process holds is left alone rather than freed, which is what a killed run whose
// pid was recycled must not do to its successor.
func (s *gateLaneSlot) release() error {
	gateMutex.Lock()
	defer gateMutex.Unlock()
	if s.lane == nil {
		return nil
	}
	holder := s.lane.readSafe()
	if holder == nil {
		return nil
	}
	if !s.lane.holds(*holder) {
		return nil
	}
	aside, taken := s.lane.takeAside()
	if !taken {
		return fmt.Errorf("land lane: the slot changed hands while releasing")
	}
	if err := os.Remove(aside); err != nil && !os.IsNotExist(err) {
		// The slot is already out of the way; putting the record back is worse than
		// reporting the leftover file, so the removal is reported as itself.
		return fmt.Errorf("releasing the %s slot: %w", gateSlotName, err)
	}
	return nil
}

// readSafe returns the slot's holder with no side effect, nil when it is free or when
// there is no slot to read.
func (s *landLaneSlot) readSafe() *landing.Holder {
	if s == nil {
		return nil
	}
	return s.read()
}

// gateHoldSlot acquires the advisory slot for one whole gate run and returns the release.
//
// It is the fix for the silent death in go-cask#486: the slot's record names the acquiring
// process's pid, and a gate that holds the slot for its whole run leaves that pid alive
// for exactly as long as the run, so the liveness question answers itself and a second run
// in the clone finds a live holder instead of a dead-looking one.
//
// The outcomes are deliberately different:
//   - already held by this very process: proceed without claiming and without releasing,
//     because the holder is this command and freeing it would hand away the outer run's
//     slot;
//   - free, expired, or held by a provably gone process past the grace: claim it;
//   - held by a live process: wait for it, bounded by the slot's wait window, saying that
//     it is waiting;
//   - still held when the wait ends: refuse with gateSlotRefusal, which is a readable
//     operational answer rather than a silent exit 15.
//
// A slot that cannot be resolved at all is the caller's to report: a repository without a
// shared git directory has no slot to hold.
func gateHoldSlot(mode gateSlotMode, out, errOut io.Writer) (gateRelease, error) {
	slot := gateSlotFor()
	if err := slot.resolve(gateRunLabel()); err != nil {
		return nil, err
	}
	if slot.held() {
		return nil, nil
	}

	// The wait is bounded by the slot's own window — a gate run's length rather than an
	// arbitrary timeout — and the deadline is read once so the report and the wait agree.
	window := slotWaitSeconds()
	deadline := time.Now().Add(time.Duration(window) * time.Second)
	fmt.Fprintf(out, "land lane: acquiring the %s slot for this gate run (bounded wait %ds)\n",
		gateSlotName, window)

	announced := false
	for attempt := 0; attempt < gateSlotAttempts; attempt++ {
		claim, err := slot.claim(mode == gateSlotTakeover, mode == gateSlotQueue)
		if err != nil {
			return nil, err
		}
		if claim.held {
			fmt.Fprintf(out, "land lane: holding the %s slot for this gate run\n", gateSlotName)
			return slot.release, nil
		}
		if claim.holder == nil {
			// No holder was named, so this is not contention: a slot mid-write, or a
			// takeover race another evictor won. Retrying is what the command's own
			// claim does with the same state.
			time.Sleep(gateSlotRetry)
			continue
		}
		if !announced {
			fmt.Fprintf(out, "land lane: waiting for %s (pid %s, idle %dm) — bounded wait %ds\n",
				gateHolderText(claim.holder), claim.holder.PID, claim.holder.IdleMinutes(now()), window)
			announced = true
		}
		if !time.Now().Before(deadline) {
			return nil, gateSlotRefusal(claim.holder)
		}
		time.Sleep(gateSlotPoll)
	}
	if holder := slot.holder(); holder != nil {
		return nil, gateSlotRefusal(holder)
	}
	return nil, exitStatus(3)
}

// gateRelease lets go of the slot a run held. A nil release is a run that took none — it
// already held the slot, or it never got that far — and nothing about the slot may turn a
// finished gate run red, so the caller reports a release error and carries on.
type gateRelease func() error

// gateHolderText names the holder a report is waiting for.
func gateHolderText(holder *landing.Holder) string {
	if holder == nil {
		return "another gate run"
	}
	return holder.Label + " — " + holder.Who
}

// gateSlotRefusal is the answer when the slot is still someone else's after the bounded
// wait: the status the destructive verbs carry (the invocation was right and no rule on
// the tree failed), the holder, and what a session can do about it. It is explicitly not a
// verdict on the tree: the gate refused to run rather than contending, so a run that reads
// this has spent no step and misattributes nothing (coordination.md §5, go-cask#486).
func gateSlotRefusal(holder *landing.Holder) error {
	return statusError{code: 3, message: fmt.Sprintf(
		"verify: the %s slot is held by %s (pid %s, idle %dm) and the bounded wait ended.\n"+
			"  No step ran, so this is NOT a verdict on the tree.\n"+
			"  Wait for that run, or queue for the slot before gating:\n"+
			"      go run ./cmd/gate land-lane status\n"+
			"      go run ./cmd/gate land-lane wait <label>   # queue for it\n"+
			"      go run ./cmd/gate verify --slot=takeover   # the holder's run is gone",
		gateSlotName, gateHolderText(holder), holder.PID, holder.IdleMinutes(now()))}
}

// The claim's retry budget. A claim creates the slot before it writes the record into it,
// so a contender that reads the slot in between retries rather than taking it over; the
// budget bounds that wait, and running out of it reports a retry instead of handing out a
// second gate run.
const (
	gateSlotAttempts = 8
	gateSlotRetry    = 5 * time.Millisecond
	// gateSlotPoll is how often a waiting gate looks again. A wait may last minutes, and
	// polling as fast as gateSlotRetry would spin a core for the whole wait; it is the
	// command's own lanePoll cadence.
	gateSlotPoll = 250 * time.Millisecond
)

// slotWaitSeconds is how long the gate waits for a live holder before it refuses: the
// slot's own wait window (LAND_LANE_WAIT_SECONDS, ten minutes by default), which is a gate
// run's length rather than an arbitrary timeout.
func slotWaitSeconds() int {
	return waitSeconds(policy.LandLane())
}

// gateRunLabel is what this run's acquisition is labelled with in the slot, so a session
// reading `land-lane status` sees which run holds the clone's gate: the checkout it gates
// and its head, never a bare pid.
func gateRunLabel() string {
	root, err := repoRoot()
	if err != nil {
		return "gate verify"
	}
	label := "verify " + filepath.ToSlash(root)
	if head, err := gitOutputIn(root, "rev-parse", "--short", "HEAD"); err == nil {
		if short := strings.TrimSpace(head); short != "" {
			label += " " + short
		}
	}
	return label
}

// The killed-step diagnosis. A gate run whose step is killed — a second run in the clone is
// enough to trip the machine's memory limit, and the kernel kills the biggest child — ends
// with no output past the step's heading: exit 15, no ledger entry, no receipt, and nothing
// for the lane to read. These functions make that failure legible instead: which signal
// killed the step, how many concurrent `gate verify` processes this host can see, and
// what to do about it.

// killedStep and its per-platform half live in signals.go, signals_unix.go and
// signals_windows.go: whether a process status carries a signal is the platform's fact, and
// the diagnosis must not guess at one.

// gateStepKilledReport is the diagnosis a killed step prints. It names the step, the
// signal, how many concurrent verify processes this host can see, and the advice — hold
// the slot — where the lane is already reading.
func gateStepKilledReport(errOut io.Writer, step, signal string) {
	fmt.Fprintf(errOut, "verify: the %s step was killed by %s — no verdict was produced for it.\n", step, signal)
	fmt.Fprintf(errOut, "  concurrent `gate verify` processes visible on this host: %d\n", gateVerifyProcessesFunc())
	fmt.Fprintln(errOut, "  A second gate run in one clone is enough to trip the machine's memory limit, and")
	fmt.Fprintln(errOut, "  the kernel kills the step that is holding the most memory. Nothing is wrong with")
	fmt.Fprintln(errOut, "  the tree: hold the slot and re-run. The gate takes the slot for a whole run now,")
	fmt.Fprintln(errOut, "  so a second run waits for the first instead of running beside it:")
	fmt.Fprintln(errOut, "      go run ./cmd/gate land-lane status")
	fmt.Fprintln(errOut, "      go run ./cmd/gate land-lane wait <label>")
}

// gateVerifyProcessesFunc counts the concurrent gate runs. It is a variable so a test can
// drive the count a diagnosis prints, and so a host with no process listing can be
// described without one.
var gateVerifyProcessesFunc = gateVerifyProcesses

// gateVerifyProcesses counts the `gate verify` processes this host can see — this one
// included. It reads /proc, the only cheap process listing available here, and reads it
// directly rather than through `ps` so a diagnosis cannot fail on a machine that has no
// such tool: a host with no /proc reports 0, which the report states as the count it could
// see rather than as an absence of contention.
func gateVerifyProcesses() int {
	if _, err := os.Stat("/proc"); err != nil {
		return 0
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() || !allDigits(entry.Name()) {
			continue
		}
		raw, err := os.ReadFile("/proc/" + entry.Name() + "/cmdline")
		if err != nil {
			continue
		}
		if isVerifyCommand(strings.Split(string(raw), "\x00")) {
			count++
		}
	}
	return count
}

// isVerifyCommand reports whether one process's argv is a gate run: a gate binary or
// `go run` invocation whose next argument is `verify`. Both separators are read, because the
// listing may be a Windows process table read from a POSIX shell, or the other way round.
func isVerifyCommand(argv []string) bool {
	for index, arg := range argv {
		base := strings.ToLower(path.Base(strings.ReplaceAll(arg, `\`, "/")))
		if base != "gate" && base != "gate.exe" {
			continue
		}
		return index+1 < len(argv) && argv[index+1] == "verify"
	}
	return false
}

// allDigits reports whether a name is a pid. A /proc entry that is not one is not a process
// entry.
func allDigits(name string) bool {
	if name == "" {
		return false
	}
	for _, char := range name {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}
