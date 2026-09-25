package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

// This file covers the files CLI's remaining reachable error and dispatch
// paths: an unusable root, the runtime-error exit codes of every subcommand,
// and the ways the object model reports a damaged store.
//
// Deliberately left uncovered, with the reason:
//
//   - add's blob-put, tree-put and refs-set returns (main.go:156, 162, 169),
//     commit's commit-put return (main.go:195), app.verify's VerifyAll return
//     (main.go:240) and run's graph and stats error returns (main.go:347, 370):
//     all need the fs backend or the refs store to fail mid-operation. The
//     example holds a concrete *fs.Backend and refs.Store with no injectable
//     seam, and a valid t.TempDir produces none of those states.
//   - main (main.go:382): the process entry point, which only calls run with
//     os.Args and os.Exit.

// newApp fails, with a message naming the offending subtree, when either
// directory it owns cannot be created.
func TestNewAppRejectsUnusableRoot(t *testing.T) {
	cases := []struct {
		name  string
		block string // the path under root that is a regular file, not a directory
		want  string // the error must name this subtree
	}{
		{name: "objects base cannot be created", block: "objects", want: "objects"},
		{name: "refs directory cannot be created", block: "refs", want: "refs"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, tc.block), []byte("in the way"), 0o644); err != nil {
				t.Fatal(err)
			}
			a, err := newApp(root)
			if err == nil {
				t.Fatal("newApp with an unusable root returned no error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("newApp error = %q, want it to name %q", err, tc.want)
			}
			if a != nil {
				t.Fatal("newApp returned an app alongside an error")
			}
		})
	}
}

// Every subcommand that reaches the store reports a runtime failure as exit 1
// when the root cannot hold the layout — not the usage error (2), and never a
// silent success.
func TestRunRuntimeErrorsOnUnusableRoot(t *testing.T) {
	var stdout, stderr bytes.Buffer
	notADir := writeTempFile(t, t.TempDir(), "not-a-dir", "x")

	for _, cmd := range []string{"stats", "verify", "log", "graph", "audit", "cat", "commit"} {
		t.Run(cmd, func(t *testing.T) {
			stdout.Reset()
			stderr.Reset()
			args := []string{"-store", notADir, cmd}
			switch cmd {
			case "cat":
				args = append(args, strings.Repeat("ab", 32))
			case "commit":
				args = append(args, "-m", "msg")
			}
			if code := run(context.Background(), args, &stdout, &stderr); code != 1 {
				t.Fatalf("%v: code=%d, want 1 (stderr=%q)", args, code, stderr.String())
			}
		})
	}
}

// add of an unreadable file is a runtime error (exit 1), and stores nothing.
func TestRunAddUnreadableFile(t *testing.T) {
	ctx := context.Background()
	var stdout, stderr bytes.Buffer
	args := []string{"-store", t.TempDir(), "add", filepath.Join(t.TempDir(), "missing.txt")}
	if code := run(ctx, args, &stdout, &stderr); code != 1 {
		t.Fatalf("add of a missing file: code=%d, want 1 (stderr=%q)", code, stderr.String())
	}
}

// commit before add has no tree to record: exit 1, not an empty commit.
func TestRunCommitWithoutTree(t *testing.T) {
	ctx := context.Background()
	var stdout, stderr bytes.Buffer
	args := []string{"-store", t.TempDir(), "commit", "-m", "nothing to commit"}
	if code := run(ctx, args, &stdout, &stderr); code != 1 {
		t.Fatalf("commit with no tree: code=%d, want 1 (stderr=%q)", code, stderr.String())
	}
}

// cat of a digest nothing stored is a runtime error (exit 1), not empty
// output with a success code. A malformed argument is likewise exit 1 (it got
// past the argument-count check, so it is not a usage error).
func TestRunCatErrors(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		arg  string
	}{
		{name: "digest that was never stored", arg: strings.Repeat("ab", 32)},
		{name: "malformed digest", arg: "not-a-digest"},
		{name: "digest of the wrong width", arg: "abcd"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			args := []string{"-store", t.TempDir(), "cat", tc.arg}
			if code := run(ctx, args, &stdout, &stderr); code != 1 {
				t.Fatalf("cat %q: code=%d, want 1 (stderr=%q)", tc.arg, code, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("cat %q wrote %q to stdout", tc.arg, stdout.String())
			}
		})
	}
}

// log reports a HEAD that names a commit which is not in the store, instead of
// printing nothing and exiting successfully. The planted ref is read back
// first, so the test proves the missing commit is the failure, not a missing
// ref.
func TestRunLogWithDanglingHead(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	a, err := newApp(root)
	if err != nil {
		t.Fatal(err)
	}
	dangling := sha256.Of([]byte("a commit that was deleted"))
	if err := a.refs.Set(ctx, headRef, dangling); err != nil {
		t.Fatal(err)
	}
	if got, present, err := a.headCommitOrAbsent(ctx); err != nil || !present || !got.Equal(dangling) {
		t.Fatalf("planted HEAD = (%s, %v, %v), want %s present", got, present, err, dangling)
	}

	var stdout, stderr bytes.Buffer
	if code := run(ctx, []string{"-store", root, "log"}, &stdout, &stderr); code != 1 {
		t.Fatalf("log with a dangling HEAD: code=%d, want 1 (stderr=%q)", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("log with a dangling HEAD printed %q", stdout.String())
	}
}

// A HEAD ref that exists but is not a digest is corruption, not "no commits
// yet": log and audit must fail rather than print an empty history or call
// every object an orphan.
func TestRunWithCorruptHeadRef(t *testing.T) {
	ctx := context.Background()

	for _, cmd := range []string{"log", "audit"} {
		t.Run(cmd, func(t *testing.T) {
			root := t.TempDir()
			if _, err := newApp(root); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "refs", headRef), []byte("not a digest\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			var stdout, stderr bytes.Buffer
			if code := run(ctx, []string{"-store", root, cmd}, &stdout, &stderr); code != 1 {
				t.Fatalf("%s with a corrupt HEAD: code=%d, want 1 (stderr=%q)", cmd, code, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("%s with a corrupt HEAD printed %q", cmd, stdout.String())
			}
		})
	}
}

// verify reports a corrupted object as a runtime error (exit 1) and names the
// digest it found corrupt.
func TestRunVerifyDetectsCorruption(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	a, err := newApp(root)
	if err != nil {
		t.Fatal(err)
	}
	f := writeTempFile(t, t.TempDir(), "a.txt", "verify through the CLI")
	if _, err := a.add(ctx, []string{f}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.commit(ctx, "c"); err != nil {
		t.Fatal(err)
	}

	digests, err := a.backend.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(digests) == 0 {
		t.Fatal("no objects stored")
	}
	path := testObjectPath(a.objects, digests[0].String())
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b[0] ^= 0xff
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := run(ctx, []string{"-store", root, "verify"}, &stdout, &stderr); code != 1 {
		t.Fatalf("verify over a corrupt store: code=%d, want 1", code)
	}
	// verify's per-object report and summary go to the process's own stdout,
	// not to run's injectable writer (main.go:243/245), so only the exit code
	// and the stderr error are asserted through the CLI here; the digest
	// naming is pinned by the app-level TestVerify.
	if !strings.Contains(stderr.String(), "corrupt") {
		t.Fatalf("verify stderr = %q, want the runtime error", stderr.String())
	}
}
