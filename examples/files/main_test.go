package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/hash/sha256"
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

// testObjectPath rebuilds the on-disk path for a digest under the default (2,1)
// fan-out layout: <objects>/<2 hex>/<full hex>. There is no algorithm
// directory: the backend does not know the client's hash algorithm.
//
// The example itself no longer derives this path — that was the deleted
// objectPath helper, which broke under a non-default fan-out. A test may: it
// has to corrupt one specific file to prove Verify still catches bit rot.
func testObjectPath(objects string, h string) string {
	return filepath.Join(objects, h[:2], h)
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
		t.Fatal("add returned no tree digest")
	}
	commit, err := a.commit(ctx, "initial")
	if err != nil {
		t.Fatal(err)
	}
	if commit.IsZero() {
		t.Fatal("commit returned no digest")
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
	st, err := a.backend.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// 1 blob (deduplicated) + 2 trees (different entry names). Stats has no
	// per-algorithm breakdown: the core does not know the algorithm.
	if st.ObjectCount != 3 {
		t.Fatalf("object count = %d, want 3", st.ObjectCount)
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
	digests, err := a.backend.List(ctx)
	if err != nil {
		t.Fatal(err)
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
	if err := a.verify(ctx); err == nil {
		t.Fatal("verify must report corruption")
	}
}

// TestIntactObjectVerifiesWithoutSidecar pins the contract the deleted CRC32
// sidecar broke: an object is verified from its own stored bytes alone, with
// nothing persisted beside it. The old check returned "missing crc32 sidecar"
// for exactly this intact object, so audit labelled an object written by
// anything but this example (cask put, a snapshot import) as corrupt — and the
// sidecar files themselves were invisible to List yet unreclaimable by Clean.
func TestIntactObjectVerifiesWithoutSidecar(t *testing.T) {
	ctx := context.Background()
	a, err := newApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := writeTempFile(t, t.TempDir(), "seed.txt", "no sidecar needed")
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
	for _, d := range digests {
		if err := cas.Verify(ctx, a.backend, d, a.hasher); err != nil {
			t.Fatalf("intact object %s must verify without a sidecar: %v", d, err)
		}
		if _, err := os.Stat(testObjectPath(a.objects, d.String()) + ".crc32"); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("object %s has a sidecar beside it (%v); integrity is a recompute now", d, err)
		}
	}

	// audit agrees: every listed object is verified, none corrupt.
	rep, err := a.audit(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.counts[stateCorrupt] != 0 {
		t.Fatalf("intact objects without a sidecar must not be corrupt: %+v", rep.counts)
	}
	if rep.counts[stateVerified] != len(digests) {
		t.Fatalf("verified = %d, want all %d objects (%+v)", rep.counts[stateVerified], len(digests), rep.counts)
	}

	// Negative: the recompute still rejects real damage.
	path := testObjectPath(a.objects, digests[0].String())
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b[0] ^= 0xff
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cas.Verify(ctx, a.backend, digests[0], a.hasher); err == nil {
		t.Fatal("Verify must reject a mutated object")
	}
}

// TestLayoutSeparatesRefsFromObjects pins the example root's shape: objects in
// <root>/objects (the fs.Backend base), refs in <root>/refs (cas/refs). The
// split is what keeps a ref's "<name>.tmp" temp file out of the store's Clean
// sweep and keeps refs out of the store's List.
func TestLayoutSeparatesRefsFromObjects(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	a, err := newApp(root)
	if err != nil {
		t.Fatal(err)
	}
	f := writeTempFile(t, t.TempDir(), "a.txt", "layout")
	if _, err := a.add(ctx, []string{f}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.commit(ctx, "c"); err != nil {
		t.Fatal(err)
	}

	for _, sub := range []string{"objects", "refs"} {
		p := filepath.Join(root, sub)
		fi, err := os.Stat(p)
		if err != nil || !fi.IsDir() {
			t.Fatalf("layout: %s is not a directory (err %v)", p, err)
		}
	}

	// The refs hold the bare-hex digest, cas/refs' format — no "sha256:" prefix.
	for _, name := range []string{headRef, indexRef} {
		p := filepath.Join(root, "refs", name)
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("layout: ref %s: %v", p, err)
		}
		if _, err := cas.ParseDigest(strings.TrimSpace(string(b))); err != nil {
			t.Fatalf("ref %s = %q, want a bare hex digest: %v", name, b, err)
		}
		if strings.Contains(string(b), ":") {
			t.Fatalf("ref %s = %q, want bare hex (cas/refs' format)", name, b)
		}
	}

	// The objects base holds objects only: no CRC32 sidecar, no temp leftover.
	var stray []string
	err = filepath.WalkDir(filepath.Join(root, "objects"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".crc32") || strings.HasSuffix(path, ".tmp") {
			stray = append(stray, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(stray) != 0 {
		t.Fatalf("objects base contains non-object files: %v", stray)
	}

	// blob + tree + commit: the two refs are not among the listed objects.
	digests, err := a.backend.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(digests) != 3 {
		t.Fatalf("stored objects = %d, want 3 (blob, tree, commit); refs must not be listed", len(digests))
	}
}

// TestRefsRecordHistory pins what the hand-rolled ref file never had: HEAD is a
// cas/refs ref, so each commit appends a reflog entry and Previous reports the
// commit before the current one.
func TestRefsRecordHistory(t *testing.T) {
	ctx := context.Background()
	work := t.TempDir()
	a, err := newApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first := writeTempFile(t, work, "a.txt", "one")
	if _, err := a.add(ctx, []string{first}); err != nil {
		t.Fatal(err)
	}
	c1, err := a.commit(ctx, "one")
	if err != nil {
		t.Fatal(err)
	}
	second := writeTempFile(t, work, "b.txt", "two")
	if _, err := a.add(ctx, []string{second}); err != nil {
		t.Fatal(err)
	}
	c2, err := a.commit(ctx, "two")
	if err != nil {
		t.Fatal(err)
	}

	prev, err := a.refs.Previous(ctx, headRef)
	if err != nil {
		t.Fatal(err)
	}
	if !prev.Equal(c1) {
		t.Fatalf("Previous(HEAD) = %s, want the first commit %s", prev, c1)
	}
	entries, err := a.refs.Log(ctx, headRef, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("HEAD reflog = %d entries, want 2 (one per commit)", len(entries))
	}
	if !entries[0].Digest.Equal(c2) {
		t.Fatalf("newest reflog entry = %s, want the current commit %s", entries[0].Digest, c2)
	}
}

func TestPrintableDigest(t *testing.T) {
	h, _ := sha256.Parse("sha256:0000000000000000000000000000000000000000000000000000000000000000")
	s := printable(h)
	if len(s) == 0 {
		t.Fatal("printable() empty")
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
	digest := strings.TrimSpace(stdout.String())
	if digest == "" {
		t.Fatal("add stdout empty")
	}
	// `add` prints the digest's bare hex form (cas.Digest.String): no
	// algorithm prefix. The refs store bare hex too (cas/refs' format), but
	// the CLI prints the digest itself.
	if len(digest) != 64 || strings.Contains(digest, ":") {
		t.Fatalf("add printed %q, want a bare 64-hex digest", digest)
	}
	stdout.Reset()
	stderr.Reset()

	// cat accepts the bare hex form and resolves the object (`add` returns
	// the tree digest, which cat renders as the tree's entries).
	if code := run(ctx, []string{"-store", store, "cat", digest}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "tree") {
		t.Fatalf("cat code=%d out=%q stderr=%s", code, stdout.String(), stderr.String())
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
	root := t.TempDir() // every case that reaches newApp points at a temp root
	var stdout, stderr bytes.Buffer
	cases := [][]string{
		{},                      // no args
		{"-store"},              // -store missing value
		{"-store", root, "add"}, // add missing file
		{"-store", root, "commit"},
		{"-store", root, "cat"},
		{"-store", root, "unknown"},
		{"-store", root, "audit", "-badflag"}, // bad audit flag
	}
	for _, c := range cases {
		stdout.Reset()
		stderr.Reset()
		if code := run(ctx, c, &stdout, &stderr); code != 2 {
			t.Fatalf("args %v: code=%d, want 2", c, code)
		}
	}
}

// TestRunStoreFlagForms pins the standard flag parsing: the store directory may
// be spelled -store dir, -store=dir, or --store dir, and -h/--help and an
// unknown flag are usage errors that print the usage text (exit 2).
func TestRunStoreFlagForms(t *testing.T) {
	ctx := context.Background()
	store := t.TempDir()
	var stdout, stderr bytes.Buffer

	for _, args := range [][]string{
		{"-store", store, "stats"},
		{"-store=" + store, "stats"},
		{"--store", store, "stats"},
	} {
		stdout.Reset()
		stderr.Reset()
		if code := run(ctx, args, &stdout, &stderr); code != 0 {
			t.Fatalf("args %v: code=%d stderr=%s", args, code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "objects") {
			t.Fatalf("args %v: stats output = %q", args, stdout.String())
		}
	}

	for _, args := range [][]string{
		{"-h"},
		{"--help"},
		{"-store=" + store, "-nope", "stats"},
	} {
		stdout.Reset()
		stderr.Reset()
		if code := run(ctx, args, &stdout, &stderr); code != 2 {
			t.Fatalf("args %v: code=%d, want 2", args, code)
		}
		if !strings.Contains(stderr.String(), "usage: files") {
			t.Fatalf("args %v: stderr = %q, want the usage text", args, stderr.String())
		}
	}
}

// TestHeadReadErrorIsNotAbsence pins the distinction between "no HEAD yet" and
// "HEAD exists but cannot be read": a corrupt HEAD ref is corruption, so commit
// and audit must report it instead of silently starting a new root commit or
// calling every object orphaned.
func TestHeadReadErrorIsNotAbsence(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	a, err := newApp(root)
	if err != nil {
		t.Fatal(err)
	}
	f := writeTempFile(t, t.TempDir(), "seed.txt", "head corruption")
	if _, err := a.add(ctx, []string{f}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "refs", headRef), []byte("not a digest\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.commit(ctx, "c"); err == nil {
		t.Fatal("commit with a corrupt HEAD must error, not create a parentless commit")
	}
	if _, err := a.audit(ctx, true); err == nil {
		t.Fatal("audit with a corrupt HEAD must error, not report every object orphaned")
	}
	// A missing HEAD is still the first-commit case: absent, not an error.
	empty, err := newApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, present, err := empty.headCommitOrAbsent(ctx); err != nil || present {
		t.Fatalf("headCommitOrAbsent() with no HEAD = present %v, err %v; want absent and no error", present, err)
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

// TestRunStoreError pins the runtime-error exit code: a -store root that cannot
// hold the layout (here a regular file stands where the root directory would
// go, so <root>/objects cannot be created) fails newApp, which is exit 1 — not
// the usage error (2) or the default root, which newApp creates on demand.
func TestRunStoreError(t *testing.T) {
	ctx := context.Background()
	var stdout, stderr bytes.Buffer
	notADir := writeTempFile(t, t.TempDir(), "not-a-dir", "x")
	if code := run(ctx, []string{"-store", notADir, "stats"}, &stdout, &stderr); code != 1 {
		t.Fatalf("stats with an unusable store root: code=%d, want 1 (stderr=%q)", code, stderr.String())
	}
}

// TestPrintableDigestForm pins printable()'s two renderings: the printable
// "sha256:hexdigest" form for a present digest and "<absent>" for the zero one.
func TestShortDigestForm(t *testing.T) {
	h, _ := sha256.Parse("sha256:" + strings.Repeat("ab", 32))
	if printable(nil) != "<absent>" {
		t.Fatal("printable(absent) should say <absent>")
	}
	if !strings.Contains(printable(h), "sha256") {
		t.Fatalf("printable = %q", printable(h))
	}
}
