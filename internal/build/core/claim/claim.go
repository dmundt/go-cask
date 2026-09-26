package claim

import (
	"encoding/json"
	"strings"
	"time"
)

// The states a coordination ref can be in. They mirror the local slot's outcomes but are
// decided from the REMOTE record: the lane lives on the server, so what these report is
// true for every clone, machine and toolchain at once.
type State int

const (
	// Free: no ref, so anybody may claim it.
	Free State = iota
	// Held: an open pull request names the issue. The PR is the lease, so a holder that
	// died is visible as a PR nobody advances rather than as a file nobody can read.
	Held
	// Claiming: a claim with no pull request yet, still inside its window — the time a
	// claimer has to create its worktree and run the gate.
	Claiming
	// Stale: a claim with no pull request whose window has passed. A claim takes it over,
	// with no human deciding whether the holder is dead.
	Stale
	// Unreadable: the ref exists but its record cannot be read, so nothing about it can be
	// decided; only a deliberate release clears it.
	Unreadable
)

// String renders a state the way the command reports it.
func (s State) String() string {
	switch s {
	case Free:
		return "free"
	case Held:
		return "held"
	case Claiming:
		return "claiming"
	case Stale:
		return "stale"
	case Unreadable:
		return "unreadable"
	default:
		return "unknown"
	}
}

// PullRequest is the open pull request that names a lane's issue: its number, and the head
// branch whose name carries that issue.
type PullRequest struct {
	Number int
	Branch string
}

// IssueOf returns the issue a coordination ref names: the ref's last segment, which must be
// digits. refPrefix is the caller's, e.g. `refs/lane/`.
func IssueOf(ref, refPrefix string) (string, bool) {
	issue, found := strings.CutPrefix(strings.TrimSpace(ref), refPrefix)
	if !found || issue == "" || strings.Contains(issue, "/") {
		return "", false
	}
	for _, digit := range issue {
		if digit < '0' || digit > '9' {
			return "", false
		}
	}
	return issue, true
}

// BranchNamesIssue reports whether a head branch names an issue. A branch carries its issue
// number as a whole segment (`<type>/<NNN>-<kebab>`), so `feat/38-x` does not name issue 38:
// the digits must stand alone between separators.
func BranchNamesIssue(branch, issue string) bool {
	if issue == "" || branch == "" {
		return false
	}
	for _, segment := range strings.FieldsFunc(branch, func(r rune) bool { return r == '/' }) {
		if segment == issue {
			return true
		}
		if rest, found := strings.CutPrefix(segment, issue+"-"); found && rest != "" {
			return true
		}
	}
	return false
}

// ClaimMessage renders the record a claim is made of: who claimed the lane, as two
// path-free names. The ref itself carries no timestamp and the claimant's pull request does
// not exist yet, so this record is the only way another session learns who holds the lane —
// and both names stay free of a path because the two toolchains here spell one directory
// differently.
func ClaimMessage(branch, worktree string) string {
	return "branch=" + branch + " worktree=" + worktree
}

// Minutes returns whole minutes between two instants, the unit the window is stated in.
func Minutes(from, to time.Time) int {
	return int(to.Sub(from).Minutes())
}

// LaneInput is what a verdict reads: whether the ref is there, whether its record could be
// read, the pull request naming the issue, and what the record says.
type LaneInput struct {
	// Present is false when no ref exists — the lane is free.
	Present bool
	// Readable is false when the ref exists but its record cannot be read.
	Readable bool
	// PullRequest is the open pull request naming the issue, if there is one.
	PullRequest *PullRequest
	// Claimer is the record's own text: whatever the claim wrote.
	Claimer string
	// Claimed is the moment the claim was recorded.
	Claimed time.Time
	// AgeKnown is false when that moment could not be read.
	AgeKnown bool
}

// Verdict is a decision about one lane.
type Verdict struct {
	// State is the verdict.
	State State
	// PullRequest is the open pull request holding the lane, for Held.
	PullRequest *PullRequest
	// Claimer is the record's text, as the record wrote it.
	Claimer string
	// Age is how long ago the claim was recorded, meaningful when AgeKnown.
	Age time.Duration
	// AgeKnown is false when the claim's moment could not be read.
	AgeKnown bool
}

// DecideLane reads a lane the way both `check` and `claim` must: one function, so the two
// can never disagree about whether a claim would succeed.
//
// The order is the protocol. An unreadable record decides nothing. An open pull request
// holds the lane whatever the claim's age — the PR is the lease. Without one, the claim is
// inside its window until the window passes, and an unreadable moment leaves the claim
// inside it: guessing that it is stale would take a live landing away.
func DecideLane(in LaneInput, window time.Duration, now time.Time) Verdict {
	if !in.Present {
		return Verdict{State: Free}
	}
	if !in.Readable {
		return Verdict{State: Unreadable}
	}
	verdict := Verdict{PullRequest: in.PullRequest, Claimer: in.Claimer}
	if verdict.PullRequest != nil {
		// A held lane is held whatever the claim's age, so the age is left unset rather
		// than reported as zero minutes.
		verdict.State = Held
		return verdict
	}
	if !in.AgeKnown {
		verdict.State = Claiming
		return verdict
	}
	verdict.Age = now.Sub(in.Claimed)
	verdict.AgeKnown = true
	if verdict.Age >= window {
		verdict.State = Stale
		return verdict
	}
	verdict.State = Claiming
	return verdict
}

// Status is one lane as `status` reports it, in the JSON shape the command promises. It is
// a typed struct rather than hand-built text so the field names, their order and the null
// cases are pinned by a test.
type Status struct {
	// Issue is the issue number the lane's ref names.
	Issue string `json:"issue"`
	// Ref is the coordination ref, e.g. `refs/lane/389`.
	Ref string `json:"ref"`
	// Object is the ref's object, the claim record's own address.
	Object string `json:"object"`
	// PullRequest is the open pull request holding the lane, or nil.
	PullRequest *int `json:"pull_request"`
	// Branch is that pull request's head branch, empty when there is none.
	Branch string `json:"branch"`
	// Claimant is the claim record's text; a record that cannot be read says so.
	Claimant string `json:"claimant"`
	// AgeMinutes is how long ago the claim was recorded, or nil when unknown.
	AgeMinutes *int `json:"age_minutes"`
	// Mine is true when this worktree recorded the issue.
	Mine bool `json:"mine"`
	// State is the verdict.
	State string `json:"state"`
}

// StatusJSON renders lanes as the JSON array the command prints.
func StatusJSON(statuses []Status) (string, error) {
	if statuses == nil {
		statuses = []Status{}
	}
	encoded, err := json.Marshal(statuses)
	if err != nil {
		return "", err
	}
	return string(encoded) + "\n", nil
}
