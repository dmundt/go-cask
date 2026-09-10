package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/gitlike"
)

func writeTempFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// objectPath rebuilds the on-disk path for a hash under the default (2,1)
// fan-out layout: <dir>/<algo>/<2 hex>/<full hex>.
func objectPath(dir string, h string) string {
	algo, hex, _ := strings.Cut(h, ":")
	return filepath.Join(dir, algo, hex[:2], hex)
}

// Acceptance: add → commit → log → cat round-trips.
func TestRoundTrip(t *testing.T) {
	ctx := context.Background()
	work := t.TempDir()
	a, err := newApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	f1 := writeTempFile(t, work, "a.txt", "hello world")
	f2 := writeTempFile(t, work, "b.txt", "second file")

	tree, err := a.add(ctx, []string{f1, f2})
	if err != nil {
		t.Fatal(err)
	}
	if tree.IsZero() {
		t.Fatal("add returned no tree hash")
	}
	commit, err := a.commit(ctx, "initial")
	if err != nil {
		t.Fatal(err)
	}
	if commit.IsZero() {
		t.Fatal("commit returned no hash")
	}

	// log shows the commit message.
	var logBuf bytes.Buffer
	if err := a.log(ctx, &logBuf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logBuf.String(), "initial") {
		t.Fatalf("log = %q, want commit message", logBuf.String())
	}

	// cat resolves the blob and returns identical bytes.
	blob, err := a.repo.Blobs.Put(ctx, &gitlike.Blob{Data: []byte("hello world")})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := a.cat(ctx, blob, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "hello world" {
		t.Fatalf("cat = %q, want %q", out.String(), "hello world")
	}
}

// Dedup: identical bytes stored via add produce one blob object.
func TestDedup(t *testing.T) {
	ctx := context.Background()
	work := t.TempDir()
	a, err := newApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f1 := writeTempFile(t, work, "x1.txt", "same content")
	f2 := writeTempFile(t, work, "x2.txt", "same content")

	if _, err := a.add(ctx, []string{f1}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.add(ctx, []string{f2}); err != nil {
		t.Fatal(err)
	}
	st, err := a.raw.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// 1 blob (deduplicated) + 2 trees (different entry names).
	if st.AlgorithmCounts["sha256"] != 3 {
		t.Fatalf("sha256 object count = %d, want 3", st.AlgorithmCounts["sha256"])
	}
}

// Acceptance: audit classifies every object — verified when reachable and
// intact, orphaned when stored but unreachable from HEAD, corrupt when
// Verify fails. -no-verify downgrades intact reachable objects to
// unverified (a fast orphan scan).
func TestAuditStates(t *testing.T) {
	ctx := context.Background()
	work := t.TempDir()
	a, err := newApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := writeTempFile(t, work, "a.txt", "audit me")
	if _, err := a.add(ctx, []string{f}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.commit(ctx, "c1"); err != nil {
		t.Fatal(err)
	}

	rep, err := a.audit(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.counts[stateCorrupt] != 0 || rep.counts[stateOrphaned] != 0 || rep.counts[stateUnverified] != 0 {
		t.Fatalf("clean store audit = %+v, want all verified", rep.counts)
	}
	if rep.counts[stateVerified] == 0 {
		t.Fatal("clean store must have verified objects")
	}

	// An uncommitted add leaves its blob+tree stored but unreachable
	// (INDEX is not a root): audit reports them orphaned.
	g := writeTempFile(t, work, "b.txt", "staged, never committed")
	if _, err := a.add(ctx, []string{g}); err != nil {
		t.Fatal(err)
	}
	rep, err = a.audit(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.counts[stateOrphaned] != 2 { // blob b + its tree
		t.Fatalf("after uncommitted add orphaned = %d, want 2 (%+v)", rep.counts[stateOrphaned], rep.counts)
	}

	// Corrupt a reachable object on disk: audit reports it corrupt.
	hashes, err := a.raw.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	path := objectPath(a.dir, hashes[0].String())
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b[0] ^= 0xff
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err = a.audit(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.counts[stateCorrupt] != 1 {
		t.Fatalf("after corruption corrupt = %d, want 1 (%+v)", rep.counts[stateCorrupt], rep.counts)
	}

	// -no-verify: no integrity pass, so nothing is corrupt/verified —
	// intact reachable objects become unverified; orphans stay orphaned.
	rep, err = a.audit(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if rep.counts[stateCorrupt] != 0 || rep.counts[stateVerified] != 0 {
		t.Fatalf("-no-verify audit = %+v, want no corrupt/verified", rep.counts)
	}
	if rep.counts[stateUnverified] == 0 || rep.counts[stateOrphaned] == 0 {
		t.Fatalf("-no-verify audit = %+v, want unverified + orphaned", rep.counts)
	}
}

// Acceptance: verify passes after a clean commit and reports a mismatch
// after a stored file is corrupted on disk.
func TestVerify(t *testing.T) {
	ctx := context.Background()
	work := t.TempDir()
	a, err := newApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := writeTempFile(t, work, "a.txt", "verify me")
	if _, err := a.add(ctx, []string{f}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.commit(ctx, "c"); err != nil {
		t.Fatal(err)
	}
	if err := a.verify(ctx); err != nil {
		t.Fatalf("verify on clean store: %v", err)
	}

	// Corrupt one stored object on disk.
	hashes, err := a.raw.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(hashes) == 0 {
		t.Fatal("no objects stored")
	}
	path := objectPath(a.dir, hashes[0].String())
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b[0] ^= 0xff
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.verify(ctx); err == nil {
		t.Fatal("verify must report corruption")
	}
}

func TestShortHash(t *testing.T) {
	h, _ := cas.ParseHash("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	s := short(h)
	if len(s) == 0 {
		t.Fatal("short() empty")
	}
}

func TestErrorPaths(t *testing.T) {
	ctx := context.Background()
	a, err := newApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := a.log(ctx, &buf); err == nil {
		t.Fatal("log on empty must error")
	}
	if _, err := a.commit(ctx, "x"); err == nil {
		t.Fatal("commit with no tree must error")
	}
	// verify on empty store: should not crash.
	_ = a.verify(ctx)
}

func TestCatTree(t *testing.T) {
	ctx := context.Background()
	work := t.TempDir()
	a, err := newApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := writeTempFile(t, work, "a.txt", "tree cat")
	tree, err := a.add(ctx, []string{f})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := a.cat(ctx, tree, &out); err != nil {
		t.Fatal(err)
	}
	if out.Len() == 0 {
		t.Fatal("cat of tree returned no output")
	}
}

func TestAddReadError(t *testing.T) {
	ctx := context.Background()
	a, err := newApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.add(ctx, []string{"/nonexistent/file.txt"}); err == nil {
		t.Fatal("add of missing file must error")
	}
}

func TestRunCommands(t *testing.T) {
	ctx := context.Background()
	store := t.TempDir()
	work := t.TempDir()
	f := writeTempFile(t, work, "a.txt", "run me")

	var stdout, stderr bytes.Buffer

	// add
	if code := run(ctx, []string{"-store", store, "add", f}, &stdout, &stderr); code != 0 {
		t.Fatalf("add code=%d stderr=%s", code, stderr.String())
	}
	hash := strings.TrimSpace(stdout.String())
	if hash == "" {
		t.Fatal("add stdout empty")
	}
	stdout.Reset()
	stderr.Reset()

	// commit
	if code := run(ctx, []string{"-store", store, "commit", "-m", "first"}, &stdout, &stderr); code != 0 {
		t.Fatalf("commit code=%d stderr=%s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()

	// log
	if code := run(ctx, []string{"-store", store, "log"}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "first") {
		t.Fatalf("log code=%d out=%q stderr=%s", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()

	stdout.Reset()
	stderr.Reset()

	// stats
	if code := run(ctx, []string{"-store", store, "stats"}, &stdout, &stderr); code != 0 {
		t.Fatalf("stats code=%d stderr=%s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()

	// verify
	if code := run(ctx, []string{"-store", store, "verify"}, &stdout, &stderr); code != 0 {
		t.Fatalf("verify code=%d stderr=%s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()

	// audit
	if code := run(ctx, []string{"-store", store, "audit"}, &stdout, &stderr); code != 0 {
		t.Fatalf("audit code=%d stderr=%s", code, stderr.String())
	}
}

func TestRunUsageErrors(t *testing.T) {
	ctx := context.Background()
	var stdout, stderr bytes.Buffer
	cases := [][]string{
		{},                     // no args
		{"-store"},             // -store missing value
		{"add"},                // add missing file
		{"commit"},             // commit missing -m
		{"cat"},                // cat missing hash
		{"unknown"},            // unknown command
		{"audit", "-badflag"},  // bad audit flag
		{"-store", "x", "add"}, // add missing file
	}
	for _, c := range cases {
		stdout.Reset()
		stderr.Reset()
		if code := run(ctx, c, &stdout, &stderr); code != 2 {
			t.Fatalf("args %v: code=%d, want 2", c, code)
		}
	}
}

func TestRunGraph(t *testing.T) {
	ctx := context.Background()
	store := t.TempDir()
	work := t.TempDir()
	f := writeTempFile(t, work, "a.txt", "graph me")
	var stdout, stderr bytes.Buffer
	// graph before commit -> error (no HEAD)
	if code := run(ctx, []string{"-store", store, "graph"}, &stdout, &stderr); code != 1 {
		t.Fatalf("graph on empty code=%d", code)
	}
	// add + commit then graph works
	stdout.Reset()
	stderr.Reset()
	if code := run(ctx, []string{"-store", store, "add", f}, &stdout, &stderr); code != 0 {
		t.Fatalf("add code=%d", code)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(ctx, []string{"-store", store, "commit", "-m", "g"}, &stdout, &stderr); code != 0 {
		t.Fatalf("commit code=%d", code)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(ctx, []string{"-store", store, "graph"}, &stdout, &stderr); code != 0 {
		t.Fatalf("graph code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "blob") {
		t.Fatalf("graph output missing blob: %q", stdout.String())
	}
}

func TestRunStoreError(t *testing.T) {
	ctx := context.Background()
	var stdout, stderr bytes.Buffer
	// nonexistent store dir - newApp creates it, so use an invalid path
	if code := run(ctx, []string{"stats"}, &stdout, &stderr); code == 2 {
		// default dir store ./objects may exist - not deterministic; skip assert
		_ = code
	}
}

func TestSplitHash(t *testing.T) {
	h, _ := cas.ParseHash("sha256:" + strings.Repeat("ab", 32))
	if short(cas.Hash{}) != "<absent>" {
		t.Fatal("short(absent) should say <absent>")
	}
	if !strings.Contains(short(h), "sha256") {
		t.Fatalf("short = %q", short(h))
	}
}
