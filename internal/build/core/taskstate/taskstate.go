// Package taskstate reports which branches carry work that no pull request tracks.
//
// It is the join nothing in this repository used to make. `git worktree list` knows the
// worktrees, `git rev-list --count` knows how far a branch has drifted from the base ref,
// `gh` knows the pull requests and the lane refs know the claims — but none of them answers
// "which branch carries commits that are not on main and that no open pull request will
// land". Answering it by hand, during the audit of scripts/ and internal/build/, found seven
// branches in that state; one of them held the fix for the only failure blocking an open pull
// request, and it had sat unpushed for eighteen hours.
//
// The package decides nothing and reads nothing: the caller hands it Branch values and
// Findings reports the states worth acting on, so the rule is covered by a table test instead
// of by running the command against a repository.
package taskstate

import (
	"fmt"
	"sort"
)

// The pull-request states `gh pr list --json state` reports.
const (
	Open   = "OPEN"
	Merged = "MERGED"
	Closed = "CLOSED"
)

// PullRequest is the pull request that names a branch.
type PullRequest struct {
	Number int
	State  string
}

// String renders it the way the report states it.
func (p PullRequest) String() string { return fmt.Sprintf("#%d %s", p.Number, p.State) }

// Branch is one local branch's state, as the caller read it: the counts and flag from git,
// the pull request from the forge, and whether a linked worktree holds it.
type Branch struct {
	// Name is the branch's short name, which is also how a pull request names it.
	Name string
	// Head is the abbreviated commit, for the report only.
	Head string
	// Ahead and Behind are the counts against the base ref.
	Ahead  int
	Behind int
	// Dirty is true when a worktree has the branch checked out with uncommitted changes.
	Dirty bool
	// Worktree is true when a linked worktree has it checked out.
	Worktree bool
	// PullRequest is nil when no pull request names the branch.
	PullRequest *PullRequest
}

// Kind is the state a finding names.
type Kind string

const (
	// Unlanded is a branch carrying commits the base ref does not have, with no open pull
	// request to land them. It is the state that loses work: nothing on the forge knows the
	// commits exist, no lane holds them, and only a clone that has the branch can land it.
	Unlanded Kind = "carries commits, no pull request"
	// Leftover is a branch whose pull request has finished and whose content is therefore on
	// the base ref. It is debris rather than risk, and safe to delete.
	Leftover Kind = "pull request finished, branch still here"
)

// Finding is one state worth acting on.
type Finding struct {
	Kind   Kind
	Branch string
	Detail string
}

// Findings reports the branches worth acting on, ordered by kind and then by name, so that two
// runs over one repository print the same report.
//
// A branch with an open pull request is never a finding: the pull request is the lease, and it
// is exactly what makes the work visible. A branch that has nothing the base ref lacks is not
// one either — that is the base ref's own state, or a branch already contained in it.
//
// A pull request in any state this build does not know is treated as unlanded, not as
// finished: a report that stays quiet about work it does not understand is the failure this
// command exists to prevent.
func Findings(branches []Branch) []Finding {
	found := make([]Finding, 0, len(branches))
	for _, b := range branches {
		switch {
		case b.Ahead == 0:
			// Nothing of its own to land, whatever a pull request says about it.
		case b.PullRequest == nil:
			found = append(found, Finding{Unlanded, b.Name,
				fmt.Sprintf("%s ahead of the base ref and no pull request names it%s", commits(b.Ahead), worktreeClause(b))})
		case b.PullRequest.State == Open:
			// The lease: something will land it.
		case b.PullRequest.State == Merged:
			found = append(found, Finding{Leftover, b.Name,
				fmt.Sprintf("%s merged, so %s %s on the base ref through it", *b.PullRequest, commits(b.Ahead), isAre(b.Ahead))})
		default:
			found = append(found, Finding{Unlanded, b.Name,
				fmt.Sprintf("%s is not open and %s %s unlanded", *b.PullRequest, commits(b.Ahead), isAre(b.Ahead))})
		}
	}
	sort.SliceStable(found, func(i, j int) bool {
		if found[i].Kind != found[j].Kind {
			return found[i].Kind < found[j].Kind
		}
		return found[i].Branch < found[j].Branch
	})
	return found
}

// commits renders a count as a phrase, and isAre the verb that agrees with it, so a finding
// reads as prose for one commit and for many.
func commits(n int) string {
	if n == 1 {
		return "1 commit"
	}
	return fmt.Sprintf("%d commits", n)
}

// isAre is the verb that agrees with commits.
func isAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

// worktreeClause tells the two kinds of unlanded branch apart, which is the difference between
// a session that is working and work nobody is on: a branch a linked worktree holds is in
// flight, and one nothing holds is only in a clone.
func worktreeClause(b Branch) string {
	if b.Worktree {
		return " (a worktree holds it)"
	}
	return ", and no worktree holds it"
}
