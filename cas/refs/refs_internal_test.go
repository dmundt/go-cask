package refs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/dmundt/go-cask/cas"
	"github.com/dmundt/go-cask/cas/hash/sha256"
)

func TestWriteFileAtomicReplacesStaleTmp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main")
	if err := os.WriteFile(path+".tmp", []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(path, []byte("fresh\n")); err != nil {
		t.Fatalf("writeFileAtomic = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "fresh\n" {
		t.Fatalf("writeFileAtomic wrote %q, want %q", string(data), "fresh\n")
	}
}

func TestParseLogIgnoresMalformedTail(t *testing.T) {
	d1 := sha256.Of([]byte("a"))
	d2 := sha256.Of([]byte("b"))
	data := []byte("bad\n" +
		"1\t" + d1.String() + "\t\n\n" +
		"2\t" + d2.String() + "\t" + d1.String() + "\n")
	entries := parseLog(data)
	if len(entries) != 2 {
		t.Fatalf("parseLog = %d entries, want 2", len(entries))
	}
	if !entries[0].Digest.Equal(d1) || !entries[0].Old.IsZero() {
		t.Fatalf("parseLog[0] = %+v, want Digest=%s Old=absent", entries[0], d1)
	}
	if !entries[1].Digest.Equal(d2) || !entries[1].Old.Equal(d1) {
		t.Fatalf("parseLog[1] = %+v, want Digest=%s Old=%s", entries[1], d2, d1)
	}
}

func TestResolveEmptyNameAndRootsSkipZero(t *testing.T) {
	ctx := context.Background()
	s := &Store{dir: t.TempDir(), now: time.Now}
	if _, err := s.Resolve(ctx, ""); err == nil {
		t.Fatal("Resolve(empty) = nil error, want error")
	}
	if err := s.Set(ctx, "a", sha256.Of([]byte("a"))); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(ctx, "b", nil); err == nil {
		t.Fatal("Set(nil digest) = nil error, want error")
	}
	roots, err := s.Roots(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 1 || !roots[0].Equal(sha256.Of([]byte("a"))) {
		t.Fatalf("Roots = %v, want [%s]", roots, sha256.Of([]byte("a")))
	}
}

func TestValidateNameRejectsWindowsReservedNamesAndBackslash(t *testing.T) {
	for _, name := range []string{"con", "CON.txt", "a\\b", "a/b\\c"} {
		if err := ValidateName(name); err == nil {
			t.Fatalf("ValidateName(%q) = nil, want error", name)
		}
	}
	if _, err := cas.ParseDigest(""); err == nil {
		t.Fatal("ParseDigest(empty) = nil error, want error")
	}
}

func TestListMissingDirAndCorruptValueAreHandled(t *testing.T) {
	ctx := context.Background()
	missing := &Store{dir: filepath.Join(t.TempDir(), "missing"), now: time.Now}
	if got, err := missing.List(ctx); err != nil || got != nil {
		t.Fatalf("List(missing dir) = (%v, %v), want (nil, nil)", got, err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "main")
	if err := os.WriteFile(path, []byte("not-a-digest\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := &Store{dir: dir, now: time.Now}
	if _, err := st.readValue("main"); err == nil {
		t.Fatal("readValue(corrupt digest) = nil error, want error")
	}
}

// The tests below cover cas/refs' remaining failure branches. refs is
// core-adjacent — it is what makes a store's "current revision" durable — so it
// is held to the same ≥90% statement gate as the storage packages, and the
// round-trip tests cannot reach these branches. Every case is driven by a
// filesystem state the test creates before the call (a file where a directory
// belongs, a directory where a file belongs, a stale temp path, an unreadable
// file): nothing sleeps, retries, or depends on scheduling, so the coverage
// they add is deterministic. Branches that cannot be produced by any filesystem
// state are listed at the end of the file.

// TestOpenReportsUncreatableDir covers Open's MkdirAll failure: a base path
// whose parent is a regular file cannot be created, and Open must report that
// instead of returning a Store rooted at a directory that does not exist.
func TestOpenReportsUncreatableDir(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(filepath.Join(file, "refs")); err == nil {
		t.Fatal("Open under a regular file = nil error, want error")
	}
}

// TestContextAndNameGuards covers the argument guards every public method
// shares: a canceled context and an invalid name are reported before any
// filesystem work, for the methods whose guards the round-trip tests do not
// reach. The Set case also pins the guard order — the name is validated before
// the digest, so an invalid name is reported even when both arguments are bad.
func TestContextAndNameGuards(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	live := context.Background()
	s := &Store{dir: t.TempDir(), now: time.Now}

	if _, err := s.Get(canceled, "main"); !errors.Is(err, context.Canceled) {
		t.Errorf("Get(canceled ctx) = %v, want context.Canceled", err)
	}
	if err := s.Delete(canceled, "main"); !errors.Is(err, context.Canceled) {
		t.Errorf("Delete(canceled ctx) = %v, want context.Canceled", err)
	}
	if _, err := s.Resolve(canceled, "main"); !errors.Is(err, context.Canceled) {
		t.Errorf("Resolve(canceled ctx) = %v, want context.Canceled", err)
	}
	if _, err := s.Log(canceled, "main", 0); !errors.Is(err, context.Canceled) {
		t.Errorf("Log(canceled ctx) = %v, want context.Canceled", err)
	}

	if _, err := s.Get(live, ".."); !errors.Is(err, ErrInvalidName) {
		t.Errorf("Get(..) = %v, want ErrInvalidName", err)
	}
	if err := s.Delete(live, ".."); !errors.Is(err, ErrInvalidName) {
		t.Errorf("Delete(..) = %v, want ErrInvalidName", err)
	}
	if _, err := s.Log(live, "..", 0); !errors.Is(err, ErrInvalidName) {
		t.Errorf("Log(..) = %v, want ErrInvalidName", err)
	}
	if _, err := s.Previous(live, ".."); !errors.Is(err, ErrInvalidName) {
		t.Errorf("Previous(..) = %v, want ErrInvalidName", err)
	}
	if err := s.Set(live, "..", nil); !errors.Is(err, ErrInvalidName) {
		t.Errorf("Set(.., absent digest) = %v, want ErrInvalidName (the name guard runs first)", err)
	}
}

// TestValuePathFailuresAreReported covers the value-file failures that are not
// a miss: a ref whose stored path is a directory (reading it fails on every
// platform, and never with "not exist"), and a name whose parent component is a
// regular file, so the path cannot be opened at all. Both must be reported
// rather than turned into ErrNotFound — Get's readValue branch, Set's pre-read
// branch and Delete's pre-read branch all rely on that distinction.
func TestValuePathFailuresAreReported(t *testing.T) {
	ctx := context.Background()

	t.Run("directory-valued", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "main"), 0o755); err != nil {
			t.Fatal(err)
		}
		s := &Store{dir: dir, now: time.Now}
		calls := []struct {
			method string
			call   func() error
		}{
			{"Get", func() error { _, err := s.Get(ctx, "main"); return err }},
			{"Set", func() error { return s.Set(ctx, "main", sha256.Of([]byte("v1"))) }},
			{"Delete", func() error { return s.Delete(ctx, "main") }},
		}
		for _, tc := range calls {
			err := tc.call()
			if err == nil {
				t.Errorf("%s over a directory-valued ref = nil error, want error", tc.method)
				continue
			}
			if errors.Is(err, ErrNotFound) {
				t.Errorf("%s over a directory-valued ref = %v; an unreadable ref must not be reported as ErrNotFound", tc.method, err)
			}
		}
	})

	t.Run("file-component", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "a"), []byte("regular file"), 0o644); err != nil {
			t.Fatal(err)
		}
		s := &Store{dir: dir, now: time.Now}
		if err := s.Set(ctx, "a/b", sha256.Of([]byte("v1"))); err == nil {
			t.Error("Set under a regular file = nil error, want error")
		}
	})
}

// TestWriteFileAtomicReportsUncreatableParent covers writeFileAtomic's MkdirAll
// failure: the temp file's directory cannot exist because its parent is a
// regular file, so nothing is written and the failure is reported.
func TestWriteFileAtomicReportsUncreatableParent(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(filepath.Join(file, "ref"), []byte("v\n")); err == nil {
		t.Fatal("writeFileAtomic under a regular file = nil error, want error")
	}
}

// TestWriteFileAtomicReportsUnexpandableTmpPath covers writeFileAtomic's
// non-collision open failure: the temp path exists but is not an entry the
// writer may replace (here a non-empty directory), so the single ErrExist retry
// cannot clear it and the write is reported as failed. Set wraps the same
// failure, and the ref must still be absent afterwards.
func TestWriteFileAtomicReportsUnexpandableTmpPath(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	stale := filepath.Join(dir, "main.tmp")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "occupied"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := writeFileAtomic(filepath.Join(dir, "main"), []byte("v\n")); err == nil {
		t.Error("writeFileAtomic with an unreplaceable temp path = nil error, want error")
	}

	s := &Store{dir: dir, now: time.Now}
	if err := s.Set(ctx, "main", sha256.Of([]byte("v1"))); err == nil {
		t.Error("Set with an unreplaceable temp path = nil error, want error")
	}
	if _, err := s.Get(ctx, "main"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after the failed Set = %v, want ErrNotFound (no value was published)", err)
	}
}

// TestWriteFileAtomicReportsRenameFailure covers writeFileAtomic's final rename:
// a destination that cannot be replaced (a non-empty directory) leaves the
// value unwritten, so the failure is reported and the temp file is cleaned up
// rather than left for List to skip.
func TestWriteFileAtomicReportsRenameFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ref")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "occupied"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(path, []byte("v\n")); err == nil {
		t.Fatal("writeFileAtomic onto a non-empty directory = nil error, want error")
	}
	if _, err := os.Stat(path + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("temp file survived the failed rename: stat = %v", err)
	}
}

// TestSyncParentDirReportsUnopenableParent covers syncParentDir's open failure:
// with no directory to fsync, the write cannot claim durability and must report
// it. syncParentDir is a documented no-op on Windows (a Windows directory
// cannot be opened for Sync), so the assertion is POSIX-only.
func TestSyncParentDirReportsUnopenableParent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("syncParentDir is a no-op on Windows")
	}
	if err := syncParentDir(filepath.Join(t.TempDir(), "missing", "ref")); err == nil {
		t.Fatal("syncParentDir with a missing parent = nil error, want error")
	}
}

// TestSetAndDeleteReportReflogFailures covers appendLog's two reachable
// failures, driven through Set and Delete: the log directory cannot be created
// because ".log" is a regular file (MkdirAll), and the log file cannot be
// opened because ".log/<name>" is a directory (OpenFile). Set writes the value
// before appending its log, so the second case also reaches Delete's
// append branch: the ref was published, but its history could not be recorded
// and the caller is told so.
func TestSetAndDeleteReportReflogFailures(t *testing.T) {
	ctx := context.Background()

	t.Run("log-dir-is-a-file", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, logSubdir), []byte("not a directory"), 0o644); err != nil {
			t.Fatal(err)
		}
		s := &Store{dir: dir, now: time.Now}
		if err := s.Set(ctx, "main", sha256.Of([]byte("v1"))); err == nil {
			t.Error("Set with a regular file where the log directory belongs = nil error, want error")
		}
	})

	t.Run("log-path-is-a-directory", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, logSubdir, "main"), 0o755); err != nil {
			t.Fatal(err)
		}
		s := &Store{dir: dir, now: time.Now}
		d := sha256.Of([]byte("v1"))
		if err := s.Set(ctx, "main", d); err == nil {
			t.Error("Set with a directory where the log file belongs = nil error, want error")
		}
		if got, err := s.Get(ctx, "main"); err != nil || !got.Equal(d) {
			t.Errorf("Get after the failed Set = (%q, %v), want the published value %s", got, err, d)
		}
		if err := s.Delete(ctx, "main"); err == nil {
			t.Error("Delete with a directory where the log file belongs = nil error, want error")
		}
	})
}

// TestLogReportsUnreadableReflogPath covers Log's non-ErrNotExist read failure:
// a reflog path that cannot be read as a file is a real failure, not "this name
// has no history yet" (which is (nil, nil)).
func TestLogReportsUnreadableReflogPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, logSubdir, "main"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Store{dir: dir, now: time.Now}
	if _, err := s.Log(context.Background(), "main", 0); err == nil {
		t.Fatal("Log with a directory where the reflog file belongs = nil error, want error")
	}
}

// TestListSkipsNonDirectoryLogEntry covers List's guard for the reserved log
// name: even when ".log" is a plain file — an entry this package never writes
// itself — List skips it instead of descending into it or reading it as a ref.
func TestListSkipsNonDirectoryLogEntry(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, logSubdir), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Store{dir: dir, now: time.Now}
	all, err := s.List(context.Background())
	if err != nil {
		t.Fatalf("List with a regular file named %q = %v, want it skipped", logSubdir, err)
	}
	if len(all) != 0 {
		t.Fatalf("List = %v, want no refs (the reserved log name is never a ref)", all)
	}
}

// TestParseLogDropsUnparsableOldDigest covers parseLog's third field: a line
// whose Old field is present but is not a digest is dropped like any other
// malformed line, while the entries before and after it survive.
func TestParseLogDropsUnparsableOldDigest(t *testing.T) {
	d := sha256.Of([]byte("a"))
	data := []byte("1\t" + d.String() + "\t\n" +
		"2\t" + d.String() + "\tnot-a-digest\n" +
		"3\t" + d.String() + "\t" + d.String() + "\n")
	entries := parseLog(data)
	if len(entries) != 2 {
		t.Fatalf("parseLog = %d entries, want 2 (only the invalid Old field is dropped)", len(entries))
	}
	if !entries[0].Digest.Equal(d) || !entries[0].Old.IsZero() {
		t.Errorf("parseLog[0] = %+v, want Digest=%s Old=absent", entries[0], d)
	}
	if !entries[1].Digest.Equal(d) || !entries[1].Old.Equal(d) {
		t.Errorf("parseLog[1] = %+v, want Digest=%s Old=%s", entries[1], d, d)
	}
}

// TestListReportsUnreadableRef covers the walk callback's readValue branch: a
// ref the process may not read is a real failure, so List reports it — and
// WalkDir propagates the callback's error — instead of silently dropping the
// ref from the listing.
//
// POSIX permission bits are the only portable way to produce that error, so the
// test is skipped on Windows (ACLs, not mode bits) and under a superuser
// account (permission checks do not apply).
func TestListReportsUnreadableRef(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permission bits and an unprivileged user")
	}
	ctx := context.Background()
	dir := t.TempDir()
	s := &Store{dir: dir, now: time.Now}
	if err := s.Set(ctx, "main", sha256.Of([]byte("v1"))); err != nil {
		t.Fatal(err)
	}
	value := filepath.Join(dir, "main")
	if err := os.Chmod(value, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(value, 0o644) })

	if _, err := s.List(ctx); err == nil {
		t.Fatal("List with an unreadable ref = nil error, want error")
	}
}

// TestStoreWideMethodsReportUnreadableDir covers the failure that starts at the
// refs directory itself: WalkDir hands the read error to its callback, which
// List must return rather than swallow, and Resolve and Roots — which answer
// from List — report it instead of answering "no refs". Same skip conditions as
// TestListReportsUnreadableRef.
func TestStoreWideMethodsReportUnreadableDir(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permission bits and an unprivileged user")
	}
	ctx := context.Background()
	dir := t.TempDir()
	s := &Store{dir: dir, now: time.Now}
	if err := s.Set(ctx, "main", sha256.Of([]byte("v1"))); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	if _, err := s.List(ctx); err == nil {
		t.Error("List over an unreadable refs directory = nil error, want error")
	}
	if _, err := s.Resolve(ctx, "main"); err == nil {
		t.Error("Resolve over an unreadable refs directory = nil error, want error")
	}
	if _, err := s.Roots(ctx); err == nil {
		t.Error("Roots over an unreadable refs directory = nil error, want error")
	}
}

// TestDeleteReportsUndeletableValue covers Delete's os.Remove failure: the ref
// is readable, so its tombstone is appended, but the directory holding the
// value is not writable, so the value cannot actually be removed and Delete
// reports that instead of claiming the ref is gone. Same skip conditions as
// TestListReportsUnreadableRef.
func TestDeleteReportsUndeletableValue(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permission bits and an unprivileged user")
	}
	ctx := context.Background()
	dir := t.TempDir()
	s := &Store{dir: dir, now: time.Now}
	if err := s.Set(ctx, "main", sha256.Of([]byte("v1"))); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil { // read + traverse, no write
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	if err := s.Delete(ctx, "main"); err == nil {
		t.Fatal("Delete of an undeletable ref = nil error, want error")
	}
}

// Deliberately uncovered in refs.go, none reachable from a test that does not
// depend on the host filesystem or scheduler:
//
//   - writeFileAtomic's Write/Sync/Close failure returns (491-503): the temp
//     file is created successfully and is this process's own descriptor, so
//     only a device-level failure (a full or read-only filesystem, an I/O
//     error) can produce one — no up-front filesystem state can.
//   - appendLog's Write failure return (425-426): same reason, on a log file
//     opened O_APPEND.
//   - List's filepath.Rel failure return (277-278): Rel only fails when the two
//     paths share no common root (a different Windows volume), which cannot be
//     arranged from inside one temp directory.
//   - syncParentDir's Windows short-circuit (516-517): covered only when the
//     suite runs on Windows, which the WSL coverage gate by definition is not.
