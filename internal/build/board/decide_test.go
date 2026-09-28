package board

import (
	"strings"
	"testing"
)

// minutes is a helper for the age a claim record carries.
func minutes(n int) *int { return &n }

// TestStalenessOf pins the liveness rule (docs/specs/coordination.md §7): a lease keeps a
// lane alive whatever its age, a claim inside its window is not to be taken, a claim past it
// is, and a record nobody can read is neither.
func TestStalenessOf(t *testing.T) {
	cases := []struct {
		name       string
		lane       *Lane
		lease      bool
		unreadable bool
		want       Staleness
	}{
		{name: "no lane at all", lane: nil, want: Unclaimed},
		{name: "a ref whose record cannot be read", lane: &Lane{AgeMinutes: minutes(5)}, unreadable: true, want: Unreadable},
		{name: "a lease, however old", lane: &Lane{AgeMinutes: minutes(5000)}, lease: true, want: Fresh},
		{name: "inside the window", lane: &Lane{AgeMinutes: minutes(10)}, want: Fresh},
		{name: "past the window", lane: &Lane{AgeMinutes: minutes(200)}, want: Stale},
		{name: "on the window's edge", lane: &Lane{AgeMinutes: minutes(90)}, want: Fresh},
		{name: "an age nobody could read", lane: &Lane{}, want: Fresh},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := StalenessOf(tc.lane, tc.lease, 90, tc.unreadable); got != tc.want {
				t.Errorf("StalenessOf = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestBoardOf pins the join: every open issue appears, each row says who holds it, where the
// work is, what the lease says, whether the head is gated evidence, and the one next action.
func TestBoardOf(t *testing.T) {
	issues := []Issue{
		{Number: 438, Title: "the board verbs"},
		{Number: 440, Title: "collisions"},
		{Number: 441, Title: "a stale claim with nothing in it"},
		{Number: 442, Title: "nobody claimed this"},
		{Number: 443, Title: "a ref nobody can read"},
	}
	lanes := map[int]*Lane{
		438: {Ref: "refs/lane/438", Object: "tag0001", Message: "branch=chore/438 worktree=wt-438", ClaimedKnown: true, AgeMinutes: minutes(5)},
		440: {Ref: "refs/lane/440", Object: "tag0002", Message: "branch=chore/440 worktree=wt-440", ClaimedKnown: true, AgeMinutes: minutes(20)},
		441: {Ref: "refs/lane/441", Object: "tag0003", Message: "branch=chore/441 worktree=wt-441", ClaimedKnown: true, AgeMinutes: minutes(400)},
		443: {Ref: "refs/lane/443", Object: "tag0004"},
	}
	pulls := map[int]*PullRequest{
		438: {Number: 470, Branch: "chore/438", HeadOID: "1111aaaa2222bbbb3333cccc4444dddd5555eeee", Draft: true, MergeState: MergeDraft, Mergeable: "MERGEABLE"},
		440: {Number: 471, Branch: "chore/440", HeadOID: "9999888877776666555544443333222211110000", MergeState: MergeBehind, Mergeable: "MERGEABLE"},
	}
	where := map[string]Where{
		"chore/438": {Path: "D:/src/.worktrees/wt-438", Unpushed: 3, Known: true},
		"chore/440": {Path: "D:/src/.worktrees/wt-440", Unpushed: 2, Known: true},
		"chore/441": {Path: "D:/src/.worktrees/wt-441", Unpushed: 0, Known: true},
	}
	gated := map[string]bool{"9999888877776666555544443333222211110000": true}

	assembled := BoardOf(issues, lanes, pulls, where, map[int]bool{443: true}, gated, []string{"gh pr view failed for #470"}, 90)
	if len(assembled.Rows) != len(issues) {
		t.Fatalf("got %d rows, want one per open issue (%d)", len(assembled.Rows), len(issues))
	}
	if len(assembled.Notes) != 1 || !strings.Contains(assembled.Notes[0], "gh pr view failed") {
		t.Errorf("notes = %v, want the caller's caveat kept", assembled.Notes)
	}
	rows := map[int]Row{}
	for _, row := range assembled.Rows {
		rows[row.Issue.Number] = row
	}

	draft := rows[438]
	if draft.Holder != "wt-438" || draft.Worktree != "D:/src/.worktrees/wt-438" {
		t.Errorf("#438 = holder %q, worktree %q; want the claim's worktree", draft.Holder, draft.Worktree)
	}
	if draft.Claim != Fresh || draft.Gated {
		t.Errorf("#438 = claim %q, gated %t; want a fresh un-gated draft's row", draft.Claim, draft.Gated)
	}
	if draft.Next != ActMarkReady {
		t.Errorf("#438 next = %q, want the draft's next step (%q)", draft.Next, ActMarkReady)
	}

	gated2 := rows[440]
	if !gated2.Gated || gated2.Claim != Fresh {
		t.Errorf("#440 = gated %t, claim %q; want a leased lane with a receipt", gated2.Gated, gated2.Claim)
	}
	if gated2.Next != ActRebuild {
		t.Errorf("#440 next = %q, want the rebuild signal (%q)", gated2.Next, ActRebuild)
	}

	abandoned := rows[441]
	if !abandoned.Abandoned || abandoned.Next != ActAbandoned {
		t.Errorf("#441 = abandoned %t, next %q; want the abandoned shape reported", abandoned.Abandoned, abandoned.Next)
	}
	if abandoned.Worktree != "D:/src/.worktrees/wt-441" {
		t.Errorf("#441 worktree = %q, want the claim's worktree", abandoned.Worktree)
	}

	free := rows[442]
	if free.Lane != nil || free.Claim != Unclaimed || free.Next != ActBaseRefOnly {
		t.Errorf("#442 = lane %v, claim %q, next %q; want an unclaimed issue", free.Lane, free.Claim, free.Next)
	}

	unreadable := rows[443]
	if unreadable.Claim != Unreadable || unreadable.Holder != "unreadable claim record" {
		t.Errorf("#443 = claim %q, holder %q; want an unreadable record named", unreadable.Claim, unreadable.Holder)
	}
	if unreadable.Next != ActInspect {
		t.Errorf("#443 next = %q, want the record read (%q)", unreadable.Next, ActInspect)
	}
}

// TestBoardOfStaleClaimWithWork pins the other half of §7's abandoned shape: a claim past
// its window whose worktree carries commits beyond the base ref is stale, not abandoned —
// releasing it would throw away the only copy of that work.
func TestBoardOfStaleClaimWithWork(t *testing.T) {
	issues := []Issue{{Number: 441, Title: "a stale claim with commits"}}
	lanes := map[int]*Lane{441: {Ref: "refs/lane/441", Message: "branch=chore/441 worktree=wt-441", AgeMinutes: minutes(400)}}
	where := map[string]Where{"chore/441": {Path: "D:/src/.worktrees/wt-441", Unpushed: 2, Known: true}}

	row := BoardOf(issues, lanes, nil, where, nil, nil, nil, 90).Rows[0]
	if row.Abandoned {
		t.Error("a stale claim whose worktree holds commits was reported as abandoned")
	}
	if row.Next != ActTakeover {
		t.Errorf("next = %q, want the claim taken over (%q)", row.Next, ActTakeover)
	}

	// An unread count is not evidence of an empty worktree either.
	unknown := BoardOf(issues, lanes, nil, map[string]Where{"chore/441": {Path: "x"}}, nil, nil, nil, 90).Rows[0]
	if unknown.Abandoned || unknown.Next != ActTakeover {
		t.Errorf("unread count: abandoned %t, next %q; want stale, not abandoned", unknown.Abandoned, unknown.Next)
	}
}

// TestNextAction pins every arm of the decision, including the one that matters most: a
// merged pull request whose lane is still held is released, and a lane inside its window is
// waited for rather than taken (docs/specs/coordination.md §7).
func TestNextAction(t *testing.T) {
	leased := func(pr *PullRequest) Row {
		return Row{Issue: Issue{Number: 1}, Lane: &Lane{Message: "branch=b worktree=w"}, PullRequest: pr, Claim: Fresh}
	}
	cases := []struct {
		name string
		row  Row
		want Action
	}{
		{name: "an open issue nobody touched", row: Row{Issue: Issue{Number: 1}}, want: ActBaseRefOnly},
		{name: "an unreadable claim record", row: Row{Issue: Issue{Number: 1}, Lane: &Lane{}, Claim: Unreadable}, want: ActInspect},
		{name: "a claim inside its window", row: Row{Issue: Issue{Number: 1}, Lane: &Lane{}, Claim: Fresh}, want: ActWaitLease},
		{name: "a stale claim", row: Row{Issue: Issue{Number: 1}, Lane: &Lane{}, Claim: Stale}, want: ActTakeover},
		{name: "an abandoned claim", row: Row{Issue: Issue{Number: 1}, Lane: &Lane{}, Claim: Stale, Abandoned: true}, want: ActAbandoned},
		{name: "an armed, mergeable pull request", row: leased(&PullRequest{AutoMerge: true, Mergeable: "MERGEABLE"}),
			want: ActArmed},
		{name: "an armed pull request the forge still blocks",
			row:  leased(&PullRequest{AutoMerge: true, Mergeable: "MERGEABLE", MergeState: MergeBlocked}),
			want: ActWaitChecks},
		{name: "a draft", row: leased(&PullRequest{Draft: true, MergeState: MergeDraft, Mergeable: "MERGEABLE"}),
			want: ActMarkReady},
		{name: "a conflicting pull request", row: leased(&PullRequest{MergeState: MergeDirty, Mergeable: "CONFLICTING"}),
			want: ActRebuild},
		{name: "an un-gated head", row: leased(&PullRequest{MergeState: MergeClean, Mergeable: "MERGEABLE"}), want: ActGate},
		{name: "a draft leaves the ready draft's row to mark it first", row: leased(&PullRequest{Draft: true, MergeState: MergeDirty, Mergeable: "CONFLICTING"}),
			want: ActMarkReady},
		{name: "a stale base", row: Row{PullRequest: &PullRequest{MergeState: MergeBehind, Mergeable: "MERGEABLE"}, Gated: true},
			want: ActRebuild},
		{name: "ready, green and armed by hand", row: Row{PullRequest: &PullRequest{MergeState: MergeClean, Mergeable: "MERGEABLE"}, Gated: true},
			want: ActArmAuto},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NextAction(tc.row); got != tc.want {
				t.Errorf("NextAction = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestHolderOf pins the holder reading: the worktree the claim message names, an unnamed
// claim, and a record that could not be read at all.
func TestHolderOf(t *testing.T) {
	cases := []struct {
		name       string
		lane       *Lane
		unreadable bool
		want       string
	}{
		{name: "no lane", lane: nil, want: "—"},
		{name: "an unreadable record", lane: &Lane{}, unreadable: true, want: "unreadable claim record"},
		{name: "the named worktree", lane: &Lane{Message: "branch=chore/lane/389 worktree=wt-389"}, want: "wt-389"},
		{name: "a message naming no worktree", lane: &Lane{Message: "claimed"}, want: "unnamed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := holderOf(tc.lane, tc.unreadable); got != tc.want {
				t.Errorf("holderOf = %q, want %q", got, tc.want)
			}
		})
	}
}
