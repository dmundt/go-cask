package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/dmundt/go-cask/internal/build/core/lane"
)

// slotFor builds one worktree's view of the shared advisory slot: contenders get their
// own name — and therefore their own identity and token, which is what a linked worktree
// gives them — while sharing one slot directory, exactly as the worktrees of a clone do.
//
// It is built directly rather than resolved from Git, so the slot's behaviour is provable
// without a repository and without four real worktrees.
func slotFor(t *testing.T, root, name string, staleMinutes int) *landLaneSlot {
	t.Helper()
	dir := filepath.Join(root, "dsh-land-lane")
	return &landLaneSlot{
		dir:          dir,
		owner:        filepath.Join(dir, "owner"),
		takeover:     filepath.Join(dir, "takeover"),
		token:        filepath.Join(root, "worktrees", name, "dsh-land-lane-mine"),
		who:          lane.Identity("clone-id", name, "main"),
		staleMinutes: staleMinutes,
		// The policy's own grace, so a fixture behaves like the real slot: a gone holder is
		// not instantly evictable just because the test forgot the field.
		deadGraceSeconds: 60,
	}
}

// runSlot runs one land-lane invocation against a slot and returns its output and exit
// status: 0 for a command that succeeded, and the status a shell caller would read
// otherwise.
func runSlot(t *testing.T, slot *landLaneSlot, args ...string) (stdout, stderr string, status int) {
	t.Helper()
	var out, errOut bytes.Buffer
	err := landLane(args, &out, &errOut, slot)
	switch {
	case err == nil:
		status = 0
	default:
		var statusErr statusError
		var usage usageError
		switch {
		case errors.As(err, &statusErr):
			status = statusErr.code
			if statusErr.message != "" {
				errOut.WriteString(statusErr.message + "\n")
			}
		case errors.As(err, &usage):
			// The invocation was wrong, which the process reports as 2 — the harness has to
			// agree, or a test cannot tell a usage error from a refusal.
			status = 2
			errOut.WriteString(usage.message + "\n")
		default:
			status = 1
			errOut.WriteString(err.Error() + "\n")
		}
	}
	return out.String(), errOut.String(), status
}

// TestLandLaneRoundTrip pins the statuses a caller decides on: 1 free, 0 held by this
// worktree, 2 held by someone else — and that releasing returns the slot to free.
func TestLandLaneRoundTrip(t *testing.T) {
	root := t.TempDir()
	mine := slotFor(t, root, "wt-mine", 90)
	other := slotFor(t, root, "wt-other", 90)

	if out, _, status := runSlot(t, mine, "status"); status != 1 || !strings.Contains(out, "land lane: free") {
		t.Fatalf("status on a free slot = %d %q, want exit 1 and the free line", status, out)
	}

	out, _, status := runSlot(t, mine, "acquire", "386")
	if status != 0 || !strings.Contains(out, "land lane: acquired by 386") {
		t.Fatalf("acquire = %d %q, want exit 0 and the acquisition line", status, out)
	}

	out, _, status = runSlot(t, mine, "status")
	if status != 0 || !strings.Contains(out, "land lane: you hold it") {
		t.Fatalf("status as the holder = %d %q, want exit 0 and the holder line", status, out)
	}

	// Another worktree of the same clone sees the same slot and does not hold it.
	out, _, status = runSlot(t, other, "status")
	if status != 2 {
		t.Fatalf("status as another worktree = %d, want 2", status)
	}
	if !strings.Contains(out, "idle 0m") {
		t.Errorf("the status line %q does not report the idle time", out)
	}

	// Only the holder releases it, and the refusal names the holder.
	_, errOut, status := runSlot(t, other, "release")
	if status != 1 || !strings.Contains(errOut, "only the holder releases it") {
		t.Fatalf("release by a non-holder = %d %q, want a refusal naming the holder", status, errOut)
	}
	if out, _, status := runSlot(t, mine, "status"); status != 0 {
		t.Fatalf("the refusal disturbed the holder's slot: %d %q", status, out)
	}

	if out, _, status := runSlot(t, mine, "release"); status != 0 || !strings.Contains(out, "released") {
		t.Fatalf("release by the holder = %d %q, want exit 0", status, out)
	}
	if out, _, status := runSlot(t, mine, "status"); status != 1 {
		t.Fatalf("the slot is not free after release: %d %q", status, out)
	}
	if out, _, status := runSlot(t, mine, "release"); status != 0 || !strings.Contains(out, "already free") {
		t.Fatalf("releasing a free slot = %d %q, want exit 0 and 'already free'", status, out)
	}
}

// TestLandLaneSecondAcquireInOneWorktree pins the rule that a worktree hands out ONE
// landing: a second session in it cannot be told from the first by any file they share,
// so it is told the slot is held rather than given a second one. Renewing the deadline is
// the holder's own call, never a side effect of asking.
func TestLandLaneSecondAcquireInOneWorktree(t *testing.T) {
	root := t.TempDir()
	mine := slotFor(t, root, "wt-mine", 90)

	if _, _, status := runSlot(t, mine, "acquire", "325"); status != 0 {
		t.Fatalf("the first acquire failed: %d", status)
	}
	out, _, status := runSlot(t, mine, "acquire", "326")
	if status != 0 {
		t.Fatalf("a second acquire in the holding worktree = %d, want 0 with the held line", status)
	}
	if !strings.Contains(out, "land lane: already held by 325") {
		t.Errorf("the second acquire reported %q, want it to name the standing holder", out)
	}

	// The standing holder's slot is untouched: no second landing was handed out.
	holder := mine.read()
	if holder == nil || holder.Label != "325" {
		t.Fatalf("the slot now names %+v, want the standing holder 325", holder)
	}
}

// TestLandLaneRenewMovesTheDeadline pins that renew is the holder's own call, that it
// keeps the slot, and that a worktree whose token no longer matches the slot is refused
// with an explanation instead of silently taking it over.
func TestLandLaneRenewMovesTheDeadline(t *testing.T) {
	root := t.TempDir()
	mine := slotFor(t, root, "wt-mine", 90)

	if _, _, status := runSlot(t, mine, "acquire", "325"); status != 0 {
		t.Fatalf("acquire failed: %d", status)
	}
	// Age the slot as one that stopped being refreshed would look, then renew it: the
	// deadline must move, which is the property that keeps a long landing alive.
	if err := os.WriteFile(mine.owner, []byte(lane.Holder{
		PID: "1", Since: now() - 3600, Label: "325", Who: mine.who, Token: mine.mine(),
	}.Fields()+"\n"), 0o644); err != nil {
		t.Fatalf("aging the slot: %v", err)
	}
	if _, _, status := runSlot(t, mine, "renew"); status != 0 {
		t.Fatalf("renew by the holder = %d, want 0", status)
	}
	if idle := mine.read().IdleMinutes(now()); idle != 0 {
		t.Errorf("the slot is idle %dm after renew, want 0", idle)
	}
	if _, _, status := runSlot(t, mine, "status"); status != 0 {
		t.Errorf("renew lost the holder's slot: %d", status)
	}

	// A worktree with no outstanding token does not hold the slot, and is told so.
	if err := os.Remove(mine.token); err != nil {
		t.Fatalf("removing the acquisition token: %v", err)
	}
	_, errOut, status := runSlot(t, mine, "renew")
	if status != 1 || !strings.Contains(errOut, "does not hold the slot") {
		t.Fatalf("renew without a token = %d %q, want a refusal", status, errOut)
	}
	_, errOut, status = runSlot(t, mine, "status")
	if status != 2 || !strings.Contains(errOut, "holds no outstanding acquisition") {
		t.Fatalf("status without a token = %d %q, want the recorded-identity note", status, errOut)
	}
	_, errOut, status = runSlot(t, mine, "release")
	if status != 1 || !strings.Contains(errOut, "only the holder releases it") {
		t.Fatalf("release without a token = %d %q, want a refusal", status, errOut)
	}
	// The refusals leave no half-moved slot behind.
	if leftovers := slotLeftovers(t, mine.dir); leftovers != 0 {
		t.Errorf("the refusals left %d slot copy/copies behind", leftovers)
	}
}

// TestLandLaneStalenessIsIdleTime pins the eviction contract: a slot inside its idle
// window is refused, an idle one is taken over, the eviction is recorded for the holder
// it evicted, and that holder learns what happened instead of being told it never held
// the slot.
func TestLandLaneStalenessIsIdleTime(t *testing.T) {
	root := t.TempDir()
	victim := slotFor(t, root, "wt-victim", 90)
	// The usurper's window is the one a slot has to outlast: with a real window the
	// fresh slot below is defended, and with the same slot aged the window has passed.
	usurper := slotFor(t, root, "wt-usurper", 1)

	if _, _, status := runSlot(t, victim, "acquire", "325"); status != 0 {
		t.Fatalf("the victim could not claim the free slot: %d", status)
	}

	// A fresh slot is not taken over, however much the newcomer wants it.
	_, errOut, status := runSlot(t, usurper, "acquire", "999")
	if status != 1 || !strings.Contains(errOut, "held by 325") || !strings.Contains(errOut, "wait for it") {
		t.Fatalf("a fresh slot was not defended: %d %q", status, errOut)
	}

	// Age the victim's slot as an unrefreshed one looks after the window, then let the
	// usurper take it. No clock is involved and the victim does nothing wrong.
	holder := victim.read()
	holder.Since = now() - 600
	if err := os.WriteFile(victim.owner, []byte(holder.Fields()+"\n"), 0o644); err != nil {
		t.Fatalf("aging the slot: %v", err)
	}
	_, errOut, status = runSlot(t, usurper, "acquire", "999")
	if status != 0 {
		t.Fatalf("an idle slot was not taken over: %d %q", status, errOut)
	}
	if !strings.Contains(errOut, "taking over from 325") {
		t.Errorf("the takeover did not name the holder it evicted: %q", errOut)
	}

	takeover := lane.ParseTakeover(readFileOrEmpty(usurper.takeover))
	if takeover.Label != "325" || takeover.How != lane.Expired {
		t.Errorf("the eviction record = %+v, want holder 325 and reason %q", takeover, lane.Expired)
	}

	// The evicted holder is told what happened rather than left to guess: renew and
	// release both report the eviction, and neither disturbs the new holder's slot.
	_, errOut, status = runSlot(t, victim, "renew")
	if status != 1 || !strings.Contains(errOut, "does not hold the slot") || !strings.Contains(errOut, "taken over from 325") {
		t.Fatalf("the evicted holder's renew = %d %q, want the eviction reported", status, errOut)
	}
	_, errOut, status = runSlot(t, victim, "release")
	if status != 1 || !strings.Contains(errOut, "taken over from 325") {
		t.Fatalf("the evicted holder's release = %d %q, want the eviction reported", status, errOut)
	}
	if _, _, status := runSlot(t, victim, "status"); status != 2 {
		t.Errorf("the new holder lost the slot: status = %d, want 2", status)
	}
	if leftovers := slotLeftovers(t, victim.dir); leftovers != 0 {
		t.Errorf("the evictions left %d slot copy/copies behind", leftovers)
	}
}

// TestLandLaneReleaseRefusesTheEvictedHolder pins the message an evicted and a
// never-holding session used to share: the eviction record is what tells them apart.
func TestLandLaneReleaseRefusesTheEvictedHolder(t *testing.T) {
	root := t.TempDir()
	evicted := slotFor(t, root, "wt-evicted", 90)
	holder := slotFor(t, root, "wt-holder", 90)

	// The evicted worktree's own token names an acquisition that no longer owns the
	// slot the other worktree holds.
	if err := evicted.writeToken("tok-evicted"); err != nil {
		t.Fatalf("writing the token: %v", err)
	}
	if err := os.MkdirAll(evicted.dir, 0o755); err != nil {
		t.Fatalf("creating the slot directory: %v", err)
	}
	record := lane.Holder{PID: "1", Since: now(), Label: "325", Who: holder.who, Token: "tok-holder"}
	if err := os.WriteFile(evicted.owner, []byte(record.Fields()+"\n"), 0o644); err != nil {
		t.Fatalf("writing the slot: %v", err)
	}
	eviction := lane.Takeover{Holder: lane.Holder{PID: "9", Since: now(), Label: "324", Who: evicted.who, Token: lane.Forced}, How: lane.Forced}
	if err := os.WriteFile(evicted.takeover, []byte(eviction.Fields()+"\n"), 0o644); err != nil {
		t.Fatalf("writing the eviction record: %v", err)
	}

	_, errOut, status := runSlot(t, evicted, "release")
	if status != 1 {
		t.Fatalf("release = %d, want 1", status)
	}
	if !strings.Contains(errOut, "taken over from 324") || !strings.Contains(errOut, "forced") {
		t.Errorf("the refusal %q does not report the eviction that caused it", errOut)
	}
	// The other holder's slot survived both refusals.
	if got := holder.read(); got == nil || got.Label != "325" {
		t.Errorf("the slot now holds %+v, want the standing holder 325", got)
	}
}

// TestLandLaneAcquireIsAtomic pins the claim: of four simultaneous acquirers exactly one
// holds the slot each round. A check followed by a write let two waiters retry into the
// same free slot and both claim it, which is what the exclusive create fixed.
func TestLandLaneAcquireIsAtomic(t *testing.T) {
	root := t.TempDir()
	contenders := []*landLaneSlot{
		slotFor(t, root, "wt-1", 90),
		slotFor(t, root, "wt-2", 90),
		slotFor(t, root, "wt-3", 90),
		slotFor(t, root, "wt-4", 90),
	}

	for round := 1; round <= 3; round++ {
		if err := os.Remove(contenders[0].owner); err != nil && !os.IsNotExist(err) {
			t.Fatalf("clearing the slot: %v", err)
		}
		var (
			wg    sync.WaitGroup
			mu    sync.Mutex
			wins  int
			losse int
		)
		for _, contender := range contenders {
			wg.Add(1)
			go func(slot *landLaneSlot) {
				defer wg.Done()
				var out, errOut bytes.Buffer
				err := landLane([]string{"acquire", "race"}, &out, &errOut, slot)
				mu.Lock()
				defer mu.Unlock()
				if err == nil {
					wins++
				} else {
					losse++
				}
			}(contender)
		}
		wg.Wait()
		if wins != 1 || losse != len(contenders)-1 {
			t.Fatalf("round %d: %d winners and %d losers, want exactly one winner", round, wins, losse)
		}
		if leftovers := slotLeftovers(t, contenders[0].dir); leftovers != 0 {
			t.Errorf("round %d left %d slot copy/copies behind", round, leftovers)
		}
	}
}

// TestResolvedIdentityCarriesNoPath pins the property that lets two toolchains share one
// slot: the identity this repository resolves carries neither a drive letter nor a mount
// point, because Windows Git and WSL Git spell the same directory differently.
func TestResolvedIdentityCarriesNoPath(t *testing.T) {
	slot, err := resolveLandLane("")
	if err != nil {
		t.Skipf("not running in a repository: %v", err)
	}
	if regexp.MustCompile(`[A-Za-z]:/|/mnt/|^/`).MatchString(slot.who) {
		t.Errorf("identity %q carries a toolchain path", slot.who)
	}
	if !strings.Contains(slot.who, "#") {
		t.Errorf("identity %q does not carry its parts", slot.who)
	}
}

// slotLeftovers counts the copies a caller left in the slot directory. A renew or a
// release moves the slot aside and decides on the copy, so a copy left behind is a slot
// nobody will ever see again.
func slotLeftovers(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".read.") || strings.Contains(entry.Name(), ".tmp.") {
			count++
		}
	}
	return count
}

// TestLandLaneRenewNeverOverwritesAClaim pins the commit point the renewal was missing.
//
// Renewal used to move its aside copy onto the slot path with os.Rename — which replaces its
// destination on every platform, not only on Windows. An acquirer that claimed the slot while
// the renewal had it aside had its record silently destroyed, and the renewal reported success:
// two worktrees believed they held the slot, and the one that lost could not find out.
//
// The exclusive create is now the only way a record reaches the slot, so the claim wins and the
// renewal is told it lost. This test drives both halves: the publish that must refuse, and the
// command that must leave another holder's record exactly as it found it.
func TestLandLaneRenewNeverOverwritesAClaim(t *testing.T) {
	root := t.TempDir()
	mine := slotFor(t, root, "wt-mine", 90)
	if _, _, status := runSlot(t, mine, "acquire", "387"); status != 0 {
		t.Fatalf("acquire failed: %d", status)
	}

	// The window: renew moves the holder's record aside before it publishes the renewal, so the
	// slot is briefly absent and an acquirer can see it free.
	aside, taken := mine.takeAside()
	if !taken {
		t.Fatal("takeAside failed; the fixture is not in the state the race needs")
	}
	acquirer := lane.Holder{
		PID: "4242", Since: now(), Label: "387", Who: "other#primary:wt-other#main", Token: "other-token",
	}.Fields() + "\n"
	file, err := os.OpenFile(mine.owner, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		t.Fatalf("the acquirer could not claim the free slot: %v", err)
	}
	if _, err := file.WriteString(acquirer); err != nil {
		t.Fatalf("writing the acquirer's record: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("closing the acquirer's record: %v", err)
	}

	// The renewal publishes into the slot the acquirer now holds. It must lose.
	renewed := lane.Holder{
		PID: "1", Since: now(), Label: "387", Who: mine.who, Token: mine.mine(),
	}.Fields() + "\n"
	if err := mine.publish(renewed); !errors.Is(err, os.ErrExist) {
		t.Fatalf("publish onto a claimed slot = %v, want os.ErrExist", err)
	}
	if got := readFileOrEmpty(mine.owner); got != acquirer {
		t.Fatalf("the acquirer's record was replaced:\n got %q\nwant %q", got, acquirer)
	}

	// End to end: the command renewing over another holder refuses and leaves that record
	// untouched. The holder it finds is not this worktree, so the slot is not this worktree's to
	// renew — and the record must come back exactly as it was.
	_, errOut, status := runSlot(t, mine, "renew")
	if status == 0 {
		t.Fatal("renew over another holder = 0, want a refusal")
	}
	if !strings.Contains(errOut, "does not hold the slot") {
		t.Errorf("renew over another holder reported %q, want it to say the slot is not held here", errOut)
	}
	if got := readFileOrEmpty(mine.owner); got != acquirer {
		t.Errorf("the refusal changed the other holder's record:\n got %q\nwant %q", got, acquirer)
	}
	// And the renewal's own scratch is gone: the copy it moved aside is removed as soon as the
	// slot is occupied again, so a lost renewal leaves the slot directory as it found it.
	if _, err := os.Stat(aside); err == nil {
		t.Errorf("the lost renewal left its aside copy behind: %s", aside)
	}
}

// TestLandLaneStatusCallsAGoneHolderEvictable pins the half of #387 that makes a dead holder
// visible rather than merely takeable: `status` says the slot is evictable and exits 3, which is
// a state of its own — not "held by someone else" (2), which a caller would wait out for nothing
// when the holder's process no longer exists.
func TestLandLaneStatusCallsAGoneHolderEvictable(t *testing.T) {
	root := t.TempDir()
	mine := slotFor(t, root, "wt-mine", 90)

	// A holder on this host, idle past the grace, whose process is gone.
	if err := os.MkdirAll(filepath.Dir(mine.owner), 0o755); err != nil {
		t.Fatalf("creating the slot directory: %v", err)
	}
	gone := lane.Holder{
		PID: "2", Since: now() - 120, Label: "386", Who: "other#primary:wt-other#main",
		Token: "tok-other", Host: currentHost(), Start: "1234",
	}.Fields() + "\n"
	if err := os.WriteFile(mine.owner, []byte(gone), 0o644); err != nil {
		t.Fatalf("writing the gone holder: %v", err)
	}
	restore := processStartFunc
	// This host can tell, and there is no such process.
	processStartFunc = func(pid string) (string, bool) { return "", true }
	defer func() { processStartFunc = restore }()

	out, errOut, status := runSlot(t, mine, "status")
	if status != 3 {
		t.Fatalf("status with a gone holder = %d, want 3 (out %q err %q)", status, out, errOut)
	}
	if !strings.Contains(out, "evictable") {
		t.Errorf("status printed %q, want it to say the slot is evictable", out)
	}

	// And inside the grace the machine has not finished noticing: the holder is still just
	// someone else, which is exit 2.
	fresh := lane.Holder{
		PID: "2", Since: now() - 5, Label: "386", Who: "other#primary:wt-other#main",
		Token: "tok-other", Host: currentHost(), Start: "1234",
	}.Fields() + "\n"
	if err := os.WriteFile(mine.owner, []byte(fresh), 0o644); err != nil {
		t.Fatalf("writing the fresh holder: %v", err)
	}
	out, _, status = runSlot(t, mine, "status")
	if status != 2 || strings.Contains(out, "evictable") {
		t.Errorf("status inside grace = %d %q, want 2 and no evictable note", status, out)
	}

	// A holder whose liveness cannot be read is never evictable, whatever the flag says.
	processStartFunc = func(pid string) (string, bool) { return "", false }
	if err := os.WriteFile(mine.owner, []byte(gone), 0o644); err != nil {
		t.Fatalf("writing the gone holder: %v", err)
	}
	out, _, status = runSlot(t, mine, "status")
	if status != 2 || strings.Contains(out, "evictable") {
		t.Errorf("status with an unreadable process = %d %q, want 2 and no evictable note", status, out)
	}
}

// TestLandLaneAcquireTakeoverDeadTakesAGoneHoldersSlot pins the takeover itself: the flag takes a
// gone holder's slot after the grace without waiting the window out, and the eviction record says
// the holder was gone rather than expired — the only trace the holder can read afterwards.
func TestLandLaneAcquireTakeoverDeadTakesAGoneHoldersSlot(t *testing.T) {
	root := t.TempDir()
	mine := slotFor(t, root, "wt-mine", 90)
	if err := os.MkdirAll(filepath.Dir(mine.owner), 0o755); err != nil {
		t.Fatalf("creating the slot directory: %v", err)
	}
	gone := lane.Holder{
		PID: "2", Since: now() - 120, Label: "386", Who: "other#primary:wt-other#main",
		Token: "tok-other", Host: currentHost(), Start: "1234",
	}.Fields() + "\n"
	if err := os.WriteFile(mine.owner, []byte(gone), 0o644); err != nil {
		t.Fatalf("writing the gone holder: %v", err)
	}
	restore := processStartFunc
	processStartFunc = func(pid string) (string, bool) { return "", true }
	defer func() { processStartFunc = restore }()

	// Without the flag the window still applies: a gone holder is not taken implicitly.
	if _, errOut, status := runSlot(t, mine, "acquire", "387"); status == 0 {
		t.Fatalf("acquire without the flag took a gone holder's slot: %q", errOut)
	}

	if _, errOut, status := runSlot(t, mine, "acquire", "--takeover-dead", "387"); status != 0 {
		t.Fatalf("acquire --takeover-dead = %d (%q), want it to take the slot", status, errOut)
	}
	if holder := mine.read(); holder == nil || holder.Who != mine.who {
		t.Fatalf("the slot is held by %+v, want this worktree", holder)
	}
	record := readFileOrEmpty(mine.takeover)
	if !strings.Contains(record, lane.Dead) {
		t.Errorf("the eviction record is %q, want it to say the holder was gone (%q)", record, lane.Dead)
	}
}

// TestLandLaneWaitClaimsAFreeSlotAndWatchesWithoutClaiming pins the two halves of the verb whose
// whole purpose is to make losing a race unnecessary: a waiter with a label takes a free slot, and
// a waiter that is only watching answers the question and takes nothing — a pre-push check that
// claimed the slot it was asking about would be a bug, not a convenience.
func TestLandLaneWaitClaimsAFreeSlotAndWatchesWithoutClaiming(t *testing.T) {
	root := t.TempDir()
	mine := slotFor(t, root, "wt-mine", 90)

	out, errOut, status := runSlot(t, mine, "wait", "387", "1")
	if status != 0 {
		t.Fatalf("wait on a free slot = %d (%q %q), want it to claim", status, out, errOut)
	}
	if holder := mine.read(); holder == nil || holder.Who != mine.who {
		t.Fatalf("the slot is held by %+v, want this worktree", holder)
	}
	if _, _, status := runSlot(t, mine, "release"); status != 0 {
		t.Fatalf("release = %d, want 0", status)
	}

	out, _, status = runSlot(t, mine, "wait", "--watch", "387", "0")
	if status != 1 {
		t.Fatalf("watch on a free slot = %d, want 1 (free)", status)
	}
	if !strings.Contains(out, "free") {
		t.Errorf("watch printed %q, want it to say the slot is free", out)
	}
	if mine.read() != nil {
		t.Error("watch claimed the slot it was only asked about")
	}
}

// TestLandLaneWaitEndsOnItsDeadline pins that waiting is bounded and that running out of it is an
// answer rather than a failure: a holder inside its window is left alone, and the waiter reports
// that nothing was taken with exit 3 — the status a caller reads as "not mine, act deliberately".
func TestLandLaneWaitEndsOnItsDeadline(t *testing.T) {
	root := t.TempDir()
	mine := slotFor(t, root, "wt-mine", 90)
	if err := os.MkdirAll(filepath.Dir(mine.owner), 0o755); err != nil {
		t.Fatalf("creating the slot directory: %v", err)
	}
	// A record with no host is never judged dead, so this holder is simply live and fresh.
	held := lane.Holder{
		PID: "2", Since: now(), Label: "386", Who: "other#primary:wt-other#main", Token: "tok-other",
	}.Fields() + "\n"
	if err := os.WriteFile(mine.owner, []byte(held), 0o644); err != nil {
		t.Fatalf("writing the held slot: %v", err)
	}

	_, errOut, status := runSlot(t, mine, "wait", "387", "1")
	if status != 3 {
		t.Fatalf("wait past its deadline = %d (%q), want 3", status, errOut)
	}
	if !strings.Contains(errOut, "still holds it after waiting") {
		t.Errorf("the deadline report is %q, want it to say the holder still holds the slot", errOut)
	}
	if holder := mine.read(); holder == nil || holder.Who != "other#primary:wt-other#main" {
		t.Errorf("the waiter took a live holder's slot: %+v", holder)
	}

	// Watching a slot that stays held ends the same way, and claims nothing either.
	_, errOut, status = runSlot(t, mine, "wait", "--watch", "387", "0")
	if status != 3 || !strings.Contains(errOut, "still held") {
		t.Errorf("watch on a held slot = %d (%q), want 3", status, errOut)
	}
}

// TestLandLaneWaitTakesOverAnIdleHolder pins that waiting is not just patience: a holder that has
// gone idle past the window is taken over, which is how the queue moves at all.
func TestLandLaneWaitTakesOverAnIdleHolder(t *testing.T) {
	root := t.TempDir()
	mine := slotFor(t, root, "wt-mine", 90)
	if err := os.MkdirAll(filepath.Dir(mine.owner), 0o755); err != nil {
		t.Fatalf("creating the slot directory: %v", err)
	}
	idle := lane.Holder{
		PID: "2", Since: now() - 6000, Label: "386", Who: "other#primary:wt-other#main", Token: "tok-other",
	}.Fields() + "\n"
	if err := os.WriteFile(mine.owner, []byte(idle), 0o644); err != nil {
		t.Fatalf("writing the idle holder: %v", err)
	}

	if _, errOut, status := runSlot(t, mine, "wait", "387", "1"); status != 0 {
		t.Fatalf("wait on an idle holder = %d (%q), want it to take the slot over", status, errOut)
	}
	if holder := mine.read(); holder == nil || holder.Who != mine.who {
		t.Fatalf("the slot is held by %+v, want this worktree", holder)
	}
}

// TestLandLaneWaitUsageIsRefused pins the invocation contract: a wait that could never end is a
// usage error rather than a hang, and a duration that is not a number is refused rather than read
// as zero.
func TestLandLaneWaitUsageIsRefused(t *testing.T) {
	root := t.TempDir()
	mine := slotFor(t, root, "wt-mine", 90)

	for _, args := range [][]string{
		{"wait"},
		{"wait", "387", "not-a-number"},
		{"wait", "--unknown", "387"},
		{"wait", "387", "1", "extra"},
	} {
		if _, errOut, status := runSlot(t, mine, args...); status != 2 {
			t.Errorf("land-lane %q = %d (%q), want the usage status", args, status, errOut)
		}
	}
}
