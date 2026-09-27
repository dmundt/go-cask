package policy

// WorktreeTable is where a task worktree lives, what it starts from, and what protects it
// from a `git worktree prune` run by the other toolchain.
//
// The location is derived from the shared git directory rather than the working directory,
// so the command behaves the same however it is invoked — including from a task worktree,
// where a second task must still land in the primary checkout's own scratch tree.
type WorktreeTable struct {
	// Parent is the directory inside the primary checkout that holds the linked
	// worktrees.
	Parent string
	// Prefix is the name the worktree directory and git's admin directory share.
	Prefix string
	// Base is the ref a task worktree starts from: the freshly fetched remote-tracking ref,
	// never a local branch, which in the primary checkout can be behind the remote or carry
	// another session's uncommitted work.
	Base string
	// LockFile is the file git's own `prune` honours; a name that exists there makes prune
	// skip the registration.
	LockFile string
	// LockMessage is what that file says, so whoever finds the admin directory learns who
	// locked it and why.
	LockMessage string
	// PruneRefusal is what the command prints instead of pruning, and why: git has no
	// pre-command hook and no alias can shadow a built-in, so a refusal plus the lock is the
	// whole protection.
	PruneRefusal string
}

// Worktrees returns go-cask's worktree table. It is a function rather than a package-level
// variable so a caller cannot mutate the policy by accident.
func Worktrees() WorktreeTable {
	return WorktreeTable{
		Parent:   ".gocache",
		Prefix:   "wt-",
		Base:     "origin/main",
		LockFile: "locked",
		LockMessage: "locked by go run ./cmd/buildtool worktree — the .git link is " +
			"toolchain-relative; never run git worktree prune\n",
		PruneRefusal: "worktree: refusing to prune.\n" +
			"\n" +
			"`git worktree prune` deletes every registration whose admin `gitdir` file points\n" +
			"at a path this toolchain cannot resolve — and that link can only hold one absolute\n" +
			"path form, so it is unresolvable for every worktree the *other* toolchain created.\n" +
			"That is how three live worktrees lost their registrations and their indexes here.\n" +
			"\n" +
			"git has no pre-command hook, so nothing can intercept `git worktree prune` and no\n" +
			"alias can shadow a built-in; git's own `locked` file is the only real protection,\n" +
			"and it does hold: prune skips a locked worktree from either toolchain, while an\n" +
			"identical unlocked one is deleted.\n" +
			"\n" +
			"  go run ./cmd/buildtool worktree lock [<name>...]   # one, several, or all\n" +
			"  go run ./cmd/buildtool worktree list               # what is registered, and locked\n" +
			"  go run ./cmd/buildtool worktree remove <name>      # remove exactly one, on purpose\n",
	}
}
