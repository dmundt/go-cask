package policy

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestWorktreeTableIsWellFormed pins where a task worktree lives, what it starts from and
// what protects it.
//
// Each of the three is a rule a reader relies on: the worktrees are relative to the
// primary checkout (an absolute path records the toolchain that made it, and the gate then
// refuses to run there), they start from the freshly fetched remote-tracking ref rather
// than a local branch, and the lock is the bare file name git's own prune honours.
func TestWorktreeTableIsWellFormed(t *testing.T) {
	t.Parallel()

	table := Worktrees()
	for _, field := range []struct {
		name  string
		value string
	}{
		{"Parent", table.Parent},
		{"Prefix", table.Prefix},
		{"Base", table.Base},
		{"LockFile", table.LockFile},
		{"LockMessage", table.LockMessage},
		{"PruneRefusal", table.PruneRefusal},
	} {
		if field.value == "" {
			t.Errorf("%s is empty", field.name)
		}
	}

	// A linked worktree lives inside the checkout that owns it, so the parent has to be a
	// relative path that stays there: an absolute one, or one that climbs out, would put
	// the worktree somewhere `git worktree list` and a reader do not look.
	if filepath.IsAbs(table.Parent) {
		t.Errorf("Parent = %q, want a directory relative to the checkout", table.Parent)
	}
	if strings.HasPrefix(table.Parent, "..") || strings.Contains(table.Parent, "..") {
		t.Errorf("Parent = %q, which climbs out of the checkout", table.Parent)
	}

	// The base is the remote-tracking ref, and that is the whole point: a local `main` in
	// the primary checkout can be behind the remote or carry another session's work.
	if !strings.HasPrefix(table.Base, "origin/") {
		t.Errorf("Base = %q, want a remote-tracking ref", table.Base)
	}

	// git's prune reads exactly this file beside the admin directory's `gitdir`, so it is a
	// bare name and not a path; and the lock has to say who locked it, because the file is
	// the only trace a reader finds.
	if filepath.Base(table.LockFile) != table.LockFile {
		t.Errorf("LockFile = %q, want a bare file name", table.LockFile)
	}
	if !strings.Contains(table.LockMessage, "worktree") {
		t.Errorf("LockMessage = %q, which does not name the command that wrote it", table.LockMessage)
	}

	// The refusal is a reader's only instruction: git offers no hook that could intercept
	// the command, so the message has to say what is refused, why the lock is the guard,
	// and what to run instead.
	for _, want := range []string{"prune", "locked", "worktree lock", "worktree remove"} {
		if !strings.Contains(table.PruneRefusal, want) {
			t.Errorf("PruneRefusal does not mention %q:\n%s", want, table.PruneRefusal)
		}
	}
}
