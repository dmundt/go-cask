package lane

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestIdentityCarriesNoPath(t *testing.T) {
	t.Parallel()

	// A drive letter or a mount point is exactly what differs between the toolchains
	// that carry out one landing here, so the identity must contain neither — or a
	// lane taken by the Windows client is invisible to the WSL one's hook.
	pathLike := regexp.MustCompile(`[A-Za-z]:/|/mnt/|^/`)
	cases := []struct {
		name     string
		repoID   string
		worktree string
		branch   string
	}{
		{name: "the primary checkout", repoID: "6f1c9a2e", worktree: PrimaryWorktree, branch: "main"},
		{name: "a linked worktree", repoID: "6f1c9a2e", worktree: "wt-386", branch: "chore/lane/386"},
		{name: "a branch with a slash", repoID: "0d1f", worktree: "wt-lane", branch: "feat/a/b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			identity := Identity(tc.repoID, tc.worktree, tc.branch)
			if pathLike.MatchString(identity) {
				t.Errorf("identity %q carries a toolchain path", identity)
			}
			if !strings.HasPrefix(identity, tc.repoID+"#") {
				t.Errorf("identity %q does not start with the clone's id", identity)
			}
			if !strings.Contains(identity, tc.worktree+"#"+tc.branch) {
				t.Errorf("identity %q does not carry its worktree and branch", identity)
			}
		})
	}
}

func TestWorktreeName(t *testing.T) {
	t.Parallel()

	if got := WorktreeName(true, "go-cask"); got != PrimaryWorktree {
		t.Errorf("WorktreeName(primary) = %q, want %q", got, PrimaryWorktree)
	}
	if got := WorktreeName(false, "wt-386"); got != "wt-386" {
		t.Errorf("WorktreeName(linked) = %q, want the directory's base name", got)
	}
}

func TestHolderRoundTrip(t *testing.T) {
	t.Parallel()

	holder := Holder{PID: "4242", Since: 1790000000, Label: "386", Who: "abc#primary:wt-386#chore/lane", Token: "tok-1"}
	parsed := ParseHolder(holder.Fields())
	if parsed != holder {
		t.Errorf("ParseHolder(%q) = %+v, want %+v", holder.Fields(), parsed, holder)
	}

	// A partial or unreadable record yields the reader's defaults rather than an
	// error: a slot whose holder cannot be read is a slot to take over.
	cases := []struct {
		name   string
		record string
		want   Holder
	}{
		{name: "empty", record: "", want: Holder{Label: "?", Who: "?", Token: NoneToken}},
		{name: "no token", record: "1\t2\tlabel\twho", want: Holder{PID: "1", Since: 2, Label: "label", Who: "who", Token: NoneToken}},
		{name: "blank fields keep the defaults", record: "1\t2\t\t\t", want: Holder{PID: "1", Since: 2, Label: "?", Who: "?", Token: NoneToken}},
		{name: "a trailing newline is not a field", record: "1\t2\tl\tw\tt\n", want: Holder{PID: "1", Since: 2, Label: "l", Who: "w", Token: "t"}},
		{name: "a non-numeric stamp reads as zero", record: "1\tx\tl\tw\tt", want: Holder{PID: "1", Label: "l", Who: "w", Token: "t"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ParseHolder(tc.record); got != tc.want {
				t.Errorf("ParseHolder(%q) = %+v, want %+v", tc.record, got, tc.want)
			}
		})
	}
}

func TestTakeoverRoundTrip(t *testing.T) {
	t.Parallel()

	takeover := Takeover{
		Holder: Holder{PID: "7", Since: 1790000100, Label: "325", Who: "abc#primary:wt#main", Token: Expired},
		How:    Expired,
	}
	parsed := ParseTakeover(takeover.Fields())
	if parsed.Holder.Label != "325" || parsed.Holder.Who != takeover.Holder.Who {
		t.Errorf("ParseTakeover lost the evicted holder: %+v", parsed)
	}
	if parsed.How != Expired {
		t.Errorf("ParseTakeover read how = %q, want %q", parsed.How, Expired)
	}
	// Why the slot changed hands lives in the field a holder spends on its token, so
	// the record fits the same reader.
	if fields := strings.Split(takeover.Fields(), "\t"); len(fields) != 5 {
		t.Errorf("takeover record %q has %d fields, want 5", takeover.Fields(), len(fields))
	}
}

// TestIdleIsTimeSinceRefresh pins the property the whole staleness rule rests on: idle
// time is measured from the moment the holder last refreshed the slot, which is what
// renew moves — not from acquisition.
func TestIdleIsTimeSinceRefresh(t *testing.T) {
	t.Parallel()

	holder := Holder{Since: 1000}
	cases := []struct {
		name string
		now  int64
		want time.Duration
	}{
		{name: "just refreshed", now: 1000, want: 0},
		{name: "ten minutes later", now: 1600, want: 10 * time.Minute},
		{name: "a clock that moved back reads as fresh", now: 900, want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := holder.Idle(tc.now); got != tc.want {
				t.Errorf("Idle(%d) = %v, want %v", tc.now, got, tc.want)
			}
		})
	}
	if got := holder.IdleMinutes(1600); got != 10 {
		t.Errorf("IdleMinutes = %d, want 10", got)
	}
}

func TestDecide(t *testing.T) {
	t.Parallel()

	const (
		me     = "abc#primary:wt-386#chore/lane"
		other  = "abc#primary:wt-1#main"
		mine   = "tok-mine"
		their  = "tok-theirs"
		now    = int64(10_000)
		window = 90 * time.Minute
		fresh  = int64(10_000) // idle 0
		stale  = int64(0)      // idle 10000s
	)
	freshSlot := &Holder{PID: "1", Since: fresh, Label: "386", Who: me, Token: mine}
	theirFresh := &Holder{PID: "2", Since: fresh, Label: "1", Who: other, Token: their}
	theirStale := &Holder{PID: "3", Since: stale, Label: "1", Who: other, Token: their}

	cases := []struct {
		name  string
		slot  *Holder
		who   string
		mine  string
		force bool
		want  Outcome
	}{
		{name: "a free slot is claimed", slot: nil, who: me, mine: mine, want: Created},
		{
			name: "this worktree's own acquisition is recognised",
			slot: freshSlot, who: me, mine: mine, want: AlreadyMine,
		},
		{
			// The same identity through another acquisition: nothing in the worktree
			// tells the two sessions apart, and renewing is the holder's deliberate
			// call, so this is refused rather than handed out.
			name: "another acquisition in this worktree is refused",
			slot: freshSlot, who: me, mine: "tok-another", want: RefusedSameIdentity,
		},
		{
			name: "a worktree with no token does not hold the slot",
			slot: freshSlot, who: me, mine: NoneToken, want: RefusedSameIdentity,
		},
		{name: "a fresh holder is not evicted", slot: theirFresh, who: me, mine: mine, want: RefusedFresh},
		{name: "an idle holder expires", slot: theirStale, who: me, mine: mine, want: TakeoverExpired},
		{name: "force takes a fresh holder", slot: theirFresh, who: me, mine: mine, force: true, want: TakeoverForced},
		{
			// --force on a slot that had already expired is recorded as expired: the
			// idle window is why it was available, and the record says what happened.
			name: "force on an idle holder is still an expiry",
			slot: theirStale, who: me, mine: mine, force: true, want: TakeoverExpired,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			decision := Decide(tc.slot, tc.who, tc.mine, window, tc.force, now)
			if decision.Outcome != tc.want {
				t.Errorf("Decide = %v, want %v", decision.Outcome, tc.want)
			}
			if tc.slot != nil && decision.Found.Who != tc.slot.Who {
				t.Errorf("Decide reported holder %q, want %q", decision.Found.Who, tc.slot.Who)
			}
		})
	}

	// The outcomes that evict are the two a caller has to record a takeover for.
	for _, outcome := range []Outcome{Created, AlreadyMine, RefusedSameIdentity, RefusedFresh} {
		if outcome.TakesOver() {
			t.Errorf("%v reports that it takes the slot over", outcome)
		}
	}
	for _, outcome := range []Outcome{TakeoverExpired, TakeoverForced} {
		if !outcome.TakesOver() {
			t.Errorf("%v reports that it leaves the slot alone", outcome)
		}
	}
}

func TestSlotStatus(t *testing.T) {
	t.Parallel()

	const (
		me    = "abc#primary:wt#main"
		other = "abc#primary:other#main"
		mine  = "tok-mine"
	)
	mineSlot := &Holder{PID: "1", Who: me, Token: mine}
	othersSlot := &Holder{PID: "2", Who: other, Token: "tok-theirs"}
	theirAcquisition := &Holder{PID: "3", Who: me, Token: "tok-other"}
	// A record with no readable holder: a claim writes the slot before the record, so
	// this is a slot in progress, and it is never reported as this worktree's even when
	// the rest of the record happens to match.
	unreadable := &Holder{Who: me, Token: mine}

	cases := []struct {
		name string
		slot *Holder
		want Status
	}{
		{name: "free", slot: nil, want: Free},
		{name: "this worktree's", slot: mineSlot, want: Mine},
		{name: "another identity's", slot: othersSlot, want: Other},
		{name: "another acquisition in this worktree", slot: theirAcquisition, want: Other},
		{name: "a record that names no holder", slot: unreadable, want: Other},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := SlotStatus(tc.slot, me, mine); got != tc.want {
				t.Errorf("SlotStatus = %v, want %v", got, tc.want)
			}
		})
	}

	// The three statuses are the shell's exit statuses, and they must stay distinct:
	// a caller proceeds on one, takes the slot on another, and asks on the third.
	codes := map[int]bool{}
	for _, status := range []Status{Free, Mine, Other} {
		if codes[status.ExitCode()] {
			t.Errorf("status %v reuses an exit code", status)
		}
		codes[status.ExitCode()] = true
	}
	if Free.ExitCode() != 1 || Mine.ExitCode() != 0 || Other.ExitCode() != 2 {
		t.Errorf("exit codes = free %d, mine %d, other %d; want 1, 0, 2",
			Free.ExitCode(), Mine.ExitCode(), Other.ExitCode())
	}
}
