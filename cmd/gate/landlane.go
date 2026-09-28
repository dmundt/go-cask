package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dmundt/go-cask/internal/build/lane"
	"github.com/dmundt/go-cask/internal/build/policy"
)

// The claim's retry budget. A claim creates the slot before it writes the record into
// it, so a contender that reads the slot in between retries rather than taking it over;
// the budget bounds that wait, and running out of it reports a retry instead of handing
// out a second landing.
const (
	laneAttempts = 8
	laneRetry    = 5 * time.Millisecond
	// lanePoll is how often a waiting session looks again. laneRetry is for a slot that is
	// mid-write and resolves in microseconds; a wait may last minutes, and polling that fast
	// would spin a core for the whole wait.
	lanePoll = 250 * time.Millisecond
)

// landLaneSlot is one worktree's view of the shared advisory slot: where the slot's
// records are, where this worktree's own acquisition token lives, and the identity this
// worktree acts under.
//
// It is resolved from Git for the command and built directly by a test, which is what
// makes the slot's behaviour — the exclusive claim, the two refusals, the takeover
// record — provable without a repository and without four real worktrees.
type landLaneSlot struct {
	// dir is the slot's directory in the shared git dir, so every worktree of a clone
	// sees one slot.
	dir string
	// owner and takeover are the records inside dir.
	owner    string
	takeover string
	// token is this worktree's own acquisition token, in its own git dir.
	token string
	// who is the identity this worktree acts under.
	who string
	// label names the acquisition in the slot's record, so a session that finds the slot
	// taken can read what took it. It is what the gate sets so a waiting run reports which
	// gate run it is waiting for.
	label string
	// staleMinutes is the idle window after which another session may take the slot.
	staleMinutes int
	// deadGraceSeconds is how long a holder that is provably gone must have been idle
	// before --takeover-dead may take the slot.
	deadGraceSeconds int
	// waitSeconds is how long `wait` waits by default when the caller names no deadline.
	waitSeconds int
}

// runLandLane takes, renews, reports and frees the local advisory slot.
//
// The slot serializes gate runs inside ONE clone — it is not the lane, which is the
// open pull request on the server — and nothing about it is committed.
func runLandLane(args []string, out, errOut io.Writer) error {
	slot, err := resolveLandLane("")
	if err != nil {
		return err
	}
	return landLane(args, out, errOut, slot)
}

// resolveLandLane reads the slot's location and this worktree's identity from Git. A
// repository is named when the caller is not standing in the worktree it asks about,
// which is how a pre-push hook reports the slot of the worktree being pushed.
//
// The identity deliberately carries no path: Windows Git spells this repository's
// directory `D:/x/...` and WSL Git spells it `/mnt/d/x/...`, and the two toolchains
// carry out one landing here, so a lane taken by one must be visible to the other.
func resolveLandLane(repo string) (*landLaneSlot, error) {
	table := policy.LandLane()

	commonDir, err := gitPathIn(repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, err
	}
	gitDir, err := gitPathIn(repo, "rev-parse", "--path-format=absolute", "--git-dir")
	if err != nil {
		return nil, err
	}
	toplevel, err := gitPathIn(repo, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	absoluteGitDir, err := gitPathIn(repo, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return nil, err
	}
	branch, err := gitPathIn(repo, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return nil, err
	}

	dir := filepath.Join(commonDir, table.Dir)
	repoID, err := ensureRepoID(filepath.Join(dir, table.RepoID))
	if err != nil {
		return nil, err
	}
	primary := filepath.Clean(absoluteGitDir) == filepath.Clean(commonDir)

	return &landLaneSlot{
		dir:              dir,
		owner:            filepath.Join(dir, table.Owner),
		takeover:         filepath.Join(dir, table.Takeover),
		token:            filepath.Join(gitDir, table.Token),
		who:              lane.Identity(repoID, lane.WorktreeName(primary, filepath.Base(toplevel)), branch),
		label:            "land-lane",
		staleMinutes:     staleMinutes(table),
		deadGraceSeconds: deadGraceSeconds(table),
		waitSeconds:      waitSeconds(table),
	}, nil
}

// ensureRepoID returns the clone's id, writing one when the shared git dir has none.
// The id is what keeps one clone's slot from being recognised as another's, so it is
// random rather than derived from a path.
func ensureRepoID(path string) (string, error) {
	if data, err := os.ReadFile(path); err == nil {
		if id := strings.TrimSpace(string(data)); id != "" {
			return id, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("creating the slot directory: %w", err)
	}
	id, err := randomID()
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(id+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	return id, nil
}

// randomID returns a fresh random identifier, used for the clone id and for one
// acquisition's token.
func randomID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generating a random id: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

// staleMinutes reads the idle window, letting the environment override the table.
func staleMinutes(table policy.LandLaneTable) int {
	if override := os.Getenv(table.StaleEnv); override != "" {
		if minutes, err := strconv.Atoi(override); err == nil && minutes >= 0 {
			return minutes
		}
	}
	return table.StaleMinutes
}

// deadGraceSeconds reads how long a provably gone holder must have been idle, letting the
// environment override the table.
func deadGraceSeconds(table policy.LandLaneTable) int {
	if override := os.Getenv(table.DeadGraceEnv); override != "" {
		if seconds, err := strconv.Atoi(override); err == nil && seconds >= 0 {
			return seconds
		}
	}
	return table.DeadGraceSeconds
}

// processStartFunc is the process reader the command judges holders with. It is a variable so a
// test can describe a host without one — the alternative is a test that depends on which pids
// happen to exist — and it is one variable so the reader the claim records with and the reader
// the status judges with cannot drift apart.
var processStartFunc = processStart

// waitSeconds reads how long `wait` waits by default, letting the environment override the table.
func waitSeconds(table policy.LandLaneTable) int {
	if override := os.Getenv(table.WaitEnv); override != "" {
		if seconds, err := strconv.Atoi(override); err == nil && seconds >= 0 {
			return seconds
		}
	}
	return table.WaitSeconds
}

// currentHost names this machine for the slot record, so that a holder written here can be
// judged here. A host that cannot name itself writes lane.UnknownHost, which never compares
// equal to a real host: its holder stays Unknown, and an unknown holder is never taken over.
func currentHost() string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		return lane.UnknownHost
	}
	return strings.TrimSpace(host)
}

// processStart reports the start time of a running pid, and whether this toolchain can read one
// at all. The second result is what keeps a platform without /proc from reading every holder as
// gone: "this host cannot tell" is Unknown, and only "this host can tell, and there is no such
// process" is Gone.
func processStart(pid string) (string, bool) {
	if _, err := os.Stat("/proc"); err != nil {
		return "", false
	}
	if _, err := strconv.Atoi(pid); err != nil {
		return "", false
	}
	stat, err := os.ReadFile(filepath.Join("/proc", pid, "stat"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", true
		}
		return "", false
	}
	// Field 22 of /proc/<pid>/stat is the process's start time in clock ticks since boot. The
	// second field is the command in parentheses and may itself hold spaces and parentheses,
	// so the fields are counted from after its LAST closing parenthesis.
	if close := strings.LastIndex(string(stat), ")"); close >= 0 {
		fields := strings.Fields(string(stat)[close+1:])
		if len(fields) > 19 {
			return fields[19], true
		}
	}
	return "", true
}

// landLane is the command with its slot injected.
func landLane(args []string, out, errOut io.Writer, slot *landLaneSlot) error {
	command := "status"
	if len(args) > 0 {
		command = args[0]
	}
	switch command {
	case "whoami":
		// The identity as this worktree computes it, so a test — and a person
		// debugging a refused push — can compare what two toolchains produce without
		// reverse-engineering the owner record.
		fmt.Fprintln(out, slot.who)
		return nil
	case "status":
		return landLaneStatus(slot, out, errOut)
	case "acquire":
		return landLaneAcquire(args[1:], slot, out, errOut)
	case "wait":
		return landLaneWait(args[1:], slot, out, errOut)
	case "renew":
		return landLaneRenew(slot, out, errOut)
	case "release":
		return landLaneRelease(slot, out, errOut)
	case "-h", "--help", "help":
		fmt.Fprintln(out, landLaneUsage)
		return nil
	default:
		return usageError{landLaneUsage}
	}
}

// landLaneUsage is the command's own help, which is also its usage error.
const landLaneUsage = "usage: gate land-lane [status | whoami | acquire [--force] [--takeover-dead] <label> | wait [--watch] <label> [<seconds>] | renew | release]"

// landLaneStatus reports the slot and returns the status the caller reads: 0 when this
// worktree holds it, 1 when it is free, 2 when someone else does. The verdict is on
// stdout and the exit status carries it, so a caller can read either.
func landLaneStatus(slot *landLaneSlot, out, errOut io.Writer) error {
	holder := slot.read()
	status := lane.SlotStatus(holder, slot.who, slot.mine())
	if status == lane.Free {
		fmt.Fprintln(out, "land lane: free")
		return exitStatus(status.ExitCode())
	}
	fmt.Fprintf(out, "land lane: %s (pid %s, idle %dm)\n",
		holder.Label+" — "+holder.Who, holder.PID, holder.IdleMinutes(now()))
	if status == lane.Mine {
		fmt.Fprintln(out, "land lane: you hold it")
		return nil
	}
	if lane.Evictable(holder, time.Duration(slot.deadGraceSeconds)*time.Second,
		lane.LivenessOf(*holder, currentHost(), processStartFunc), now()) {
		fmt.Fprintln(out, "land lane: evictable — the holder's process is gone on this host")
		fmt.Fprintln(out, "land lane: 'acquire --takeover-dead <label>' takes it now")
		return exitStatus(3)
	}
	if holder.Who == slot.who {
		fmt.Fprintln(errOut, "land lane: this identity is recorded, but this worktree holds no outstanding acquisition for it")
	}
	return exitStatus(status.ExitCode())
}

// claimForGate is one non-blocking attempt at the slot, for a caller that runs its own
// wait loop: the gate holds the slot for a whole run and needs to report the holder it is
// waiting for and read the slot again after each attempt, which the batch form of the
// command's claim does not give it. staleDead is set so a holder whose process is provably
// gone does not make the clone's queue wait out a window nobody will ever renew.
//
// A refusal is reported rather than recorded, so a caller that only asked is handed the
// same answer `acquire` gives the shell: who holds the slot.
func (s *landLaneSlot) claimForGate(label string, force, staleDead bool) (gateClaim, error) {
	holder := s.read()
	decision := lane.Decide(holder, s.who, s.mine(),
		time.Duration(s.staleMinutes)*time.Minute, time.Duration(s.deadGraceSeconds)*time.Second,
		force, staleDead, lane.LivenessOf(derefHolder(holder), currentHost(), processStartFunc), now())

	switch decision.Outcome {
	case lane.AlreadyMine, lane.RefusedSameIdentity, lane.RefusedFresh:
		return gateClaim{holder: &decision.Found}, nil
	case lane.Unreadable:
		// The winner creates the slot before it writes the record into it. Waiting for
		// the record — rather than taking the half-written slot over — is what keeps two
		// acquirers from holding one slot; a claim that named no holder is the caller's
		// signal to try again.
		return gateClaim{}, nil
	}

	token, err := randomID()
	if err != nil {
		return gateClaim{}, err
	}
	if decision.Outcome.TakesOver() {
		how := lane.Expired
		switch decision.Outcome {
		case lane.TakeoverForced:
			how = lane.Forced
		case lane.TakeoverDead:
			how = lane.Dead
		}
		s.recordTakeover(decision.Found, how)
		// The evicted holder's record has to leave the slot before the exclusive create
		// below, or every attempt reads the same stale record and reports the same
		// eviction. Another evictor may have moved it first, which is why this is a
		// rename that reports whether it won.
		if aside, taken := s.takeAside(); taken {
			_ = os.Remove(aside)
		}
	}
	if err := s.claim(label, token); err != nil {
		if errors.Is(err, os.ErrExist) {
			// Lost the create: either another holder took the slot in between, or
			// another evictor did. The next attempt reads whichever record won.
			return gateClaim{}, nil
		}
		return gateClaim{}, err
	}
	return gateClaim{held: true}, nil
}

// derefHolder is the holder a decision reads: the slot's own, or a zero holder for the
// free slot the acquisition is about to claim. A free slot is never judged live, so the
// zero value is the one input Decide ignores.
func derefHolder(holder *lane.Holder) lane.Holder {
	if holder == nil {
		return lane.Holder{}
	}
	return *holder
}

// landLaneClaim takes the slot, waiting until deadline when one is given. It is the ONE claim
// path: `acquire` passes no deadline and reports a live holder at once, while `wait` passes the
// moment it stops waiting, so what a refusal means, what a takeover records and how a slot that
// is still being written is handled cannot drift between the two verbs.
//
// A zero deadline means "do not wait". With one, a holder inside its window is waited out — that
// is what `wait` is for — and the deadline is an answer rather than a failure: exit 3, the status
// a caller reads as "not mine, act deliberately".
//
// The claim is an exclusive create, never a check followed by a write: two waiters retrying on the
// same cadence both found the slot absent and both claimed it, which defeats the point of a slot.
// Eviction goes through a rename for the same reason — the slot is never unowned while its old
// holder is being removed, so two simultaneous evictors cannot both complete the sequence.
func landLaneClaim(slot *landLaneSlot, label string, force, takeDead bool, deadline time.Time, out, errOut io.Writer) error {
	window := time.Duration(slot.staleMinutes) * time.Minute
	grace := time.Duration(slot.deadGraceSeconds) * time.Second
	// attempt counts the tries that decided nothing — a slot mid-write, a holder that vanished
	// between the create and the read, a takeover race lost — and is what keeps a broken slot
	// from spinning forever. Waiting out a live holder is not one of those: the deadline governs
	// it, however long the caller asked to wait.
	for attempt := 1; ; {
		token, err := randomID()
		if err != nil {
			return err
		}
		err = slot.claim(label, token)
		if err == nil {
			fmt.Fprintf(out, "land lane: acquired by %s (%s)\n", label, slot.who)
			return nil
		}
		if !errors.Is(err, os.ErrExist) {
			return err
		}

		holder := slot.read()
		if holder == nil {
			// The holder released between the create attempt and this read.
			attempt++
			if attempt > laneAttempts {
				return errors.New("land lane: could not claim the slot; retry")
			}
			continue
		}

		decision := lane.Decide(holder, slot.who, slot.mine(), window, grace, force, takeDead,
			lane.LivenessOf(*holder, currentHost(), processStartFunc), now())
		switch decision.Outcome {
		case lane.Unreadable:
			// The winner creates the slot before it writes the record into it, so a
			// slot that holds nobody yet is a claim in progress: wait for the record
			// rather than taking the slot over, or two acquirers end up holding it.
			attempt++
			if attempt > laneAttempts {
				return errors.New("land lane: could not claim the slot; retry")
			}
			time.Sleep(laneRetry)
			continue
		case lane.AlreadyMine:
			// This worktree already holds the slot through this very acquisition. A
			// second session in it cannot be told apart from the first by any file it
			// shares, so it is told the slot is held rather than handed a second
			// landing; refreshing the deadline is `renew`, the holder's own call.
			fmt.Fprintf(out, "land lane: already held by %s (%s)\n", holder.Label, slot.who)
			return nil
		case lane.RefusedSameIdentity:
			return fmt.Errorf(
				"land lane: held by %s — %s, and this worktree holds no outstanding acquisition for that identity "+
					"(an earlier acquisition in it owns the slot); wait for it, or use --force when you know it is dead",
				holder.Label, holder.Who)
		case lane.RefusedFresh:
			if deadline.IsZero() {
				return fmt.Errorf(
					"land lane: held by %s — %s, idle %dm; wait for it, renew it if it is yours, or use "+
						"--takeover-dead when its process is gone (--force takes it whatever its state)",
					holder.Label, holder.Who, holder.IdleMinutes(now()))
			}
			if !time.Now().Before(deadline) {
				// A waiter that ran out of time has not failed: it is reporting that the slot
				// is someone else's, which is what its exit status says.
				fmt.Fprintf(errOut, "land lane: %s — %s still holds it after waiting; nothing was taken\n",
					holder.Label, holder.Who)
				return exitStatus(3)
			}
			time.Sleep(lanePoll)
			continue
		}

		attempt++
		if attempt > laneAttempts {
			return fmt.Errorf(
				"land lane: %s — %s keeps winning the takeover race; retry", holder.Label, holder.Who)
		}

		// Record the eviction before the slot changes hands: it is the only trace the
		// evicted holder can read afterwards.
		how := lane.Expired
		switch decision.Outcome {
		case lane.TakeoverForced:
			how = lane.Forced
		case lane.TakeoverDead:
			how = lane.Dead
		}
		slot.recordTakeover(*holder, how)
		reason := "expired"
		switch how {
		case lane.Forced:
			reason = "--force"
		case lane.Dead:
			reason = "holder is gone"
		}
		fmt.Fprintf(errOut, "land lane: taking over from %s — %s (idle %dm, %s)\n",
			holder.Label, holder.Who, holder.IdleMinutes(now()), reason)

		// Another evictor moved the slot first when this fails; the next attempt claims
		// the free slot either way.
		if aside, taken := slot.takeAside(); taken {
			_ = os.Remove(aside)
		}
	}
}

// landLaneAcquire claims the slot, or takes over one whose idle window has passed or whose holder
// is provably gone. It does not wait: a live holder is reported as the refusal it is, and `wait`
// is the verb for sitting that refusal out.
func landLaneAcquire(args []string, slot *landLaneSlot, out, errOut io.Writer) error {
	force, takeDead := false, false
	for len(args) > 0 && strings.HasPrefix(args[0], "--") {
		switch args[0] {
		case "--force":
			force = true
		case "--takeover-dead":
			takeDead = true
		default:
			return usageError{fmt.Sprintf("unknown flag %q for acquire", args[0])}
		}
		args = args[1:]
	}
	label := ""
	if len(args) > 0 {
		label = args[0]
	}
	if label == "" {
		return usageError{"usage: gate land-lane acquire [--force] [--takeover-dead] <label>"}
	}
	if len(args) > 1 {
		return usageError{fmt.Sprintf("unexpected extra argument: %s", args[1])}
	}
	return landLaneClaim(slot, label, force, takeDead, time.Time{}, out, errOut)
}

// landLaneWait waits for the slot instead of losing a race for it.
//
// It is for the queue rather than for the impatient: a session that would otherwise fail and retry
// on its own cadence asks here, and the clone's slot decides the order. `--watch` never claims —
// it answers whether the slot is free, which is what a pre-push check wants — and a slot that is
// already free is claimed at once.
func landLaneWait(args []string, slot *landLaneSlot, out, errOut io.Writer) error {
	watch := false
	for len(args) > 0 && strings.HasPrefix(args[0], "--") {
		if args[0] != "--watch" {
			return usageError{fmt.Sprintf("unknown flag %q for wait", args[0])}
		}
		watch = true
		args = args[1:]
	}

	label := ""
	if len(args) > 0 {
		label = args[0]
		args = args[1:]
	}
	seconds := slot.waitSeconds
	if len(args) > 0 {
		parsed, err := strconv.Atoi(args[0])
		if err != nil || parsed < 0 {
			return usageError{fmt.Sprintf("wait: %q is not a number of seconds", args[0])}
		}
		seconds = parsed
		args = args[1:]
	}
	if len(args) > 0 {
		return usageError{fmt.Sprintf("unexpected extra argument: %s", args[0])}
	}
	if label == "" && !watch {
		return usageError{"usage: gate land-lane wait [--watch] <label> [<seconds>]"}
	}

	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	if watch {
		// Watching never claims: it answers whether the slot is free, and a slot that stays
		// held until the deadline is reported rather than taken.
		for {
			if slot.read() == nil {
				fmt.Fprintln(out, "land lane: free")
				return exitStatus(1)
			}
			if !time.Now().Before(deadline) {
				fmt.Fprintln(errOut, "land lane: still held when the wait ended")
				return exitStatus(3)
			}
			time.Sleep(lanePoll)
		}
	}
	return landLaneClaim(slot, label, false, false, deadline, out, errOut)
}

// landLaneRenew pushes this holder's idle deadline out without giving the slot up, so a
// gate run or a push that outlasts the window is not evicted mid-landing. It is also the
// honest way to discover that it WAS evicted: it fails and reports what happened instead
// of leaving the holder to guess.
func landLaneRenew(slot *landLaneSlot, out, errOut io.Writer) error {
	if slot.read() == nil {
		return errors.New("land lane: free — nothing to renew; acquire it before landing")
	}
	aside, taken := slot.takeAside()
	if !taken {
		return errors.New("land lane: the slot changed hands while renewing; re-check with 'status'")
	}
	defer slot.restore(aside)

	holder := lane.ParseHolder(readFileOrEmpty(aside))
	if !slot.holds(holder) {
		return errors.New(slot.notHeldReport("land lane: this worktree does not hold the slot — " +
			holder.Label + " — " + holder.Who + " does"))
	}
	renewed := lane.Holder{PID: holder.PID, Since: now(), Label: holder.Label, Who: holder.Who, Token: holder.Token}
	// While the slot was held aside it was absent, so an acquirer may have seen it free and
	// claimed it. The exclusive create decides between them: the claim wins, and this renewal
	// reports that instead of overwriting it. The copy this renewal moved aside is dropped by
	// the restore deferred above, which removes it as soon as the slot is occupied again.
	if err := slot.publish(renewed.Fields() + "\n"); err != nil {
		if errors.Is(err, os.ErrExist) {
			return errors.New("land lane: an acquirer took the slot while renewing; this worktree no longer holds it")
		}
		return fmt.Errorf("renewing %s: %w", slot.owner, err)
	}
	fmt.Fprintf(out, "land lane: renewed by %s (%s); idle 0m\n", holder.Label, slot.who)
	return nil
}

// landLaneRelease frees the slot. Only the holder may release it: a slot another
// acquisition in this same worktree holds is not this session's to free, and neither is
// one that was taken over — which the eviction record tells it.
func landLaneRelease(slot *landLaneSlot, out, errOut io.Writer) error {
	if slot.read() == nil {
		fmt.Fprintln(out, "land lane: already free")
		return nil
	}
	aside, taken := slot.takeAside()
	if !taken {
		return errors.New("land lane: the slot changed hands while releasing; re-check with 'status'")
	}
	holder := lane.ParseHolder(readFileOrEmpty(aside))
	if !slot.holds(holder) {
		slot.restore(aside)
		return errors.New(slot.notHeldReport("land lane: held by " + holder.Label + " — " + holder.Who +
			"; only the holder releases it"))
	}
	if err := os.Remove(aside); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("releasing %s: %w", slot.owner, err)
	}
	fmt.Fprintln(out, "land lane: released")
	return nil
}

// claim creates the slot exclusively and records this acquisition in it. The O_EXCL
// create is the whole concurrency story: of any number of simultaneous acquirers exactly
// one wins, and every loser re-reads the winner's record and decides again.
func (s *landLaneSlot) claim(label, token string) error {
	// The record is built before the slot is created, so the window in which the slot
	// exists and holds nobody is one write wide.
	start, _ := processStartFunc(strconv.Itoa(os.Getpid()))
	record := lane.Holder{
		PID: strconv.Itoa(os.Getpid()), Since: now(), Label: label, Who: s.who, Token: token,
		Host: currentHost(), Start: start,
	}.Fields() + "\n"
	if err := s.publish(record); err != nil {
		return err
	}
	return s.writeToken(token)
}

// publish puts a record into the slot with an exclusive create, the one commit point every
// writer of this command shares. Of any number of simultaneous publishers exactly one wins,
// and every loser learns it lost rather than overwriting the winner.
//
// It is deliberately the only way a record reaches the slot. A plain create, and a rename onto
// the path, both replace whatever is already there — `os.Rename` replaces its destination on
// every platform, not only on Windows — so a renewal that moved an aside copy onto the path
// could destroy a claim another worktree had made in between (go-cask#387).
func (s *landLaneSlot) publish(record string) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("creating the slot directory: %w", err)
	}
	file, err := os.OpenFile(s.owner, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := file.WriteString(record); err != nil {
		file.Close()
		return fmt.Errorf("writing %s: %w", s.owner, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("writing %s: %w", s.owner, err)
	}
	return nil
}

// takeAside moves the slot out of the way with a rename, which is atomic: of two racing
// callers exactly one moves the file and the loser backs off. Renew and release decide on
// the moved copy, so a slot that was replaced under them is put back untouched.
func (s *landLaneSlot) takeAside() (string, bool) {
	aside := fmt.Sprintf("%s.read.%d", s.owner, os.Getpid())
	if err := os.Rename(s.owner, aside); err != nil {
		return "", false
	}
	return aside, true
}

// restore puts a slot we moved back when it is not ours. Only if the slot is still free:
// while it was held aside another holder may have moved in, and that slot is not ours to
// overwrite — the copy we moved is dropped instead of left in the slot directory.
func (s *landLaneSlot) restore(aside string) {
	if aside == "" {
		return
	}
	if _, err := os.Stat(s.owner); err == nil {
		_ = os.Remove(aside)
		return
	}
	if err := os.Rename(aside, s.owner); err != nil {
		_ = os.Remove(aside)
	}
}

// read returns the slot's holder, or nil when the slot is free.
func (s *landLaneSlot) read() *lane.Holder {
	data, err := os.ReadFile(s.owner)
	if err != nil {
		return nil
	}
	holder := lane.ParseHolder(string(data))
	return &holder
}

// mine returns this worktree's outstanding acquisition token, or the none-token when it
// holds none.
func (s *landLaneSlot) mine() string {
	token := strings.TrimSpace(readFileOrEmpty(s.token))
	if token == "" {
		return lane.NoneToken
	}
	return token
}

// holds reports whether this worktree holds the slot through the given record's
// acquisition. A record that names no readable holder is not this worktree's, so a
// half-written slot is never renewed or released out from under the claim writing it.
func (s *landLaneSlot) holds(holder lane.Holder) bool {
	return holder.Known() && holder.Who == s.who && holder.Token != lane.NoneToken && holder.Token == s.mine()
}

// writeToken records the acquisition this worktree owns, so a later renew or release can
// prove the slot is still the one it took.
func (s *landLaneSlot) writeToken(token string) error {
	if err := os.MkdirAll(filepath.Dir(s.token), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(s.token), err)
	}
	if err := os.WriteFile(s.token, []byte(token+"\n"), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", s.token, err)
	}
	return nil
}

// recordTakeover writes the eviction record — the only way the holder that lost the slot
// learns that it did — stamped with the moment the slot was evicted.
func (s *landLaneSlot) recordTakeover(holder lane.Holder, how string) {
	record := lane.Takeover{Holder: holder, How: how}
	record.Since = now()
	temp := fmt.Sprintf("%s.tmp.%d", s.takeover, os.Getpid())
	if err := os.WriteFile(temp, []byte(record.Fields()+"\n"), 0o644); err != nil {
		return
	}
	_ = os.Rename(temp, s.takeover)
}

// notHeldReport is a refusal, with the eviction record appended when there is one: an
// evicted holder was told "only the holder releases it" before, which is the same answer
// a session that never held the slot gets, and no hint that it had been evicted
// mid-landing.
func (s *landLaneSlot) notHeldReport(message string) string {
	data, err := os.ReadFile(s.takeover)
	if err != nil {
		return message
	}
	takeover := lane.ParseTakeover(string(data))
	return message + "\n" + fmt.Sprintf("land lane: taken over from %s — %s (%s) at %s",
		takeover.Label, takeover.Who, takeover.How,
		time.Unix(takeover.Since, 0).UTC().Format(time.RFC3339))
}

// readFileOrEmpty returns a file's contents, or "" when it cannot be read — a slot that
// vanished under a caller is one it does not hold.
func readFileOrEmpty(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

// now returns the current Unix time, which every record is stamped with.
func now() int64 {
	return time.Now().Unix()
}
