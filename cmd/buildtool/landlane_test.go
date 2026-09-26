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
		if errors.As(err, &statusErr) {
			status = statusErr.code
			if statusErr.message != "" {
				errOut.WriteString(statusErr.message + "\n")
			}
		} else {
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
