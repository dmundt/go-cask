// Package board assembles the coordinator's one view of many landings at once: every
// open issue with its lane state, the file overlaps that make two lanes one wave's
// problem, and the six checks that prove a landing rather than repeat the claim that it
// happened.
//
// It joins sources that were read one at a time and by hand: the OPEN issue list knows what
// is outstanding, the lane refs know who claimed what, the worktree list knows where the
// work is checked out, the forge knows the pull request that is the lease, and the clone
// ledger and its receipts are the only evidence a gate ran — a lane reporting "gated green"
// is a claim, and the board must never print it as a fact (docs/specs/coordination.md §3).
//
// The package decides and parses; the caller reads the world. Every authority arrives as a
// value the command produced — including the forge listings, which are parsed here so the
// decision is covered by a fixture test rather than by a live `gh`. Nothing in this package
// names this repository: the serialization points, the base ref and the lane ref prefix are
// callers' tables.
package board

import (
	"sort"
	"strings"
	"time"
)

// Version is the board's own record format. The command prints it so a reader can tell one
// build's report from another's, and so a saved report can be read back.
const Version = 1

// Issue is one open issue, as the forge lists it. It is the spine of the board: a lane is
// only interesting when it claims an issue that is still open, and an issue with no lane is
// outstanding work (docs/specs/coordination.md §2).
type Issue struct {
	Number int      `json:"number"`
	Title  string   `json:"title"`
	Labels []string `json:"labels,omitempty"`
	URL    string   `json:"url,omitempty"`
}

// Lane is one claimed lane, as the remote holds it. Everything here comes from the claim
// ref and the tag object it points at — the ref carries no timestamp, which is why the
// record is read as well.
type Lane struct {
	// Ref is the full ref, `refs/lane/<issue>` under go-cask's table.
	Ref string `json:"ref"`
	// Object is the claim ref's object: the tag object's own address, for the report.
	Object string `json:"object,omitempty"`
	// Message is the claim record's text, which names the branch and the worktree.
	Message string `json:"message,omitempty"`
	// Claimed is when the claim was written. It is only meaningful with ClaimedKnown.
	Claimed time.Time `json:"claimed,omitempty"`
	// ClaimedKnown is false when the record carried no parseable moment: a claim whose age
	// cannot be read is one to look at, not one to guess about.
	ClaimedKnown bool `json:"claimedKnown"`
	// AgeMinutes is how long ago the claim was written, nil when it is unknown.
	AgeMinutes *int `json:"ageMinutes,omitempty"`
}

// PullRequest is the lease behind a lane, as the forge reports it. Draft and the auto-merge
// request are both here because a board that omits them is not a board: a green draft and a
// green armed pull request are indistinguishable, and only one of them can land
// (docs/specs/coordination.md §2).
type PullRequest struct {
	Number int    `json:"number"`
	Title  string `json:"title,omitempty"`
	Branch string `json:"branch"`
	// HeadOID is the pull request's head commit, which is the commit a gate would have
	// verified and the only commit a receipt can exist for.
	HeadOID string `json:"headOid,omitempty"`
	// Draft is true when GitHub refuses to arm auto-merge on it.
	Draft bool `json:"draft"`
	// AutoMerge is true when a merge is armed and GitHub owns the merge from here.
	AutoMerge bool `json:"autoMerge"`
	// MergeState is the forge's `mergeStateStatus`: CLEAN, BEHIND, BLOCKED, DIRTY, DRAFT,
	// UNKNOWN. `DIRTY` is the rebuild signal, not a description of the code.
	MergeState string `json:"mergeState,omitempty"`
	// Mergeable is the forge's `mergeable` verdict alone, which is UNKNOWN while it computes.
	Mergeable string `json:"mergeable,omitempty"`
}

// MergeStates the forge reports, named so a caller reads a reason rather than a string.
const (
	// MergeClean: mergeable, with the base ref merged in.
	MergeClean = "CLEAN"
	// MergeBehind: mergeable, but the head does not carry the base ref's current tip.
	MergeBehind = "BEHIND"
	// MergeBlocked: mergeable, but a required check or review is missing.
	MergeBlocked = "BLOCKED"
	// MergeDirty: conflicting. The rebuild signal (docs/specs/coordination.md §4).
	MergeDirty = "DIRTY"
	// MergeDraft: a draft, which GitHub will not arm auto-merge on.
	MergeDraft = "DRAFT"
	// MergeUnknown: the forge has not decided yet.
	MergeUnknown = "UNKNOWN"
)

// Worktree is one registration of `git worktree list`, which is the authority for the
// worktree's existence — not a removal's own message (docs/specs/coordination.md §3).
type Worktree struct {
	Path   string
	Branch string
	Head   string
	Bare   bool
	// Detached is a registration holding no branch.
	Detached bool
	// Unpushed is how many commits the worktree's branch carries beyond the base ref. It
	// is what separates a session that is working from an abandoned claim (§7): the shape
	// to report is a claim whose worktree holds no commits beyond the base ref.
	Unpushed int
}

// Staleness is what the claim record and the lease say about a lane's liveness.
type Staleness string

const (
	// Stale: no lease and the claim is past the window — the next claimer takes it over.
	Stale Staleness = "stale"
	// Fresh: no lease yet, but the claim is inside the window the claimer was given.
	Fresh Staleness = "fresh"
	// Unclaimed: no lane ref, so no window is running.
	Unclaimed Staleness = "unclaimed"
	// Unreadable: the ref exists but its record cannot be read — never taken over silently.
	Unreadable Staleness = "unreadable"
)

// Action is the single next step a row reports. It is prose because it is read by a
// session deciding what to do next, and one string because a row that listed its options
// would leave the reader to make the decision again.
type Action string

// The next actions a row can carry, in the order the coordinator would act.
const (
	ActArmed       Action = "none — an armed pull request lands itself"
	ActRelease     Action = "release the lane (the pull request merged)"
	ActMarkReady   Action = "mark the pull request ready and arm auto-merge"
	ActArmAuto     Action = "arm auto-merge (the pull request is ready and green)"
	ActWaitChecks  Action = "wait: an armed pull request the forge still reports BLOCKED — a required check or review is missing"
	ActRebuild     Action = "rebuild: merge main locally, resolve, re-gate, push --force-with-lease"
	ActGate        Action = "run the gate: the head has no receipt for this commit"
	ActWaitLease   Action = "wait: the claim is inside its window and has no pull request yet"
	ActTakeover    Action = "take the lane over: the claim is stale and carries no pull request"
	ActClaim       Action = "claim the lane: the issue is open and nobody holds it"
	ActInspect     Action = "read the claim record: the ref exists but cannot be read"
	ActAbandoned   Action = "release the lane: the claim is past its window and its worktree holds nothing new"
	ActBaseRefOnly Action = "choose: the issue is open and no lane, worktree or pull request names it"
)

// Row is one issue's line of the board: what the lane claims, where the work is, the lease
// behind it, whether the head is gated, and the one thing to do next.
type Row struct {
	Issue Issue `json:"issue"`
	// Lane is nil when no lane ref exists for the issue.
	Lane *Lane `json:"lane"`
	// PullRequest is nil when no open pull request names the issue.
	PullRequest *PullRequest `json:"pullRequest"`
	// Worktree is the path the lane's branch is checked out at, "" when none is.
	Worktree string `json:"worktree,omitempty"`
	// Holder is the claim message's worktree name: who wrote the claim. "—" when the
	// record cannot be read.
	Holder string `json:"holder"`
	// Claim is the board's staleness reading of the claim (docs/specs/coordination.md §7).
	Claim Staleness `json:"claim"`
	// Gated is true only when the clone ledger names the head commit AND a receipt for it
	// exists — both, because one without the other is not evidence (§3).
	Gated bool `json:"gated"`
	// Abandoned is true when the claim is past its window and its worktree holds no
	// commits beyond the base ref (§7).
	Abandoned bool `json:"abandoned"`
	// Next is the one next action.
	Next Action `json:"next"`
}

// Board is the assembled view and the caveats that came with reading it.
type Board struct {
	Version int   `json:"version"`
	Rows    []Row `json:"rows"`
	// Notes are readings that failed and what the report therefore cannot say. A board
	// that quietly omitted a source is the failure this package exists to prevent.
	Notes []string `json:"notes,omitempty"`
}

// Where is the worktree a lane's branch is checked out at, and how far the branch has got.
// It is the join the caller makes from `git worktree list` and one `git rev-list --count`.
type Where struct {
	// Path is the worktree's directory.
	Path string
	// Unpushed is how many commits the branch carries beyond the base ref. It is only
	// meaningful with Known.
	Unpushed int
	// Known is false when the count could not be read, so a claim is never called
	// abandoned on the strength of a reading nobody made.
	Known bool
}

// StalenessOf reads a lane's liveness against the window a claim is honoured for. A lane
// with a lease is never stale: the open pull request is what keeps it (coordination.md §3).
// An unreadable record outranks the lease, because the claim is the thing that could not be
// read — reporting it as fresh would hide the only problem the record has.
func StalenessOf(lane *Lane, hasPullRequest bool, windowMinutes int, unreadable bool) Staleness {
	switch {
	case lane == nil:
		return Unclaimed
	case unreadable:
		return Unreadable
	case hasPullRequest:
		return Fresh
	case lane.AgeMinutes == nil:
		return Fresh
	case *lane.AgeMinutes > windowMinutes:
		return Stale
	default:
		return Fresh
	}
}

// BoardOf joins one issue list to the lanes, worktrees, pull requests and evidence the
// caller read. Every open issue appears, whether or not a lane claims it: an issue nobody
// claimed is outstanding work, and the board is the list of work, not the list of claims.
//
// pulls maps an issue number to the pull request that names it; where maps a branch to the
// worktree holding it; unreadable names the issues whose claim ref exists but whose record
// could not be read; gated maps a commit to whether the clone ledger and a receipt both name
// it. A note is recorded for every reading the caller could not make, so the report can say
// what it does not know instead of reporting a gap as good news.
func BoardOf(issues []Issue, lanes map[int]*Lane, pulls map[int]*PullRequest, where map[string]Where, unreadable map[int]bool, gated map[string]bool, notes []string, windowMinutes int) Board {
	assembled := Board{Version: Version, Notes: append([]string(nil), notes...)}
	for _, issue := range issues {
		row := Row{Issue: issue, Lane: lanes[issue.Number], PullRequest: pulls[issue.Number]}
		row.Claim = StalenessOf(row.Lane, row.PullRequest != nil, windowMinutes, unreadable[issue.Number])
		row.Holder = holderOf(row.Lane, unreadable[issue.Number])
		branch := ""
		if row.PullRequest != nil {
			branch = row.PullRequest.Branch
		} else if row.Lane != nil {
			branch = branchOf(row.Lane.Message)
		}
		spot, found := where[branch]
		if found {
			row.Worktree = spot.Path
			// The abandoned shape (§7): a claim past its window whose worktree holds
			// nothing beyond the base ref. Both halves are required — an unread count
			// says nothing, and a claim with no worktree at all is only stale.
			row.Abandoned = row.Claim == Stale && row.PullRequest == nil && spot.Known && spot.Unpushed == 0
		}
		if row.PullRequest != nil && row.PullRequest.HeadOID != "" {
			row.Gated = gated[row.PullRequest.HeadOID]
		}
		row.Next = NextAction(row)
		assembled.Rows = append(assembled.Rows, row)
	}
	sort.SliceStable(assembled.Rows, func(i, j int) bool {
		return assembled.Rows[i].Issue.Number < assembled.Rows[j].Issue.Number
	})
	return assembled
}

// NextAction is the one step a row's state calls for, read from the row alone. It reports
// the binding constraint rather than the first thing that is technically true: a draft that
// cannot be armed and a conflicting pull request are both "not landing", but only one of
// them needs a rebuild.
func NextAction(row Row) Action {
	switch {
	case row.PullRequest == nil && row.Lane == nil:
		return ActBaseRefOnly
	case row.PullRequest == nil && row.Claim == Unreadable:
		return ActInspect
	case row.PullRequest == nil && row.Claim == Stale:
		if row.Abandoned {
			return ActAbandoned
		}
		return ActTakeover
	case row.PullRequest == nil:
		return ActWaitLease
	// A draft first: GitHub refuses to arm auto-merge on one, so a draft is not landing
	// whatever else the forge says about it (docs/specs/coordination.md §2).
	case row.PullRequest.Draft:
		return ActMarkReady
	case row.PullRequest.MergeState == MergeDirty:
		return ActRebuild
	case row.PullRequest.AutoMerge && row.PullRequest.Mergeable == "MERGEABLE" && row.PullRequest.MergeState != MergeBlocked:
		return ActArmed
	case row.PullRequest.MergeState == MergeBlocked:
		return ActWaitChecks
	case !row.Gated:
		return ActGate
	case row.PullRequest.MergeState == MergeBehind:
		return ActRebuild
	default:
		return ActArmAuto
	}
}

// holderOf is who the claim record says wrote the lane: the worktree named in its message,
// which is the only identity a claim carries that a reader can act on.
func holderOf(lane *Lane, unreadable bool) string {
	switch {
	case lane == nil:
		return "—"
	case unreadable:
		return "unreadable claim record"
	default:
		if worktree := fieldOf(lane.Message, "worktree="); worktree != "" {
			return worktree
		}
		return "unnamed"
	}
}

// branchOf reads the branch a claim message names.
func branchOf(message string) string { return fieldOf(message, "branch=") }

// fieldOf reads one `name=value` field out of a claim message.
func fieldOf(message, prefix string) string {
	for _, field := range strings.Fields(message) {
		if value, found := strings.CutPrefix(field, prefix); found {
			return value
		}
	}
	return ""
}

// WorktreesOf turns the worktree registrations into the branch-to-place map the board joins
// on. A bare or detached registration names no branch, so it joins nothing. A branch held by
// more than one registration keeps the first, which is the order `git worktree list` prints.
func WorktreesOf(list []Worktree) map[string]Where {
	held := make(map[string]Where, len(list))
	for _, entry := range list {
		if entry.Branch == "" {
			continue
		}
		if _, seen := held[entry.Branch]; !seen {
			held[entry.Branch] = Where{Path: entry.Path, Unpushed: entry.Unpushed, Known: true}
		}
	}
	return held
}
