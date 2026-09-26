package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/internal/build/core/worktree"
	"github.com/dmundt/go-cask/internal/build/policy"
)

// worktreeFixture builds a repository with a bare `origin` holding one commit, so
// `origin/main` exists to base a worktree on — the one thing the command's `add` needs that
// a bare `git init` does not give it.
type worktreeFixture struct {
	primary string
	context *worktreeContext
	git     func(args ...string) string
}

func newWorktreeFixture(t *testing.T) *worktreeFixture {
	t.Helper()
	root := t.TempDir()
	primary := filepath.Join(root, "primary")
	origin := filepath.Join(root, "origin.git")

	run := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
		}
		return strings.TrimSpace(string(out))
	}

	if err := os.MkdirAll(primary, 0o755); err != nil {
		t.Fatalf("creating the fixture: %v", err)
	}
	run(root, "init", "-q", "--bare", origin)
	run(primary, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(primary, "file.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("writing the fixture file: %v", err)
	}
	run(primary, "add", "file.txt")
	run(primary, "commit", "-qm", "seed")
	run(primary, "remote", "add", "origin", origin)
	run(primary, "push", "-q", "-u", "origin", "main")
	run(primary, "fetch", "-q", "origin")

	common := filepath.Join(primary, ".git")
	return &worktreeFixture{
		primary: primary,
		context: &worktreeContext{
			primary: primary,
			common:  common,
			parent:  filepath.Join(primary, policy.Worktrees().Parent),
		},
		git: func(args ...string) string { return run(primary, args...) },
	}
}

// runWorktreeCommand drives one invocation and returns its output and exit status.
func runWorktreeCommand(t *testing.T, context *worktreeContext, args ...string) (stdout, stderr string, status int) {
	t.Helper()
	var out, errOut bytes.Buffer
	err := worktreeCommand(args, &out, &errOut, context)
	switch {
	case err == nil:
		status = 0
	default:
		var statusErr statusError
		var usage usageError
		switch {
		case errors.As(err, &statusErr):
			status = statusErr.code
			if statusErr.message != "" {
				errOut.WriteString(statusErr.message + "\n")
			}
		case errors.As(err, &usage):
			status = 2
			errOut.WriteString(usage.message + "\n")
		default:
			status = 1
			errOut.WriteString(err.Error() + "\n")
		}
	}
	return out.String(), errOut.String(), status
}

// TestWorktreeAddMakesItUsableFromBothToolchains pins what the command exists for: the
// worktree's `.git` file is relative, the registration is locked, and git inside the worktree
// resolves to the worktree's own git directory rather than to the primary checkout's.
func TestWorktreeAddMakesItUsableFromBothToolchains(t *testing.T) {
	fixture := newWorktreeFixture(t)
	table := policy.Worktrees()

	out, errOut, status := runWorktreeCommand(t, fixture.context, "add", "t1")
	if status != 0 {
		t.Fatalf("add = %d, want 0\n%s", status, errOut)
	}
	dir := filepath.Join(fixture.context.parent, table.Prefix+"t1")
	if !strings.Contains(out, "worktree ready: "+dir) {
		t.Errorf("add reported %q, want it to name the worktree", out)
	}

	content, err := os.ReadFile(filepath.Join(dir, ".git"))
	if err != nil {
		t.Fatalf("reading the worktree's .git: %v", err)
	}
	admin := worktree.Admin(fixture.context.common, table.Prefix+"t1")
	if !worktree.Resolves(string(content), dir, admin) {
		t.Errorf("the written link %q does not resolve to %q", content, admin)
	}
	if _, err := os.Stat(filepath.Join(admin, table.LockFile)); err != nil {
		t.Errorf("the worktree was not locked: %v", err)
	}

	// Git's own answer, which is the check the command makes: the same comparison from the
	// same toolchain, so no path form can hide a mismatch.
	resolved, err := gitPathIn(dir, "rev-parse", "--path-format=absolute", "--git-dir")
	if err != nil {
		t.Fatalf("git rev-parse in the worktree: %v", err)
	}
	if !worktree.SamePath(resolved, admin) {
		t.Errorf("git in the worktree resolves to %q, want %q", resolved, admin)
	}

	// A second add of the same name is refused rather than half-done.
	_, errOut, status = runWorktreeCommand(t, fixture.context, "add", "t1")
	if status != 1 || !strings.Contains(errOut, "already exists") {
		t.Errorf("a second add = %d %q, want a refusal", status, errOut)
	}
}

// TestWorktreeLockCoversWhatAddDidNotCreate pins the lock as the protection it is: a
// worktree created by plain git has no lock, and the command adds one.
func TestWorktreeLockCoversWhatAddDidNotCreate(t *testing.T) {
	fixture := newWorktreeFixture(t)
	table := policy.Worktrees()

	// A worktree the way plain git makes one: no lock, whatever toolchain created it.
	plain := filepath.Join(fixture.context.parent, table.Prefix+"plain")
	fixture.git("worktree", "add", "--detach", plain, table.Base)
	admin := worktree.Admin(fixture.context.common, table.Prefix+"plain")
	if _, err := os.Stat(filepath.Join(admin, table.LockFile)); err == nil {
		t.Fatal("the fixture's plain worktree is already locked")
	}

	out, errOut, status := runWorktreeCommand(t, fixture.context, "lock", "plain")
	if status != 0 || !strings.Contains(out, "locked: "+table.Prefix+"plain") {
		t.Fatalf("lock = %d %q, want it to lock the worktree", status, out+errOut)
	}
	if _, err := os.Stat(filepath.Join(admin, table.LockFile)); err != nil {
		t.Errorf("the lock file is missing after lock: %v", err)
	}
	// Locking twice is not an error: it is already the state the caller asked for.
	out, _, status = runWorktreeCommand(t, fixture.context, "lock", "plain")
	if status != 0 || !strings.Contains(out, "already locked: "+table.Prefix+"plain") {
		t.Errorf("a second lock = %d %q, want it reported as already locked", status, out)
	}
	// A name that is not registered is reported, and nothing is created for it.
	_, errOut, status = runWorktreeCommand(t, fixture.context, "lock", "missing")
	if status != 1 || !strings.Contains(errOut, "no such worktree") {
		t.Errorf("locking an unregistered name = %d %q, want a report", status, errOut)
	}
}

// TestWorktreeRemoveRefusesUncommittedWork pins the removal contract: an unclean worktree is
// refused with the status the wrapper documented, and --force discards.
func TestWorktreeRemoveRefusesUncommittedWork(t *testing.T) {
	fixture := newWorktreeFixture(t)
	table := policy.Worktrees()

	if _, errOut, status := runWorktreeCommand(t, fixture.context, "add", "t2"); status != 0 {
		t.Fatalf("add = %d\n%s", status, errOut)
	}
	dir := filepath.Join(fixture.context.parent, table.Prefix+"t2")
	if err := os.WriteFile(filepath.Join(dir, "dirty.txt"), []byte("work in progress\n"), 0o644); err != nil {
		t.Fatalf("dirtying the worktree: %v", err)
	}

	_, errOut, status := runWorktreeCommand(t, fixture.context, "remove", "t2")
	if status != 3 || !strings.Contains(errOut, "uncommitted changes") {
		t.Fatalf("removing a dirty worktree = %d %q, want the documented refusal", status, errOut)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("the refused removal removed the worktree anyway: %v", err)
	}

	if _, errOut, status := runWorktreeCommand(t, fixture.context, "remove", "t2", "--force"); status != 0 {
		t.Fatalf("forced removal = %d\n%s", status, errOut)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the worktree survived a forced removal: %v", err)
	}
	if _, err := os.Stat(worktree.Admin(fixture.context.common, table.Prefix+"t2")); !os.IsNotExist(err) {
		t.Errorf("the admin directory survived: %v", err)
	}
}

// TestWorktreePruneRefuses pins the refusal: prune is never performed, it is explained, and
// the status is the one the wrapper used.
func TestWorktreePruneRefuses(t *testing.T) {
	fixture := newWorktreeFixture(t)

	out, errOut, status := runWorktreeCommand(t, fixture.context, "prune")
	if status != 2 {
		t.Fatalf("prune = %d, want 2", status)
	}
	if out != "" {
		t.Errorf("prune wrote %q to stdout; a refusal belongs on stderr", out)
	}
	for _, want := range []string{"refusing to prune", "git worktree prune", "locked"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("the refusal does not explain %q:\n%s", want, errOut)
		}
	}
}

// TestWorktreeListAndUsage pins the read-only path and the invocation contract.
func TestWorktreeListAndUsage(t *testing.T) {
	fixture := newWorktreeFixture(t)

	if _, errOut, status := runWorktreeCommand(t, fixture.context, "add", "t3"); status != 0 {
		t.Fatalf("add = %d\n%s", status, errOut)
	}
	out, errOut, status := runWorktreeCommand(t, fixture.context, "list")
	if status != 0 {
		t.Fatalf("list = %d\n%s", status, errOut)
	}
	if !strings.Contains(out, "wt-t3") {
		t.Errorf("list printed %q, which does not name the worktree", out)
	}

	for _, args := range [][]string{{}, {"nope"}, {"add"}, {"list", "extra"}} {
		if _, errOut, status := runWorktreeCommand(t, fixture.context, args...); status != 2 {
			t.Errorf("worktree %q = %d %q, want the usage status", args, status, errOut)
		}
	}
}
