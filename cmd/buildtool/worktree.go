package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dmundt/go-cask/internal/build/policy"
	"github.com/dmundt/go-cask/internal/build/worktree"
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
		return worktreeLock(args[1:], out, errOut, context)
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
//
// The path is the one git reports for the registration, never one computed from policy: a
// worktree may live anywhere — another session's tooling, an older base directory — and a
// policy path names a worktree only when the command created it there. Computing the path
// removed the registration of a worktree that lived elsewhere, reported success over a
// directory that survived, and left no way back through `git worktree remove` because the
// registration was gone (go-cask#461).
//
// A destructive verb reports success only on evidence, and this one has two ways to report a
// removal that did not happen. A name git registers nowhere is refused before anything is
// touched, and the filesystem is read back at the resolved path after the removal so a half
// that survived it is named rather than papered over. The guard is not defensive tidiness:
// `os.RemoveAll` returns nil for a path that is not there, so without it an unknown name —
// the `wt-` prefix is the command's to add, so a caller who passes it gets `wt-wt-…` —
// printed `worktree removed` and exited 0 having done nothing (go-cask#395).
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
	admin := worktree.Admin(context.common, gitName)

	dir, registered, err := registeredWorktreePath(context.primary, context.common, gitName)
	if err != nil {
		return err
	}
	if !registered {
		// Nothing git registers under this name names no worktree, so there is no path that
		// is this verb's to remove. `os.RemoveAll` would report success over the policy
		// path anyway, and that is how a worktree living elsewhere was orphaned. A
		// directory that happens to sit at the policy path is named so it can be removed
		// deliberately rather than by accident.
		message := fmt.Sprintf("worktree: no such worktree: %s (git registers no worktree by that name)", gitName)
		if stray := filepath.Join(context.parent, gitName); worktreeDirExists(stray) {
			message += fmt.Sprintf("; an unregistered directory is at %s", stray)
		}
		return statusError{code: 3, message: message}
	}

	if worktreeDirExists(dir) && !force {
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
		if removeErr := os.RemoveAll(admin); removeErr != nil {
			return fmt.Errorf("removing the admin directory: %w", removeErr)
		}
	}

	var survivors []string
	if _, err := os.Lstat(dir); err == nil {
		survivors = append(survivors, dir)
	}
	if _, err := os.Lstat(admin); err == nil {
		survivors = append(survivors, admin)
	}
	if len(survivors) != 0 {
		return statusError{code: 3, message: fmt.Sprintf(
			"worktree: %s was not removed: %s still present", gitName, strings.Join(survivors, ", "))}
	}
	fmt.Fprintf(out, "worktree removed: %s\n", gitName)
	return nil
}

// registeredWorktreePath returns the path git reports for the worktree registered under a
// name, and whether git registers that name at all.
//
// The listing is the authority on the path — it is what git's own `worktree remove` acts on —
// and the name is matched two ways. The registration's `gitdir` record names the worktree it
// belongs to, which is exact and survives a directory renamed away from the registration's
// own name; the reported path's base name is the match git itself uses when it creates the
// admin directory. The primary checkout is never a candidate: it is listed first, it is not a
// linked worktree, and a name that merely matches its directory must not resolve to the
// repository itself. A path git does not report is never returned, so a stale record cannot
// point the removal at something git does not consider a worktree.
func registeredWorktreePath(primary, commonDir, gitName string) (string, bool, error) {
	listing, err := gitOutputIn(primary, "worktree", "list", "--porcelain")
	if err != nil {
		return "", false, err
	}
	entries := parseWorktrees(listing)

	recorded := ""
	if raw, err := os.ReadFile(filepath.Join(worktree.Admin(commonDir, gitName), "gitdir")); err == nil {
		recorded = strings.TrimSpace(string(raw))
	}
	for _, entry := range entries {
		if worktree.SamePath(entry.Path, primary) {
			continue
		}
		if recorded != "" && worktree.SamePath(filepath.Join(entry.Path, ".git"), recorded) {
			return entry.Path, true, nil
		}
		if filepath.Base(filepath.Clean(entry.Path)) == gitName {
			return entry.Path, true, nil
		}
	}
	return "", false, nil
}

// worktreeDirExists reports whether a path names a directory that is there, which is the
// question the removal guards ask before and after it touches the worktree.
func worktreeDirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// worktreeLock locks one, several, or every registered worktree. Git skips a locked worktree
// in `prune`, and that is the whole protection: a registration created by plain
// `git worktree add` has no lock, and the other toolchain cannot resolve its reverse link.
//
// A registration is locked where git says it lives, never at a path computed from policy: a
// worktree may live anywhere — an experiment tree, another session's tooling, an older base
// directory — and the admin directory is the one that carries its `gitdir` record, whatever
// its name. Deriving `<prefix><name>` under the shared git dir turned a worktree at
// `D:/wt366-check-c2b` into `wt-wt366-check-c2b`, whose `os.Stat` failed, and the whole gate
// aborted before its first step over a tree with nothing wrong in it (go-cask#508; the same
// defect class go-cask#461 fixed for `worktree remove`).
//
// The gate's job is to lock what exists, so a registration whose admin directory is
// genuinely gone is reported and skipped rather than made fatal: it protects nothing, and a
// lane cannot act on a name it cannot see the reason for.
//
// With --quiet it reports only the worktrees it had to lock, which is the shape a gate step
// wants: a run that changed nothing says nothing.
func worktreeLock(args []string, out, errOut io.Writer, context *worktreeContext) error {
	table := policy.Worktrees()
	quiet := false
	var args2 []string
	for _, arg := range args {
		if arg == "--quiet" {
			quiet = true
			continue
		}
		args2 = append(args2, arg)
	}

	// The caller names a worktree the way `add` does — the bare name, with the policy prefix
	// optional and cancelled rather than doubled, because a directory git registers as
	// `wt366-check-c2b` must not become `wt-wt366-check-c2b` (go-cask#508).
	names := make([]string, 0, len(args2))
	for _, name := range args2 {
		names = append(names, table.Prefix+strings.TrimPrefix(name, table.Prefix))
	}

	// Every registration git reports, so a named worktree resolves the same way an unnamed
	// sweep of them does. The listing is read once and is the authority on where a name is.
	listing, err := gitOutputIn(context.primary, "worktree", "list", "--porcelain")
	if err != nil {
		return err
	}
	entries := parseWorktrees(listing)

	// A sweep takes the names git gives the directories verbatim: they are what the
	// registrations are called, and prefixing them again is exactly the defect above.
	if len(names) == 0 {
		names = worktreeNames(entries, context.primary)
	}
	if len(names) == 0 {
		if !quiet {
			fmt.Fprintln(out, "worktree: no linked worktrees to lock")
		}
		return nil
	}

	for _, gitName := range names {
		entry, found := findWorktree(entries, context.primary, gitName)
		if !found {
			// A name git registers nowhere names no worktree, and no path computed for it
			// is this verb's to lock: creating `wt-…` under the shared git dir for a tree
			// that lives elsewhere is how a gate run invented a worktree it could not lock
			// and called it a failure (go-cask#508).
			fmt.Fprintf(errOut, "worktree: no such worktree: %s (git registers no worktree by that name)\n", gitName)
			continue
		}
		admin, ok := resolveWorktreeAdmin(context.common, entry)
		if !ok {
			// The registration is real and its admin directory is not: there is no lock
			// file to write, and the registration protects nothing. A gate run reports it
			// and locks the rest, because nothing is wrong with the tree it was asked to
			// verify.
			fmt.Fprintf(errOut, "worktree: %s is registered at %s but its admin directory is gone; nothing to lock\n",
				gitName, entry.Path)
			continue
		}
		if _, err := os.Stat(filepath.Join(admin, table.LockFile)); err == nil {
			if !quiet {
				fmt.Fprintf(out, "already locked: %s\n", gitName)
			}
			continue
		}
		if err := os.WriteFile(filepath.Join(admin, table.LockFile), []byte(table.LockMessage), 0o644); err != nil {
			return fmt.Errorf("locking %s: %w", gitName, err)
		}
		// A lock this run added is always reported, quiet or not: it is the reason the
		// caller ran the command.
		fmt.Fprintf(out, "locked: %s — a stray 'git worktree prune' would have deleted it\n", gitName)
	}
	return nil
}

// resolveWorktreeAdmin returns the admin directory of a registration: the one its own
// `gitdir` record names, or the one git's naming gives it — `<common>/worktrees/<basename>`
// — when that record cannot be read.
//
// git's naming is a fallback rather than the rule because the two can disagree: a directory
// renamed away from its registration keeps the record and loses the base name, while an
// experiment worktree that policy naming never described has the base name and no
// `<prefix>` at all. Reading the record first is what makes a worktree at `D:/wt366-check-c2b`
// lockable, and it is the same resolution `registeredWorktreePath` makes for a removal.
func resolveWorktreeAdmin(commonDir string, entry worktreeEntry) (string, bool) {
	if raw, err := os.ReadFile(filepath.Join(entry.Path, ".git")); err == nil {
		if target, ok := worktree.GitDir(string(raw)); ok {
			if !filepath.IsAbs(target) {
				target = filepath.Join(entry.Path, filepath.FromSlash(target))
			}
			if worktreeDirExists(target) {
				return target, true
			}
		}
	}
	if name := filepath.Base(filepath.Clean(entry.Path)); name != "" && name != "." {
		if admin := worktree.Admin(commonDir, name); worktreeDirExists(admin) {
			return admin, true
		}
	}
	return "", false
}

// worktreeNames lists the registered worktrees by the name the repository's convention gives
// them: the directory's base name, which is what git itself uses for the admin directory and
// what the `lock` and `remove` verbs take. The primary checkout is never one of them.
func worktreeNames(entries []worktreeEntry, primary string) []string {
	var names []string
	for _, entry := range entries {
		if worktree.SamePath(entry.Path, primary) {
			continue
		}
		if base := filepath.Base(filepath.Clean(entry.Path)); base != "" && base != "." {
			names = append(names, base)
		}
	}
	sort.Strings(names)
	return names
}

// findWorktree returns the registration a name names, and whether git reports one at all.
//
// A name is the worktree directory's base name — the same name git itself uses for the admin
// directory, and the one the `lock` and `remove` verbs take. The primary checkout is never a
// candidate: it is listed first, it is not a linked worktree, and a name that merely matches
// its directory must not resolve to the repository itself.
func findWorktree(entries []worktreeEntry, primary, name string) (worktreeEntry, bool) {
	for _, entry := range entries {
		if worktree.SamePath(entry.Path, primary) {
			continue
		}
		if filepath.Base(filepath.Clean(entry.Path)) == name {
			return entry, true
		}
	}
	return worktreeEntry{}, false
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
//
// The admin directory is created when it is missing, which is the `add` path's: the
// registration exists but nothing has written its admin directory yet. The `lock` verb never
// takes this path for a registration whose admin directory is gone — it reports that and
// leaves the registry alone rather than inventing a directory for a tree it cannot lock
// (go-cask#508).
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
