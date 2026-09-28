package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/internal/build/policy"
	"github.com/dmundt/go-cask/internal/build/worktree"
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
// worktree created by plain git has no lock, and the command adds one. A name that is not
// registered is reported and creates nothing — and it is not fatal, because a gate run has to
// lock the worktrees that exist and report the ones it cannot (go-cask#508).
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
	// A name that is not registered is reported, nothing is created for it, and the run
	// still succeeds: the gate's first step may not abort over a name it cannot lock.
	_, errOut, status = runWorktreeCommand(t, fixture.context, "lock", "missing")
	if status != 0 || !strings.Contains(errOut, "no such worktree") {
		t.Errorf("locking an unregistered name = %d %q, want a report and no failure", status, errOut)
	}
	if _, err := os.Stat(worktree.Admin(fixture.context.common, table.Prefix+"missing")); !os.IsNotExist(err) {
		t.Errorf("a name git registers nowhere had an admin directory created for it: %v", err)
	}
}

// TestWorktreeLockFollowsGitToAWorktreeOutsideThePolicyParent pins go-cask#508. The lock
// derived each registered worktree's admin directory from policy — `wt-` plus the bare name
// under the shared git dir — so a worktree git registers at `wt366-check-c2b` anywhere else
// became `wt-wt366-check-c2b`, whose `os.Stat` failed and whose name the whole gate then
// aborted on, in every lane of the clone, before its first step.
func TestWorktreeLockFollowsGitToAWorktreeOutsideThePolicyParent(t *testing.T) {
	fixture := newWorktreeFixture(t)
	table := policy.Worktrees()

	// A worktree whose directory git names without the policy prefix, created outside the
	// policy parent: an experiment tree, exactly the shape that cost a gate run.
	elsewhere := filepath.Join(t.TempDir(), "wt366-check-c2b")
	fixture.git("worktree", "add", "--detach", elsewhere, table.Base)
	admin := worktree.Admin(fixture.context.common, "wt366-check-c2b")
	if _, err := os.Stat(filepath.Join(admin, table.LockFile)); err == nil {
		t.Fatal("the fixture's worktree is already locked")
	}

	// The gate's own invocation: quiet, and every registered worktree locked.
	out, errOut, status := runWorktreeCommand(t, fixture.context, "lock", "--quiet")
	if status != 0 {
		t.Fatalf("locking every registered worktree = %d\n%s", status, errOut)
	}
	if !strings.Contains(out, "locked: wt366-check-c2b") {
		t.Errorf("the run reported %q, want it to name the worktree it locked", out)
	}
	if _, err := os.Stat(filepath.Join(admin, table.LockFile)); err != nil {
		t.Errorf("the worktree outside the policy parent was not locked: %v", err)
	}
	// The policy path git never reported was not touched, and no double-prefixed
	// registration was invented for it.
	if _, err := os.Stat(worktree.Admin(fixture.context.common, table.Prefix+"wt366-check-c2b")); !os.IsNotExist(err) {
		t.Errorf("a path computed from policy was created: %v", err)
	}
}

// TestWorktreeLockReportsARegistrationWhoseAdminIsGone pins the other half of go-cask#508: a
// registration git reports whose admin directory is genuinely gone is reported and skipped,
// never fatal. The gate locks what exists; a registration it cannot lock is not a reason to
// abort a run on a tree with nothing wrong in it.
func TestWorktreeLockReportsARegistrationWhoseAdminIsGone(t *testing.T) {
	fixture := newWorktreeFixture(t)
	table := policy.Worktrees()

	// The registration git reports and the admin directory it points at: the worktree's own
	// `.git` names the admin directory, so moving that directory away leaves git reporting
	// the worktree with nothing to lock.
	orphan := filepath.Join(fixture.primary, "outside", table.Prefix+"orphan")
	fixture.git("worktree", "add", "--detach", orphan, table.Base)
	admin := worktree.Admin(fixture.context.common, table.Prefix+"orphan")
	if err := os.Rename(admin, admin+".gone"); err != nil {
		t.Fatalf("moving the admin directory aside: %v", err)
	}

	out, errOut, status := runWorktreeCommand(t, fixture.context, "lock", "--quiet")
	if status != 0 {
		t.Fatalf("locking with a registration whose admin is gone = %d, want no failure\n%s", status, errOut)
	}
	if !strings.Contains(errOut, "admin directory is gone") || !strings.Contains(errOut, table.Prefix+"orphan") {
		t.Errorf("the report %q does not name the registration it could not lock", errOut)
	}
	if strings.Contains(out, "locked: "+table.Prefix+"orphan") {
		t.Errorf("the run reported a lock it did not write: %q", out)
	}
	if _, err := os.Stat(admin); !os.IsNotExist(err) {
		t.Errorf("the run recreated an admin directory for a registration whose own is gone: %v", err)
	}
}

// pointOriginAtMissingRemote repoints the fixture's `origin` at a directory that is not a
// repository, which is how a real failed fetch arrives: the toolchain runs `git fetch`, the
// remote cannot be reached, and the command has to decide what to do about it. The remote's
// URL is set in the local config so both toolchains read the same broken remote.
func pointOriginAtMissingRemote(t *testing.T, fixture *worktreeFixture) {
	t.Helper()
	missing := filepath.Join(t.TempDir(), "gone.git")
	if _, err := gitOutputIn(fixture.primary, "remote", "set-url", "origin", missing); err != nil {
		t.Fatalf("repointing origin: %v", err)
	}
	if _, err := gitOutputIn(fixture.primary, "fetch", "--quiet", "--all"); err == nil {
		t.Fatal("the fixture's fetch succeeded against a remote that is not there")
	}
}

// TestWorktreeAddRefusesAStaleBase pins go-cask#344: a failed `git fetch` refuses the add
// rather than warning and basing the new worktree on whatever local `origin/main` happens to
// be. The invariant every session relies on is that a task worktree starts on current
// `origin/main`, and a warning turned that into "whatever was last fetched".
func TestWorktreeAddRefusesAStaleBase(t *testing.T) {
	fixture := newWorktreeFixture(t)
	pointOriginAtMissingRemote(t, fixture)
	table := policy.Worktrees()
	dir := filepath.Join(fixture.context.parent, table.Prefix+"stale")

	out, errOut, status := runWorktreeCommand(t, fixture.context, "add", "stale")
	if status != 3 {
		t.Fatalf("add with a failed fetch = %d, want the documented status 3\n%s", status, errOut)
	}
	if out != "" {
		t.Errorf("the refusal wrote %q to stdout", out)
	}
	for _, want := range []string{"git fetch", "cannot be shown to be current", "--allow-stale"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("the refusal %q does not carry %q", errOut, want)
		}
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("a refused add created the worktree anyway: %v", err)
	}
}

// TestWorktreeAddAllowStaleOptsIn pins the deliberate half of the refusal: the caller who has
// just fetched by other means says so, and the add proceeds — saying which stale base it used.
func TestWorktreeAddAllowStaleOptsIn(t *testing.T) {
	fixture := newWorktreeFixture(t)
	pointOriginAtMissingRemote(t, fixture)
	table := policy.Worktrees()
	dir := filepath.Join(fixture.context.parent, table.Prefix+"stale")

	want, err := gitOutputIn(fixture.primary, "rev-parse", table.Base)
	if err != nil {
		t.Fatalf("resolving the base: %v", err)
	}

	out, errOut, status := runWorktreeCommand(t, fixture.context, "add", "--allow-stale", "stale")
	if status != 0 {
		t.Fatalf("add --allow-stale = %d\n%s", status, errOut)
	}
	if !strings.Contains(errOut, "using the local "+table.Base) {
		t.Errorf("the run did not say which base it accepted: %q", errOut)
	}
	// The full commit id: the transcript names the exact base, not a short sha a later fetch
	// could leave ambiguous.
	full := strings.TrimSpace(want)
	if !strings.Contains(out, "on "+full) {
		t.Errorf("the ready line %q does not name the full base commit %s", out, full)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("the accepted add did not create the worktree: %v", err)
	}
}

// TestWorktreeAddNamesTheFullBaseCommit pins the other half of go-cask#344: the `worktree
// ready:` line identifies the base by its full commit id.
func TestWorktreeAddNamesTheFullBaseCommit(t *testing.T) {
	fixture := newWorktreeFixture(t)
	table := policy.Worktrees()

	full, err := gitOutputIn(fixture.primary, "rev-parse", table.Base)
	if err != nil {
		t.Fatalf("resolving the base: %v", err)
	}
	full = strings.TrimSpace(full)
	if len(full) != 40 {
		t.Fatalf("the fixture's base is %q, not a full commit id", full)
	}

	out, errOut, status := runWorktreeCommand(t, fixture.context, "add", "fullline")
	if status != 0 {
		t.Fatalf("add = %d\n%s", status, errOut)
	}
	if !strings.Contains(out, "on "+full) {
		t.Errorf("the ready line %q does not name the full base commit %s", out, full)
	}
	if strings.Contains(out, "on "+full[:7]) && !strings.Contains(out, "on "+full) {
		t.Errorf("the ready line %q names an abbreviated base", out)
	}
}

// TestWorktreeAddUsageCoversTheNewFlag pins the invocation contract: `--allow-stale` is a
// flag, not the name, and a caller who passes it alone gets the usage rather than a worktree
// named after it.
func TestWorktreeAddUsageCoversTheNewFlag(t *testing.T) {
	fixture := newWorktreeFixture(t)

	for _, args := range [][]string{{"add"}, {"add", "--allow-stale"}, {"add", "t", "b", "extra"}} {
		_, errOut, status := runWorktreeCommand(t, fixture.context, args...)
		if status != 2 {
			t.Errorf("worktree %q = %d %q, want the usage status", args, status, errOut)
		}
	}
	if !strings.Contains(worktreeUsage, "--allow-stale") {
		t.Errorf("the usage %q does not document the flag", worktreeUsage)
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

// TestWorktreeRemoveOfAnUnknownNameIsRefused pins the evidence rule: `os.RemoveAll` reports
// success for a path that is not there, so the verb has to ask whether the worktree exists at
// all. A mistyped name used to print `worktree removed` and exit 0 having done nothing, and the
// `wt-` prefix is added by the command, so an already-prefixed name names nothing either.
func TestWorktreeRemoveOfAnUnknownNameIsRefused(t *testing.T) {
	fixture := newWorktreeFixture(t)

	for _, name := range []string{"missing", "wt-missing"} {
		out, errOut, status := runWorktreeCommand(t, fixture.context, "remove", name)
		if status != 3 {
			t.Fatalf("removing %q = %d %q, want the documented status 3", name, status, errOut)
		}
		if out != "" {
			t.Errorf("removing %q wrote %q to stdout; a removal that removed nothing must not report success", name, out)
		}
		for _, want := range []string{"no such worktree", "wt-" + name} {
			if !strings.Contains(errOut, want) {
				t.Errorf("removing %q does not name %q:\n%s", name, want, errOut)
			}
		}
	}
}

// TestWorktreeRemoveClearsAHalfRemovedWorktree pins the state a removal still has to finish:
// the checkout is gone but the registration is not. It is not "nothing to remove" — the half
// that survived is exactly what the command is for — and the second call reports nothing left.
func TestWorktreeRemoveClearsAHalfRemovedWorktree(t *testing.T) {
	fixture := newWorktreeFixture(t)
	table := policy.Worktrees()

	if _, errOut, status := runWorktreeCommand(t, fixture.context, "add", "t4"); status != 0 {
		t.Fatalf("add = %d\n%s", status, errOut)
	}
	dir := filepath.Join(fixture.context.parent, table.Prefix+"t4")
	admin := worktree.Admin(fixture.context.common, table.Prefix+"t4")
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("removing the checkout by hand: %v", err)
	}

	out, errOut, status := runWorktreeCommand(t, fixture.context, "remove", "t4")
	if status != 0 {
		t.Fatalf("removing a half-removed worktree = %d %q, want it to clear the registration", status, errOut)
	}
	if !strings.Contains(out, "worktree removed: "+table.Prefix+"t4") {
		t.Errorf("the removal reported %q, want it to name the worktree", out)
	}
	if _, err := os.Stat(admin); !os.IsNotExist(err) {
		t.Errorf("the admin directory survived: %v", err)
	}

	// The second call has nothing left to do, and says so with the status rather than a
	// success line.
	out, _, status = runWorktreeCommand(t, fixture.context, "remove", "t4")
	if status != 3 || strings.Contains(out, "worktree removed") {
		t.Errorf("re-removing = %d %q, want a refusal and no success line", status, out)
	}
}

// TestWorktreeRemoveFollowsGitToAWorktreeOutsideThePolicyParent pins the resolution: remove
// asks git where the name is registered and removes the path git reports. A path computed from
// policy reported `worktree removed`, exit 0, over a worktree that lives elsewhere — it deleted
// the registration and left the directory, with no way back through `git worktree remove`,
// which then refused the surviving path as "not a working tree" (go-cask#461).
func TestWorktreeRemoveFollowsGitToAWorktreeOutsideThePolicyParent(t *testing.T) {
	fixture := newWorktreeFixture(t)
	table := policy.Worktrees()

	elsewhere := filepath.Join(fixture.primary, "elsewhere", table.Prefix+"t5")
	fixture.git("worktree", "add", "--detach", elsewhere, table.Base)
	admin := worktree.Admin(fixture.context.common, table.Prefix+"t5")

	out, errOut, status := runWorktreeCommand(t, fixture.context, "remove", "t5")
	if status != 0 {
		t.Fatalf("removing a worktree outside the policy parent = %d\n%s", status, errOut)
	}
	if !strings.Contains(out, "worktree removed: "+table.Prefix+"t5") {
		t.Errorf("the removal reported %q, want it to name the worktree", out)
	}
	if _, err := os.Stat(elsewhere); !os.IsNotExist(err) {
		t.Errorf("the worktree at %s survived a removal that reported success", elsewhere)
	}
	if _, err := os.Stat(admin); !os.IsNotExist(err) {
		t.Errorf("the registration survived: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fixture.context.parent, table.Prefix+"t5")); !os.IsNotExist(err) {
		t.Errorf("a path git never reported was touched: %v", err)
	}
}

// TestWorktreeRemoveRefusesADirectoryGitDoesNotRegister pins the refusal that replaces the
// accidental removal. The policy path is a guess, and a directory at it that git registers
// nowhere is not a worktree: the verb refuses with its own status and names what it found, so
// the caller removes it deliberately instead of reading a success line about nothing.
func TestWorktreeRemoveRefusesADirectoryGitDoesNotRegister(t *testing.T) {
	fixture := newWorktreeFixture(t)
	table := policy.Worktrees()

	stray := filepath.Join(fixture.context.parent, table.Prefix+"stray")
	if err := os.MkdirAll(stray, 0o755); err != nil {
		t.Fatalf("creating the stray directory: %v", err)
	}

	out, errOut, status := runWorktreeCommand(t, fixture.context, "remove", "stray")
	if status != 3 {
		t.Fatalf("removing an unregistered directory = %d %q, want the documented status 3", status, errOut)
	}
	if out != "" {
		t.Errorf("the refusal wrote %q to stdout", out)
	}
	if !strings.Contains(errOut, "no such worktree") || !strings.Contains(errOut, stray) {
		t.Errorf("the refusal %q does not name the worktree and the directory it found", errOut)
	}
	if _, err := os.Stat(stray); err != nil {
		t.Errorf("the refused removal removed the directory anyway: %v", err)
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
