package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dmundt/go-cask/internal/build/core/worktree"
	"github.com/dmundt/go-cask/internal/build/policy"
)

// worktreeContext is a resolved view of the repository the command acts on: where the
// primary checkout is, where its shared git directory is, and where a task worktree lands.
//
// It is resolved from Git for the command and built directly by a test, so the rules the
// command enforces — the relative `.git` link, the lock, the resolution check — are provable
// without a repository, and the fixture test below drives a real one for the parts that are
// git's own behaviour.
type worktreeContext struct {
	// primary is the primary checkout's root: the common git directory's parent.
	primary string
	// common is the shared git directory, where every worktree's admin directory lives.
	common string
	// parent is where the linked worktrees are created, inside the primary checkout.
	parent string
}

// resolveWorktrees reads the repository's layout from Git, so the command behaves the same
// however it is invoked — including from inside a task worktree, where the next task must
// still land in the primary checkout.
func resolveWorktrees() (*worktreeContext, error) {
	table := policy.Worktrees()
	common, err := gitPathIn("", "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, err
	}
	primary := filepath.Dir(filepath.Clean(common))
	return &worktreeContext{
		primary: primary,
		common:  common,
		parent:  filepath.Join(primary, table.Parent),
	}, nil
}

// runWorktree creates, locks, lists and removes the repository's task worktrees.
//
// A worktree created here is usable from BOTH toolchains this repository is worked in, which
// is the whole reason the command exists: the `.git` file is written in the relative form and
// the registration is locked, so neither toolchain's `git worktree prune` can delete a live
// worktree, and neither gets silently redirected to the primary checkout.
func runWorktree(args []string, out, errOut io.Writer) error {
	if len(args) == 0 {
		return usageError{worktreeUsage}
	}
	context, err := resolveWorktrees()
	if err != nil {
		return err
	}
	return worktreeCommand(args, out, errOut, context)
}

// worktreeUsage is the command's own help, which is also its usage error.
const worktreeUsage = "usage: buildtool worktree [add <name> [<branch>] | remove <name> [--force] | " +
	"lock [<name>...] | prune | list]"

// worktreeCommand is the command with its context injected.
func worktreeCommand(args []string, out, errOut io.Writer, context *worktreeContext) error {
	if len(args) == 0 {
		return usageError{worktreeUsage}
	}
	switch args[0] {
	case "add":
		return worktreeAdd(args[1:], out, errOut, context)
	case "remove":
		return worktreeRemove(args[1:], out, errOut, context)
	case "lock":
		return worktreeLock(args[1:], out, context)
	case "list":
		return worktreeList(args[1:], out, errOut, context)
	case "prune":
		// The refusal is the whole answer, so it goes where a refused command's report
		// belongs — stderr — and the status carries the verdict: 2, the code the wrapper
		// this replaced used for it.
		fmt.Fprint(errOut, policy.Worktrees().PruneRefusal)
		return exitStatus(2)
	case "-h", "--help", "help":
		fmt.Fprintln(out, worktreeUsage)
		return nil
	default:
		return usageError{worktreeUsage}
	}
}

// worktreeAdd creates a task worktree from the freshly fetched base and makes it usable from
// both toolchains: the `.git` link is rewritten in the relative form, the registration is
// locked, and git's own answer is checked against the admin directory the link must name.
func worktreeAdd(args []string, out, errOut io.Writer, context *worktreeContext) error {
	table := policy.Worktrees()
	if len(args) == 0 {
		return usageError{"usage: buildtool worktree add <name> [<branch>]"}
	}
	if len(args) > 2 {
		return usageError{fmt.Sprintf("unexpected extra argument: %s", args[2])}
	}
	name, branch := args[0], ""
	if len(args) > 1 {
		branch = args[1]
	}
	gitName := table.Prefix + name
	dir := filepath.Join(context.parent, gitName)

	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("worktree: %s already exists", dir)
	}
	if err := os.MkdirAll(context.parent, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", context.parent, err)
	}

	// A failed fetch (a toolchain without a usable SSL backend, for one) would silently base
	// the new worktree on a stale base — say so instead of pretending it is current.
	if _, err := gitOutputIn(context.primary, "fetch", "--quiet", "--all"); err != nil {
		fmt.Fprintf(errOut, "worktree: 'git fetch' failed — using the local %s, which may be stale\n", table.Base)
	}
	base := "unknown"
	if short, err := gitOutputIn(context.primary, "rev-parse", "--short", table.Base); err == nil {
		base = strings.TrimSpace(short)
	}

	add := []string{"worktree", "add", dir}
	if branch != "" {
		add = append(add, "-b", branch)
	} else {
		add = append(add, "--detach")
	}
	add = append(add, table.Base)
	if _, err := gitOutputIn(context.primary, add...); err != nil {
		return err
	}

	admin := worktree.Admin(context.common, gitName)
	file, err := worktree.GitFile(dir, admin)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte(file), 0o644); err != nil {
		return fmt.Errorf("writing the worktree's .git link: %w", err)
	}
	if err := lockWorktree(context.common, gitName, table); err != nil {
		return err
	}

	// Prove it: git inside the worktree must resolve to the worktree's own git directory,
	// never to the primary checkout's. Both paths come from the same git, so the comparison
	// holds whatever path form that toolchain prints.
	resolved, err := gitPathIn(dir, "rev-parse", "--path-format=absolute", "--git-dir")
	if err != nil {
		return err
	}
	if !worktree.SamePath(resolved, admin) {
		return statusError{code: 2, message: fmt.Sprintf(
			"worktree: %s resolves to %q, expected %q — refusing to leave a broken worktree", dir, resolved, admin)}
	}

	revision := branch
	if revision == "" {
		revision = "detached"
	}
	fmt.Fprintf(out, "worktree ready: %s (%s) on %s — .git normalized and locked\n", dir, revision, base)
	return nil
}

// worktreeRemove removes exactly one worktree. A worktree with uncommitted changes is
// refused unless the caller says to discard them, and the removal never falls back to
// `git worktree prune`: that is repository-wide and would delete the worktrees whose reverse
// link holds the other toolchain's path form.
func worktreeRemove(args []string, out, errOut io.Writer, context *worktreeContext) error {
	table := policy.Worktrees()
	if len(args) == 0 {
		return usageError{"usage: buildtool worktree remove <name> [--force]"}
	}
	name := args[0]
	force := len(args) > 1 && args[1] == "--force"
	if len(args) > 2 || (len(args) > 1 && !force) {
		return usageError{"usage: buildtool worktree remove <name> [--force]"}
	}
	gitName := table.Prefix + name
	dir := filepath.Join(context.parent, gitName)

	if _, err := os.Stat(dir); err == nil && !force {
		status, err := gitOutputIn(dir, "status", "--porcelain")
		if err == nil && strings.TrimSpace(status) != "" {
			return statusError{code: 3, message: fmt.Sprintf(
				"worktree: %s has uncommitted changes — commit, or pass --force to discard", dir)}
		}
	}

	// `--force --force` also unlocks. Reading the admin gitdir back only works from the
	// creating toolchain, so a failure removes both halves directly.
	if _, err := gitOutputIn(context.primary, "worktree", "remove", "--force", "--force", dir); err != nil {
		if removeErr := os.RemoveAll(dir); removeErr != nil {
			return fmt.Errorf("removing %s: %w", dir, removeErr)
		}
		if removeErr := os.RemoveAll(worktree.Admin(context.common, gitName)); removeErr != nil {
			return fmt.Errorf("removing the admin directory: %w", removeErr)
		}
	}
	fmt.Fprintf(out, "worktree removed: %s\n", gitName)
	return nil
}

// worktreeLock locks one, several, or every registered worktree. Git skips a locked worktree
// in `prune`, and that is the whole protection: a registration created by plain
// `git worktree add` has no lock, and the other toolchain cannot resolve its reverse link.
//
// With --quiet it reports only the worktrees it had to lock, which is the shape a gate step
// wants: a run that changed nothing says nothing.
func worktreeLock(args []string, out io.Writer, context *worktreeContext) error {
	table := policy.Worktrees()
	quiet := false
	var names []string
	for _, arg := range args {
		if arg == "--quiet" {
			quiet = true
			continue
		}
		names = append(names, arg)
	}
	if len(names) == 0 {
		registered, err := registeredWorktrees(context.common)
		if err != nil {
			return err
		}
		names = registered
	}
	if len(names) == 0 {
		if !quiet {
			fmt.Fprintln(out, "worktree: no linked worktrees to lock")
		}
		return nil
	}

	var failed []string
	for _, name := range names {
		gitName := name
		if !strings.HasPrefix(gitName, table.Prefix) {
			gitName = table.Prefix + gitName
		}
		admin := worktree.Admin(context.common, gitName)
		if info, err := os.Stat(admin); err != nil || !info.IsDir() {
			failed = append(failed, "worktree: no such worktree: "+gitName)
			continue
		}
		if _, err := os.Stat(filepath.Join(admin, table.LockFile)); err == nil {
			if !quiet {
				fmt.Fprintf(out, "already locked: %s\n", gitName)
			}
			continue
		}
		if err := lockWorktree(context.common, gitName, table); err != nil {
			return err
		}
		// A lock this run added is always reported, quiet or not: it is the reason the
		// caller ran the command.
		fmt.Fprintf(out, "locked: %s — a stray 'git worktree prune' would have deleted it\n", gitName)
	}
	if len(failed) != 0 {
		return errors.New(strings.Join(failed, "\n"))
	}
	return nil
}

// worktreeList reports what git has registered.
func worktreeList(args []string, out, errOut io.Writer, context *worktreeContext) error {
	if len(args) != 0 {
		return usageError{fmt.Sprintf("unexpected argument %q", args[0])}
	}
	listing, err := gitOutputIn(context.primary, "worktree", "list")
	if err != nil {
		return err
	}
	fmt.Fprint(out, listing)
	return nil
}

// lockWorktree writes the lock file into a worktree's admin directory, unless it is there
// already. It is written directly rather than through `git worktree lock`, which itself has to
// read the admin `gitdir` back and so only works from the creating toolchain.
func lockWorktree(commonDir, gitName string, table policy.WorktreeTable) error {
	admin := worktree.Admin(commonDir, gitName)
	if err := os.MkdirAll(admin, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", admin, err)
	}
	if err := os.WriteFile(filepath.Join(admin, table.LockFile), []byte(table.LockMessage), 0o644); err != nil {
		return fmt.Errorf("locking %s: %w", gitName, err)
	}
	return nil
}

// registeredWorktrees returns the names of the linked worktrees git has an admin directory
// for, in a stable order so a report reads the same twice.
func registeredWorktrees(commonDir string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(commonDir, "worktrees"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading the worktree registrations: %w", err)
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}
