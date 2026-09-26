package worktree

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestGitFileIsRelative(t *testing.T) {
	t.Parallel()

	primary := filepath.Join(string(filepath.Separator)+"src", "go-cask")
	worktreeDir := filepath.Join(primary, ".gocache", "wt-386")
	adminDir := Admin(filepath.Join(primary, ".git"), "wt-386")

	content, err := GitFile(worktreeDir, adminDir)
	if err != nil {
		t.Fatalf("GitFile: %v", err)
	}
	if content != "gitdir: ../../.git/worktrees/wt-386\n" {
		t.Errorf("GitFile = %q, want the relative form with forward slashes", content)
	}
	// The target carries no drive letter and no leading separator: a path in either
	// toolchain's form is what makes the other toolchain walk up to the primary checkout.
	target := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(content), "gitdir:"))
	if strings.Contains(target, ":") {
		t.Errorf("GitFile = %q, whose target carries a drive", content)
	}
	if strings.HasPrefix(target, "/") {
		t.Errorf("GitFile = %q, whose target is absolute", content)
	}
	if !Resolves(content, worktreeDir, adminDir) {
		t.Errorf("the file it wrote does not resolve to the admin directory: %q", content)
	}
	// A worktree placed somewhere else still gets the right number of steps up.
	shallow := filepath.Join(primary, "wt-x")
	content, err = GitFile(shallow, adminDir)
	if err != nil {
		t.Fatalf("GitFile: %v", err)
	}
	if !Resolves(content, shallow, adminDir) {
		t.Errorf("GitFile(%q) = %q, which does not resolve to %q", shallow, content, adminDir)
	}
}

func TestGitDir(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		content string
		want    string
		found   bool
	}{
		{name: "the relative form", content: "gitdir: ../../.git/worktrees/wt-386\n", want: "../../.git/worktrees/wt-386", found: true},
		{name: "an absolute form", content: "gitdir: /src/go-cask/.git/worktrees/wt-1", want: "/src/go-cask/.git/worktrees/wt-1", found: true},
		{name: "a Windows form", content: "gitdir: D:/src/go-cask/.git/worktrees/wt-1", want: "D:/src/go-cask/.git/worktrees/wt-1", found: true},
		{name: "no link", content: "not a git file\n"},
		{name: "an empty target", content: "gitdir:   \n"},
		{name: "an empty file", content: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, found := GitDir(tc.content)
			if found != tc.found || got != tc.want {
				t.Errorf("GitDir(%q) = %q, %v; want %q, %v", tc.content, got, found, tc.want, tc.found)
			}
		})
	}
}

func TestAdmin(t *testing.T) {
	t.Parallel()

	got := Admin(filepath.Join(string(filepath.Separator)+"src", "go-cask", ".git"), "wt-386")
	want := filepath.Join(string(filepath.Separator)+"src", "go-cask", ".git", "worktrees", "wt-386")
	if got != want {
		t.Errorf("Admin = %q, want %q", got, want)
	}
}

func TestSamePath(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		a    string
		b    string
		want bool
	}{
		{name: "identical", a: "D:/src/go-cask/.git/worktrees/wt-1", b: "D:/src/go-cask/.git/worktrees/wt-1", want: true},
		{name: "separators differ", a: `D:\src\go-cask\.git`, b: "D:/src/go-cask/.git", want: true},
		{name: "a trailing slash", a: "/src/go-cask/.git/", b: "/src/go-cask/.git", want: true},
		{name: "a redundant segment", a: "/src/go-cask/./.git", b: "/src/go-cask/.git", want: true},
		{name: "different worktrees", a: "/src/.git/worktrees/wt-1", b: "/src/.git/worktrees/wt-2"},
		{name: "different toolchains do not compare equal", a: "D:/src/go-cask", b: "/mnt/d/src/go-cask"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := SamePath(tc.a, tc.b); got != tc.want {
				t.Errorf("SamePath(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestResolvesRejectsOtherTrees(t *testing.T) {
	t.Parallel()

	worktreeDir := filepath.Join(string(filepath.Separator)+"src", "go-cask", ".gocache", "wt-386")
	adminDir := Admin(filepath.Join(string(filepath.Separator)+"src", "go-cask", ".git"), "wt-386")

	// The failure this whole package exists to prevent: a link that leads to another
	// worktree, or to the primary checkout.
	if Resolves("gitdir: ../../.git\n", worktreeDir, adminDir) {
		t.Error("a link to the shared git dir was accepted as this worktree's admin dir")
	}
	if Resolves("gitdir: ../../.git/worktrees/wt-1\n", worktreeDir, adminDir) {
		t.Error("a link to another worktree was accepted")
	}
	if Resolves("not a git file\n", worktreeDir, adminDir) {
		t.Error("a file with no gitdir line was accepted")
	}
	if Resolves("gitdir: ../../.git/worktrees/wt-386\n", worktreeDir, adminDir) != true {
		t.Error("the worktree's own admin dir was rejected")
	}
}
