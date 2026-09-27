package policy

import (
	"strings"
	"testing"
)

// TestTasksIsWellFormed pins the table the report reads: a base ref a branch can be measured
// against, and a listing limit generous enough that a pull request does not fall outside it and
// turn a landed branch into a reported "unlanded" one.
func TestTasksIsWellFormed(t *testing.T) {
	t.Parallel()

	table := Tasks()
	if strings.TrimSpace(table.Base) == "" {
		t.Error("Base is empty; a branch cannot be measured against nothing")
	}
	if strings.ContainsAny(table.Base, " \t") {
		t.Errorf("Base = %q, which is not a ref", table.Base)
	}
	if table.PullRequestLimit < 100 {
		t.Errorf("PullRequestLimit = %d, too small: a pull request outside the listing reads as unlanded work",
			table.PullRequestLimit)
	}
}

// TestTasksMeasuresAgainstTheWorktreeBase pins the one thing that would make the report lie:
// measuring against a ref the worktrees are not created from. A branch based on `origin/main`
// is "ahead of the base" exactly when the worktree command would have given it those commits.
func TestTasksMeasuresAgainstTheWorktreeBase(t *testing.T) {
	t.Parallel()

	if got, want := Tasks().Base, Worktrees().Base; got != want {
		t.Errorf("Tasks().Base = %q, want the worktree base %q: the report and `worktree add` must measure the same thing",
			got, want)
	}
}
