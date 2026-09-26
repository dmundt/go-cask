// Package worktree owns the rules that make a linked worktree usable from more than one
// toolchain: the relative `.git` link, the admin directory it points at, the lock that keeps
// `git worktree prune` from deleting a live registration, and the text of the refusal that
// stands in for prune.
//
// The rules exist because one checkout here is worked by two toolchains that spell the same
// directory differently. `git worktree add` records an absolute path in the creating
// toolchain's form; the other cannot resolve it, walks up to the enclosing repository, and
// silently operates on the primary checkout — so a gate run inside such a worktree tests the
// wrong tree. The relative form is the fix, and the lock is what protects the registration
// from a prune run by the toolchain that cannot resolve its reverse link.
package worktree

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// GitFile renders a linked worktree's `.git` file: a `gitdir:` line naming the admin
// directory the worktree belongs to, as a path relative to the worktree itself.
//
// The relative form is the point, and the separators are forward slashes, which both
// toolchains read.
func GitFile(worktreeDir, adminDir string) (string, error) {
	rel, err := filepath.Rel(worktreeDir, adminDir)
	if err != nil {
		return "", fmt.Errorf("relating %s to %s: %w", adminDir, worktreeDir, err)
	}
	return "gitdir: " + filepath.ToSlash(rel) + "\n", nil
}

// GitDir reads the admin directory a `.git` file names, exactly as written: a caller that
// needs a path resolves a relative one against the worktree it read the file from.
func GitDir(content string) (string, bool) {
	for _, line := range strings.Split(content, "\n") {
		target, found := strings.CutPrefix(strings.TrimSpace(line), "gitdir:")
		if !found {
			continue
		}
		if target = strings.TrimSpace(target); target != "" {
			return target, true
		}
	}
	return "", false
}

// Admin returns the admin directory git keeps for a linked worktree: `<common>/worktrees/<name>`.
// It is where the lock lives and what the worktree's `.git` file must point at.
func Admin(commonDir, name string) string {
	return filepath.Join(commonDir, "worktrees", name)
}

// Resolves reports whether a worktree's `.git` file leads to the admin directory the caller
// expects. That is the proof `add` makes after writing the file: git inside the worktree
// must resolve to the worktree's own git directory, never to the primary checkout's.
func Resolves(content, worktreeDir, expectedAdmin string) bool {
	target, ok := GitDir(content)
	if !ok {
		return false
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(worktreeDir, filepath.FromSlash(target))
	}
	return SamePath(target, expectedAdmin)
}

// SamePath reports whether two paths name the same place, comparing them as text after
// normalising separators and cleaning them. Both paths must come from one toolchain — the two
// spell a directory `D:/x` and `/mnt/d/x` — so this absorbs a path git printed against one the
// caller joined from its parts, and nothing more.
func SamePath(a, b string) bool {
	normalize := func(value string) string {
		return strings.TrimSuffix(path.Clean(strings.ReplaceAll(value, `\`, "/")), "/")
	}
	return normalize(a) == normalize(b)
}
