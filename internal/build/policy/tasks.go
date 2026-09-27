package policy

// TaskTable is the layout the task-state report reads.
//
// It is go-cask's answer, not the engine's: another repository measures against another ref and
// lists a different number of pull requests. The rules that turn those readings into findings
// are internal/build/core/taskstate's.
type TaskTable struct {
	// Base is the ref a branch is measured against, and the one that decides whether its
	// commits are landed. It is the same remote-tracking ref the task worktrees are created
	// from, so the report and `buildtool worktree add` cannot disagree about what "ahead of
	// the base" means.
	Base string
	// PullRequestLimit is how many pull requests one listing may carry. A branch whose pull
	// request fell outside the listing would be reported as unlanded, so the number is
	// deliberately generous: a false "unlanded" costs a look, a missed one costs the work.
	PullRequestLimit int
}

// Tasks returns go-cask's task-state table. It is a function rather than a package-level
// variable so a caller cannot mutate the report's policy by accident.
func Tasks() TaskTable {
	return TaskTable{
		Base:             Worktrees().Base,
		PullRequestLimit: 200,
	}
}
