package fs

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dmundt/go-cask/cas"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
	"github.com/dmundt/go-cask/internal/test"
)

// TestPathToDigest pins the path→digest reconstruction: the last path element
// is the hex digest, whatever the fan-out depth, and a file whose name is not a
// digest is rejected (List/Stats then skip it).
func TestPathToDigest(t *testing.T) {
	d := digestOf([]byte("path to digest"))
	hexDigest := d.String()

	for _, rel := range []string{
		hexDigest,                            // flat layout
		filepath.Join("aa", hexDigest),       // one fan-out level
		filepath.Join("aa", "bb", hexDigest), // two levels
	} {
		got, err := pathToDigest(rel)
		if err != nil {
			t.Errorf("pathToDigest(%q) = %v", rel, err)
			continue
		}
		if !got.Equal(d) {
			t.Errorf("pathToDigest(%q) = %s, want %s", rel, got, d)
		}
	}

	for _, rel := range []string{"", "not-hex", filepath.Join("aa", "zz"), "ab.tmp"} {
		if _, err := pathToDigest(rel); !errors.Is(err, cas.ErrInvalidDigest) {
			t.Errorf("pathToDigest(%q) = %v, want ErrInvalidDigest", rel, err)
		}
	}
}

// TestVerifyRejectsWrongWidthDigest pins the injected hasher's guard: Verify
// validates the key before reading, so a digest that cannot name an object is
// reported as invalid rather than as a mismatch or a miss.
func TestVerifyRejectsWrongWidthDigest(t *testing.T) {
	s := mustFS(t)
	short := cas.NewDigest([]byte{1, 2, 3})
	if err := s.Verify(context.Background(), short, sha256.New()); !errors.Is(err, cas.ErrInvalidDigest) {
		t.Fatalf("Verify(short digest) = %v, want ErrInvalidDigest", err)
	}
}

// TestDirSyncRoundTrip exercises the WithDirSync path: a Put and a Delete each
// sync the object's parent directory (so the rename survives a crash).
func TestDirSyncRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := mustFS(t, WithDirSync())

	data := []byte("dir-synced object")
	d := digestOf(data)
	if err := s.Put(ctx, d, bytes.NewReader(data)); err != nil {
		t.Fatalf("Put with WithDirSync = %v", err)
	}
	rc, err := s.Get(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	got, err := test.ReadAllAndClose(rc)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("Get = %q, %v", got, err)
	}
	if err := s.Verify(ctx, d, sha256.New()); err != nil {
		t.Fatalf("Verify = %v", err)
	}
	if err := s.Delete(ctx, d); err != nil {
		t.Fatalf("Delete with WithDirSync = %v", err)
	}
}

// TestCleanRemovesTempCollisionFallbacks pins Clean's scope: both "<hex>.tmp"
// and the "<hex>.tmp.<n>" collision fallbacks createTempExcl may leave behind
// are reclaimed, while a real object file is not.
func TestCleanRemovesTempCollisionFallbacks(t *testing.T) {
	ctx := context.Background()
	s := mustFS(t)
	d := digestOf([]byte("kept object"))
	if err := s.Put(ctx, d, strings.NewReader("kept object")); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Dir(s.digestPath(d))
	temps := []string{
		filepath.Join(dir, d.String()+".tmp"),
		filepath.Join(dir, d.String()+".tmp.1"),
		filepath.Join(dir, d.String()+".tmp.2"),
	}
	for _, p := range temps {
		if err := os.WriteFile(p, []byte("crash leftover"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := s.Clean(ctx, 0)
	if err != nil {
		t.Fatalf("Clean = %v", err)
	}
	if removed != len(temps) {
		t.Fatalf("Clean removed %d files, want %d", removed, len(temps))
	}
	for _, p := range temps {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("temp file %s survived Clean", p)
		}
	}
	// The object itself is not a temp file and must survive.
	if err := s.Verify(ctx, d, sha256.New()); err != nil {
		t.Fatalf("object file was reclaimed by Clean: %v", err)
	}
}

// TestCleanKeepsFreshTempFiles pins the grace rule: with olderThan > 0 a temp
// file younger than the cutoff is left for a later sweep.
func TestCleanKeepsFreshTempFiles(t *testing.T) {
	ctx := context.Background()
	s := mustFS(t)
	dir := filepath.Join(s.base, "aa")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(dir, strings.Repeat("ab", 32)+".tmp")
	if err := os.WriteFile(tmp, []byte("fresh"), 0o644); err != nil {
		t.Fatal(err)
	}
	removed, err := s.Clean(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 {
		t.Fatalf("Clean(hour) removed %d fresh temp files, want 0", removed)
	}
	if _, err := os.Stat(tmp); err != nil {
		t.Fatalf("fresh temp file was removed: %v", err)
	}
}

// TestPutUniqueTempPerWriterFallback pins that a Put into a directory holding a
// stale temp file still succeeds: createTempExcl falls back to "<hex>.tmp.<n>".
func TestPutUniqueTempPerWriterFallback(t *testing.T) {
	ctx := context.Background()
	s := mustFS(t)
	data := []byte("fallback temp")
	d := digestOf(data)
	dir := filepath.Dir(s.digestPath(d))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Occupy the first candidate name.
	if err := os.WriteFile(filepath.Join(dir, filepath.Base(s.digestPath(d))+".tmp"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, d, bytes.NewReader(data)); err != nil {
		t.Fatalf("Put with an occupied temp name = %v", err)
	}
	if err := s.Verify(ctx, d, sha256.New()); err != nil {
		t.Fatalf("Verify after fallback Put = %v", err)
	}
	// The stale temp file is still there, and the object is intact.
	if _, err := os.Stat(filepath.Join(dir, filepath.Base(s.digestPath(d))+".tmp")); err != nil {
		t.Fatalf("stale temp file disappeared: %v", err)
	}
}

func TestFSCorruptionRecoveryFallsBackToLooseObject(t *testing.T) {
	ctx := context.Background()
	s := mustFS(t)
	d := digestOf([]byte("recovery object"))
	if err := s.Put(ctx, d, strings.NewReader("recovery object")); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(s.digestPath(d), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(ctx, d, sha256.New()); !errors.Is(err, cas.ErrDigestMismatch) {
		t.Fatalf("Verify(tampered) = %v, want ErrDigestMismatch", err)
	}

	if err := s.Put(ctx, d, strings.NewReader("recovery object")); err != nil {
		t.Fatalf("re-put valid bytes = %v", err)
	}
	if err := s.Verify(ctx, d, sha256.New()); err != nil {
		t.Fatalf("Verify(recovered) = %v", err)
	}
}

// TestBackendOptionsAreAcceptable pins that the exported options compose, so a
// caller can build a store with a custom layout and dir sync together.
func TestBackendOptionsCompose(t *testing.T) {
	s, err := New(t.TempDir(), WithFanOut(4), WithFanLevels(2), WithDirSync())
	if err != nil {
		t.Fatalf("New with combined options = %v", err)
	}
	if err := s.Put(context.Background(), digestOf([]byte("x")), strings.NewReader("x")); err != nil {
		t.Fatalf("Put into a custom layout = %v", err)
	}
	if _, err := New(t.TempDir(), WithFanOut(3), WithFanLevels(22)); err == nil {
		t.Fatal("fan-out beyond MaxFanDepth must be rejected")
	}
}

// TestShortDigestRejectedNotPanicking pins the fix for a slice-bounds panic: a
// key whose hex form is shorter than FanOut × FanLevels cannot name an object of
// this layout, so every key-taking method reports ErrInvalidDigest. The backend
// names no algorithm, so it cannot know a client's digest width — but it can
// measure the key it was handed (cas-core §4.4).
func TestShortDigestRejectedNotPanicking(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name              string
		fanOut, fanLevels int
		width             int // digest bytes
	}{
		{"1 byte, (2,2)", 2, 2, 1},
		{"2 bytes, (4,2)", 4, 2, 2},
		{"3 bytes, (2,4)", 2, 4, 3},
		{"0 bytes is absent anyway, (2,2)", 2, 2, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := New(t.TempDir(), WithFanOut(tc.fanOut), WithFanLevels(tc.fanLevels))
			if err != nil {
				t.Fatal(err)
			}
			d := make(cas.Digest, tc.width)
			for i := range d {
				d[i] = 0xab
			}
			if err := s.Put(ctx, d, strings.NewReader("x")); !errors.Is(err, cas.ErrInvalidDigest) {
				t.Errorf("Put = %v, want ErrInvalidDigest", err)
			}
			if _, err := s.Get(ctx, d); !errors.Is(err, cas.ErrInvalidDigest) {
				t.Errorf("Get = %v, want ErrInvalidDigest", err)
			}
			if _, err := s.Exists(ctx, d); !errors.Is(err, cas.ErrInvalidDigest) {
				t.Errorf("Exists = %v, want ErrInvalidDigest", err)
			}
			if err := s.Delete(ctx, d); !errors.Is(err, cas.ErrInvalidDigest) {
				t.Errorf("Delete = %v, want ErrInvalidDigest", err)
			}
			if _, err := s.Size(ctx, d); !errors.Is(err, cas.ErrInvalidDigest) {
				t.Errorf("Size = %v, want ErrInvalidDigest", err)
			}
			if _, err := s.ModTime(ctx, d); !errors.Is(err, cas.ErrInvalidDigest) {
				t.Errorf("ModTime = %v, want ErrInvalidDigest", err)
			}
			if err := s.Verify(ctx, d, sha256.New()); !errors.Is(err, cas.ErrInvalidDigest) {
				t.Errorf("Verify = %v, want ErrInvalidDigest", err)
			}
			// A key that fills the layout exactly still works: the guard rejects
			// only what the layout cannot address.
			if tc.width > 0 {
				ok := make(cas.Digest, (tc.fanOut*tc.fanLevels+1)/2)
				for i := range ok {
					ok[i] = 0xcd
				}
				if err := s.Put(ctx, ok, strings.NewReader("x")); err != nil {
					t.Errorf("Put of a wide-enough key = %v", err)
				}
			}
		})
	}
}

// TestSweepsSkipUnaddressableDigestNames pins that a digest-named file the
// layout cannot address is still reported by List (documented behaviour) but is
// never touched by GC/Prune — they used to panic on it while building its path.
func TestSweepsSkipUnaddressableDigestNames(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	s, err := New(base, WithFanLevels(2)) // needs >= 4 hex chars
	if err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(base, "ab") // 1 byte of hex: unaddressable
	if err := os.WriteFile(stray, []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	digests, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(digests) != 1 || digests[0].String() != "ab" {
		t.Fatalf("List = %v, want the stray digest reported", digests)
	}
	if err := s.GC(ctx, map[string]bool{}); err != nil {
		t.Fatalf("GC with an unaddressable digest-named file = %v", err)
	}
	if _, err := s.Prune(ctx, nil, 0, false); err != nil {
		t.Fatalf("Prune with an unaddressable digest-named file = %v", err)
	}
	if _, err := os.Stat(stray); err != nil {
		t.Fatalf("stray file must survive the sweeps (it is not an object of this layout): %v", err)
	}
}

// TestBackendErrorBranchesOnMissingBase covers the paths a store takes when
// its own base directory disappears underneath it — a crash-recovery or
// operator-error shape, not a hypothetical. Clean treats it as nothing to do;
// the walkers report it; GC and Prune surface the walk failure rather than
// silently sweeping an empty list, which would look like "everything is
// unreachable" and delete nothing.
func TestBackendErrorBranchesOnMissingBase(t *testing.T) {
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), "store")
	s, err := New(base)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(base); err != nil {
		t.Fatal(err)
	}
	if removed, err := s.Clean(ctx, 0); err != nil || removed != 0 {
		t.Fatalf("Clean(missing base) = (%d, %v), want (0, nil)", removed, err)
	}
	if _, err := s.List(ctx); err == nil {
		t.Fatal("List(missing base) = nil error, want error")
	}
	if _, err := s.Stats(ctx); err == nil {
		t.Fatal("Stats(missing base) = nil error, want error")
	}
	if err := s.GC(ctx, map[string]bool{}); err == nil {
		t.Fatal("GC(missing base) = nil error, want error")
	}
	if _, err := s.Prune(ctx, nil, 0, true); err == nil {
		t.Fatal("Prune(missing base) = nil error, want error")
	}
}

// TestVerifyNilBackend pins the nil-receiver guard: Verify is reachable
// through the cas.Backend interface, where a typed nil is easy to hand in, so
// it reports an error instead of panicking.
func TestVerifyNilBackend(t *testing.T) {
	var s *Backend
	if err := s.Verify(context.Background(), digestOf([]byte("x")), sha256.New()); err == nil {
		t.Fatal("Verify on a nil backend = nil error, want error")
	}
}

// TestCleanReportsAnUnreadableDirectory covers the two failure branches Clean
// keeps for a store it cannot fully traverse: a directory it may not read at
// all, and one it may read but not write, so the temp file inside it cannot be
// removed. Both are wrapped and returned rather than reported as a clean sweep,
// because "removed 0, no error" would tell an operator the store is tidy when
// it is not.
//
// POSIX permission bits are the only portable way to produce those errors, so
// the test is skipped on Windows (ACLs, not mode bits) and under a superuser
// account (permission checks do not apply).
func TestCleanReportsAnUnreadableDirectory(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permission bits and an unprivileged user")
	}
	ctx := context.Background()

	t.Run("unreadable", func(t *testing.T) {
		base := filepath.Join(t.TempDir(), "store")
		s, err := New(base)
		if err != nil {
			t.Fatal(err)
		}
		sealed := filepath.Join(base, "aa")
		if err := os.MkdirAll(sealed, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(sealed, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(sealed, 0o755) })

		if _, err := s.Clean(ctx, 0); err == nil {
			t.Fatal("Clean over an unreadable directory = nil error, want error")
		}
	})

	t.Run("undeletable", func(t *testing.T) {
		base := filepath.Join(t.TempDir(), "store")
		s, err := New(base)
		if err != nil {
			t.Fatal(err)
		}
		locked := filepath.Join(base, "bb")
		if err := os.MkdirAll(locked, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(locked, "stale.tmp"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(locked, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

		if _, err := s.Clean(ctx, 0); err == nil {
			t.Fatal("Clean over an undeletable temp file = nil error, want error")
		}
	})
}

// errWalkEntryFailed stands in for a directory-read or stat failure that is not
// a vanished entry: the only failure a walk is allowed to swallow.
var errWalkEntryFailed = errors.New("test: entry read failed")

// fakeDirEntry is a walk entry a test controls, so the List, Stats and Clean
// callbacks can be driven through the shapes a real scan produces only when an
// entry vanishes between the directory read and the stat (a concurrent
// Delete/Put). Every fixture is a file entry and its Info always fails: the
// walk-seam tests below target exactly the branches that stat an entry and find
// it gone or unreadable, while the healthy paths — and the directory-skip
// branch — are covered end to end against real files. A nil infoErr yields
// errWalkEntryFailed, so a mistake surfaces as an error instead of a nil deref.
type fakeDirEntry struct {
	name    string
	infoErr error
}

func (e fakeDirEntry) Name() string      { return e.name }
func (e fakeDirEntry) IsDir() bool       { return false }
func (e fakeDirEntry) Type() fs.FileMode { return 0 }

func (e fakeDirEntry) Info() (fs.FileInfo, error) {
	if e.infoErr == nil {
		return nil, errWalkEntryFailed
	}
	return nil, e.infoErr
}

// fakeWalkCall is one callback invocation a scripted walk makes: the path and
// entry handed to fn, and the err argument filepath.WalkDir passes when it
// could not read the entry.
type fakeWalkCall struct {
	path  string
	entry fakeDirEntry
	err   error
}

// scriptedWalk returns a walk seam (Backend.walk) that makes exactly the calls
// given, in order, and then reports walkErr as the walk's own failure.
// Scripting the walk is what makes the vanish-mid-scan branches deterministic:
// against a real filesystem they are reachable only by winning a race against a
// concurrent Delete/Put.
func scriptedWalk(calls []fakeWalkCall, walkErr error) func(string, fs.WalkDirFunc) error {
	return func(_ string, fn fs.WalkDirFunc) error {
		for _, c := range calls {
			if err := fn(c.path, c.entry, c.err); err != nil {
				return err
			}
		}
		return walkErr
	}
}

// TestListWalkBranches drives List's walk callback through every error shape it
// distinguishes, with a scripted walk so a vanished entry need not be raced
// for: a vanished entry is tolerated (the list is still a truthful snapshot of
// what was there), an unreadable entry and a failed walk are returned, a
// canceled context stops the walk, and a path that cannot be made relative to
// the store base names no object and is skipped.
func TestListWalkBranches(t *testing.T) {
	s := mustFS(t)
	hexDigest := digestOf([]byte("listed object")).String()
	entryPath := filepath.Join(s.base, hexDigest)

	for _, tc := range []struct {
		name    string
		calls   []fakeWalkCall
		walkErr error
		ctx     context.Context // nil means context.Background()
		wantErr error           // errors.Is target; nil means no error
	}{
		{
			name:  "entry vanished mid-walk",
			calls: []fakeWalkCall{{path: entryPath, entry: fakeDirEntry{name: hexDigest}, err: fs.ErrNotExist}},
		},
		{
			name:    "entry read failed",
			calls:   []fakeWalkCall{{path: entryPath, entry: fakeDirEntry{name: hexDigest}, err: errWalkEntryFailed}},
			wantErr: errWalkEntryFailed,
		},
		{
			name:  "path that is not under the store base",
			calls: []fakeWalkCall{{path: "outside-" + hexDigest, entry: fakeDirEntry{name: hexDigest}}},
		},
		{
			name:    "walk failed with a vanished base",
			walkErr: fs.ErrNotExist,
		},
		{
			name:    "walk failed for another reason",
			walkErr: errWalkEntryFailed,
			wantErr: errWalkEntryFailed,
		},
		{
			// Cancellation is reported on the second Err call: List's entry
			// check, then the walk callback's check.
			name:    "canceled inside the walk",
			calls:   []fakeWalkCall{{path: entryPath, entry: fakeDirEntry{name: hexDigest}}},
			ctx:     &cancelAfterNErr{Context: context.Background(), n: 2},
			wantErr: context.Canceled,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s.walk = scriptedWalk(tc.calls, tc.walkErr)
			ctx := tc.ctx
			if ctx == nil {
				ctx = context.Background()
			}
			digests, err := s.List(ctx)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("List = %v, %v; want %v", digests, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("List = %v, %v; want no error", digests, err)
			}
			if len(digests) != 0 {
				t.Fatalf("List = %v; want no digests", digests)
			}
		})
	}
}

// TestStatsWalkBranches is TestListWalkBranches for Stats, plus the two branches
// only Stats has: an object that vanishes before its own stat is skipped as
// transient, while any other stat failure is returned rather than silently
// undercounting the store.
func TestStatsWalkBranches(t *testing.T) {
	s := mustFS(t)
	hexDigest := digestOf([]byte("counted object")).String()
	entryPath := filepath.Join(s.base, hexDigest)

	for _, tc := range []struct {
		name    string
		calls   []fakeWalkCall
		walkErr error
		ctx     context.Context // nil means context.Background()
		wantErr error           // errors.Is target; nil means no error
	}{
		{
			name:  "entry vanished mid-walk",
			calls: []fakeWalkCall{{path: entryPath, entry: fakeDirEntry{name: hexDigest}, err: fs.ErrNotExist}},
		},
		{
			name:    "entry read failed",
			calls:   []fakeWalkCall{{path: entryPath, entry: fakeDirEntry{name: hexDigest}, err: errWalkEntryFailed}},
			wantErr: errWalkEntryFailed,
		},
		{
			name:  "path that is not under the store base",
			calls: []fakeWalkCall{{path: "outside-" + hexDigest, entry: fakeDirEntry{name: hexDigest}}},
		},
		{
			name:  "object vanished before its stat",
			calls: []fakeWalkCall{{path: entryPath, entry: fakeDirEntry{name: hexDigest, infoErr: fs.ErrNotExist}}},
		},
		{
			name:    "object stat failed",
			calls:   []fakeWalkCall{{path: entryPath, entry: fakeDirEntry{name: hexDigest, infoErr: errWalkEntryFailed}}},
			wantErr: errWalkEntryFailed,
		},
		{
			name:    "walk failed with a vanished base",
			walkErr: fs.ErrNotExist,
		},
		{
			name:    "walk failed for another reason",
			walkErr: errWalkEntryFailed,
			wantErr: errWalkEntryFailed,
		},
		{
			// Cancellation is reported on the second Err call: Stats' entry
			// check, then the walk callback's check.
			name:    "canceled inside the walk",
			calls:   []fakeWalkCall{{path: entryPath, entry: fakeDirEntry{name: hexDigest}}},
			ctx:     &cancelAfterNErr{Context: context.Background(), n: 2},
			wantErr: context.Canceled,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s.walk = scriptedWalk(tc.calls, tc.walkErr)
			ctx := tc.ctx
			if ctx == nil {
				ctx = context.Background()
			}
			st, err := s.Stats(ctx)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Stats = %v, %v; want %v", st, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Stats = %v, %v; want no error", st, err)
			}
			if st == nil || st.ObjectCount != 0 || st.TotalSize != 0 {
				t.Fatalf("Stats = %v; want an empty report", st)
			}
		})
	}
}

// TestCleanWalkBranches drives Clean's walk callback through the failures a
// scripted walk can name deterministically: a temp file that vanishes before
// os.Remove reaches it is tolerated (a concurrent sweep got there first), while
// a temp file whose age cannot be read, and a walk that fails for any reason
// other than a vanished base, are returned instead of reported as a clean
// sweep.
func TestCleanWalkBranches(t *testing.T) {
	s := mustFS(t)
	const tempName = "ghost.tmp"
	tempPath := filepath.Join(s.base, tempName)

	for _, tc := range []struct {
		name      string
		calls     []fakeWalkCall
		walkErr   error
		olderThan time.Duration
		wantErr   error // errors.Is target; nil means no error
	}{
		{
			name:  "temp file vanished before removal",
			calls: []fakeWalkCall{{path: tempPath, entry: fakeDirEntry{name: tempName}}},
		},
		{
			name:      "temp file age cannot be read",
			calls:     []fakeWalkCall{{path: tempPath, entry: fakeDirEntry{name: tempName, infoErr: errWalkEntryFailed}}},
			olderThan: time.Hour,
			wantErr:   errWalkEntryFailed,
		},
		{
			name:    "walk failed with a vanished base",
			walkErr: fs.ErrNotExist,
		},
		{
			name:    "walk failed for another reason",
			walkErr: errWalkEntryFailed,
			wantErr: errWalkEntryFailed,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s.walk = scriptedWalk(tc.calls, tc.walkErr)
			removed, err := s.Clean(context.Background(), tc.olderThan)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Clean = %d, %v; want %v", removed, err, tc.wantErr)
				}
				return
			}
			if err != nil || removed != 0 {
				t.Fatalf("Clean = %d, %v; want 0, nil", removed, err)
			}
		})
	}
}

// TestGCCancelsMidSweep covers GC's per-object context check: the sweep reports
// the caller's cancellation instead of deleting on. The scripted walk reports
// one object, so cancellation falls on the loop's own Err call — the third one,
// after List's entry check and the walk entry.
func TestGCCancelsMidSweep(t *testing.T) {
	s := mustFS(t)
	d := digestOf([]byte("gc canceled"))
	s.walk = scriptedWalk([]fakeWalkCall{{path: filepath.Join(s.base, d.String()), entry: fakeDirEntry{name: d.String()}}}, nil)

	ctx := &cancelAfterNErr{Context: context.Background(), n: 3}
	if err := s.GC(ctx, map[string]bool{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("GC with a canceled sweep = %v, want context.Canceled", err)
	}
}

// TestPruneCancelsMidSweep covers Prune's per-object context check in its
// selection loop: with one scripted object, cancellation falls on the third Err
// call — List's entry check, the walk entry, then the selection loop.
func TestPruneCancelsMidSweep(t *testing.T) {
	ctx := context.Background()
	s := mustFS(t)
	data := []byte("prune canceled")
	d := digestOf(data)
	if err := s.Put(ctx, d, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	s.walk = scriptedWalk([]fakeWalkCall{{path: s.digestPath(d), entry: fakeDirEntry{name: d.String()}}}, nil)

	canceled := &cancelAfterNErr{Context: ctx, n: 3}
	if _, err := s.Prune(canceled, nil, 0, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("Prune with a canceled sweep = %v, want context.Canceled", err)
	}
	if ok, _ := s.Exists(ctx, d); !ok {
		t.Fatal("a canceled selection loop deleted the object")
	}
}

// TestPruneCancelsMidDelete covers the check inside Prune's deletion loop: the
// object is selected as doomed and exists, so cancellation falls on the fourth
// Err call — List's entry check, the walk entry, the selection loop, then the
// deletion loop — and stops the sweep before the delete.
func TestPruneCancelsMidDelete(t *testing.T) {
	ctx := context.Background()
	s := mustFS(t)
	data := []byte("prune delete canceled")
	d := digestOf(data)
	if err := s.Put(ctx, d, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	s.walk = scriptedWalk([]fakeWalkCall{{path: s.digestPath(d), entry: fakeDirEntry{name: d.String()}}}, nil)

	canceled := &cancelAfterNErr{Context: ctx, n: 4}
	if _, err := s.Prune(canceled, nil, 0, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("Prune with a canceled deletion loop = %v, want context.Canceled", err)
	}
	if ok, _ := s.Exists(ctx, d); !ok {
		t.Fatal("a canceled deletion loop deleted the object")
	}
}

// TestSweepsReportDeleteFailures covers GC's and Prune's delete-error branches:
// an object List reports that cannot be removed — here a non-empty directory
// sits where the object file belongs, the shape TestDeleteDirectoryError uses —
// fails the sweep instead of being reported as a completed collection.
func TestSweepsReportDeleteFailures(t *testing.T) {
	ctx := context.Background()
	s := mustFS(t)
	d := digestOf([]byte("undeletable object"))
	if err := os.MkdirAll(filepath.Join(s.digestPath(d), "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	s.walk = scriptedWalk([]fakeWalkCall{{path: s.digestPath(d), entry: fakeDirEntry{name: d.String()}}}, nil)

	if err := s.GC(ctx, map[string]bool{}); err == nil {
		t.Error("GC must report an object it cannot delete")
	}
	if _, err := s.Prune(ctx, nil, 0, false); err == nil {
		t.Error("Prune must report an object it cannot delete")
	}
}

// TestPruneSkipsObjectsMissingFromDisk covers Prune's stat guard: a path List
// reported (it is named like an object) whose canonical location is gone is
// skipped, because the age check and the delete it protects have nothing to act
// on.
func TestPruneSkipsObjectsMissingFromDisk(t *testing.T) {
	s := mustFS(t)
	d := digestOf([]byte("listed but absent"))
	s.walk = scriptedWalk([]fakeWalkCall{{path: filepath.Join(s.base, d.String()), entry: fakeDirEntry{name: d.String()}}}, nil)

	doomed, err := s.Prune(context.Background(), nil, 0, false)
	if err != nil || len(doomed) != 0 {
		t.Fatalf("Prune over an object missing from disk = %v, %v; want none, nil", doomed, err)
	}
}

// TestStatFailuresAreNotReportedAsMissing pins the non-ErrNotExist stat
// branches of Exists, Size and ModTime: a path the OS cannot stat at all is a
// backend failure, not proof that the object is absent, so it must not be
// reported as (false, nil) or as ErrNotFound. A NUL byte makes every stat fail
// with EINVAL on each supported platform — the shape TestCleanUnstattableBase
// already uses.
func TestStatFailuresAreNotReportedAsMissing(t *testing.T) {
	ctx := context.Background()
	d := digestOf([]byte("unstattable object"))
	for _, tc := range []struct {
		name string
		run  func(s *Backend) error
	}{
		{"Exists", func(s *Backend) error { _, err := s.Exists(ctx, d); return err }},
		{"Size", func(s *Backend) error { _, err := s.Size(ctx, d); return err }},
		{"ModTime", func(s *Backend) error { _, err := s.ModTime(ctx, d); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := mustFS(t)
			s.base = "bad\x00base"
			err := tc.run(s)
			if err == nil {
				t.Fatalf("%s over an unstattable base = nil error, want a stat failure", tc.name)
			}
			if errors.Is(err, cas.ErrNotFound) {
				t.Fatalf("%s = %v; a stat failure must not be reported as ErrNotFound", tc.name, err)
			}
		})
	}
}

// TestGetReportsUnreadableObject covers Get's non-ErrNotExist open branch: an
// object file the process may not read is a real failure, not a miss, so it is
// reported instead of ErrNotFound. POSIX permission bits are the only portable
// way to produce it, so the test is skipped on Windows (ACLs, not mode bits)
// and under a superuser account (permission checks do not apply).
func TestGetReportsUnreadableObject(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permission bits and an unprivileged user")
	}
	ctx := context.Background()
	s := mustFS(t)
	data := []byte("unreadable object")
	d := digestOf(data)
	if err := s.Put(ctx, d, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(s.digestPath(d), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(s.digestPath(d), 0o644) })

	rc, err := s.Get(ctx, d)
	if err == nil {
		rc.Close()
		t.Fatal("Get of an unreadable object = nil error, want error")
	}
	if errors.Is(err, cas.ErrNotFound) {
		t.Fatalf("Get = %v; an unreadable object must not be reported as ErrNotFound", err)
	}
}

// TestPutReportsDirSyncFailure covers Put's directory-sync branch: the object is
// written, but the directory holding it cannot be opened for the fsync, so Put
// reports the failure rather than claiming the publish is durable. Opening a
// directory needs its read bit while creating the temp file needs only write
// and execute, so mode 0o300 reaches that branch and nothing earlier. POSIX
// permission bits again: skipped on Windows and under a superuser account.
func TestPutReportsDirSyncFailure(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permission bits and an unprivileged user")
	}
	ctx := context.Background()
	s := mustFS(t, WithDirSync())
	data := []byte("dir sync failure")
	d := digestOf(data)
	dir := filepath.Dir(s.digestPath(d))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o300); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	if err := s.Put(ctx, d, bytes.NewReader(data)); err == nil {
		t.Fatal("Put must report a failed directory sync")
	}
}

// TestCreateTempExclFallbackFailure covers the retry loop's non-collision
// failure branch: the first candidate name is taken, but the fallback name is
// rejected (here it is longer than a single name may be), so the loop reports
// that error instead of spinning to its 10000-candidate bound. The base name is
// padded to the classic NAME_MAX so the ".1" candidate cannot fit; a filesystem
// with a wider name budget accepts it and the test skips rather than asserting
// a platform limit.
func TestCreateTempExclFallbackFailure(t *testing.T) {
	dir := t.TempDir()
	const maxName = 255
	path := filepath.Join(dir, strings.Repeat("a", maxName-len(".tmp")))
	base := path + ".tmp" // exactly NAME_MAX characters
	if err := os.WriteFile(base, []byte("occupied"), 0o644); err != nil {
		t.Skipf("this filesystem cannot hold a %d-character name: %v", maxName, err)
	}
	f, tmp, err := createTempExcl(path)
	if err == nil {
		f.Close()
		os.Remove(tmp)
		t.Skipf("this filesystem accepts names longer than %d characters", maxName)
	}
	if os.IsExist(err) {
		t.Fatalf("createTempExcl = %v; want the fallback name rejected, not reported as a collision", err)
	}
}
