package atomicfile

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// failingFile is a temp file whose every write step can be made to fail, so each
// phase of the publish is covered without asking a real filesystem to fail on
// demand. It is the seam cas/pack carried before this package owned the
// sequence (go-cask#256, go-cask#339).
type failingFile struct {
	writeErr error
	syncErr  error
	closeErr error

	// onSync runs when Sync is called, so a test can change the world — cancel
	// the context, for one — between the write and the rename.
	onSync func()

	name    string
	written []byte
	closed  int
}

func (f *failingFile) Name() string { return f.name }

func (f *failingFile) Write(p []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	f.written = append(f.written, p...)
	return len(p), nil
}

func (f *failingFile) Sync() error {
	if f.onSync != nil {
		f.onSync()
	}
	return f.syncErr
}

func (f *failingFile) Close() error {
	f.closed++
	return f.closeErr
}

// opsFor builds the publish seam around f, with the given failures. remove
// records what it was asked to remove; rename always succeeds unless a test
// overrides it.
func opsFor(f *failingFile, createErr, renameErr error) (ops, *[]string) {
	removed := &[]string{}
	o := defaultOps()
	o.createTemp = func(dir string, _ os.FileMode) (file, string, error) {
		if createErr != nil {
			return nil, "", createErr
		}
		f.name = dir + string(filepath.Separator) + "manifest.json.tmp"
		return f, f.name, nil
	}
	o.remove = func(name string) error {
		*removed = append(*removed, name)
		return nil
	}
	o.rename = func(_, _ string) error { return renameErr }
	return o, removed
}

// TestPublishFailurePhases covers every phase of the publish: a failure at any
// one of them reports that phase, publishes nothing, and closes the temp file.
func TestPublishFailurePhases(t *testing.T) {
	ctx := context.Background()
	want := errors.New("phase failed")

	cases := []struct {
		name      string
		file      *failingFile
		createErr error
		renameErr error
		phase     Phase
	}{
		{"create temp", &failingFile{}, want, nil, PhaseTemp},
		{"write", &failingFile{writeErr: want}, nil, nil, PhaseWrite},
		{"sync", &failingFile{syncErr: want}, nil, nil, PhaseSync},
		{"close", &failingFile{closeErr: want}, nil, nil, PhaseClose},
		{"rename", &failingFile{}, nil, want, PhasePublish},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state", "manifest.json")
			o, _ := opsFor(tc.file, tc.createErr, tc.renameErr)
			err := publishWith(ctx, path, bytes.NewReader([]byte("demo")), Options{}, o)
			if !errors.Is(err, want) {
				t.Fatalf("publish = %v, want %v", err, want)
			}
			phase, cause, ok := FailedPhase(err)
			if !ok || phase != tc.phase || !errors.Is(cause, want) {
				t.Fatalf("FailedPhase = (%v, %v, %v), want (%v, %v, true)", phase, cause, ok, tc.phase, want)
			}
			if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("a failed publish wrote its target: %v", statErr)
			}
			if tc.file.name != "" && tc.file.closed == 0 {
				t.Fatal("the failed publish left the temp file open")
			}
		})
	}
}

// TestPublishRemovesTempOnFailure pins the cleanup itself: the temp file the
// publish created is removed on failure.
func TestPublishRemovesTempOnFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "manifest.json")
	f := &failingFile{syncErr: errors.New("sync failed")}

	o, removed := opsFor(f, nil, nil)
	if err := publishWith(context.Background(), path, bytes.NewReader(nil), Options{}, o); err == nil {
		t.Fatal("publish reported success")
	}
	if len(*removed) != 1 || (*removed)[0] != f.Name() {
		t.Fatalf("removed = %v, want the temp file %s", *removed, f.Name())
	}
}

// TestPublishStopsOnCanceledContextAfterWrite is the context check that runs
// after the bytes were written: a context canceled while the file is being
// written must not publish it, and the temp file is cleaned up.
func TestPublishStopsOnCanceledContextAfterWrite(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	path := filepath.Join(t.TempDir(), "state", "manifest.json")
	f := &failingFile{}

	o, _ := opsFor(f, nil, nil)
	o.createTemp = func(dir string, _ os.FileMode) (file, string, error) {
		f.name = filepath.Join(dir, "manifest.json.tmp")
		cancel() // canceled while the temp file exists, before the rename
		return f, f.name, nil
	}
	if err := publishWith(ctx, path, bytes.NewReader([]byte("demo")), Options{}, o); !errors.Is(err, context.Canceled) {
		t.Fatalf("publish = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a canceled publish wrote its target: %v", err)
	}
}

// TestPublishStopsOnCanceledContextBetweenWriteAndRename covers the same check
// one step later: the file is written, fsynced and closed, and only then is the
// context canceled — the rename must not happen and the temp file must go.
func TestPublishStopsOnCanceledContextBetweenWriteAndRename(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	path := filepath.Join(t.TempDir(), "state", "manifest.json")
	f := &failingFile{onSync: cancel}

	o, removed := opsFor(f, nil, nil)
	if err := publishWith(ctx, path, bytes.NewReader([]byte("demo")), Options{}, o); !errors.Is(err, context.Canceled) {
		t.Fatalf("publish = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a canceled publish wrote its target: %v", err)
	}
	if len(*removed) != 1 || (*removed)[0] != f.Name() {
		t.Fatalf("removed = %v, want the temp file %s", *removed, f.Name())
	}
}

// TestPublishStopsOnCanceledContextBetweenMkdirAndTemp covers the second context
// check: the destination directory was created, the context was canceled while
// that happened, and no temp file may be created at all.
func TestPublishStopsOnCanceledContextBetweenMkdirAndTemp(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	path := filepath.Join(t.TempDir(), "state", "manifest.json")
	f := &failingFile{}

	o, _ := opsFor(f, nil, nil)
	o.mkdirAll = func(dir string, mode os.FileMode) error {
		if err := os.MkdirAll(dir, mode); err != nil {
			return err
		}
		cancel()
		return nil
	}
	created := false
	o.createTemp = func(dir string, _ os.FileMode) (file, string, error) {
		created = true
		f.name = filepath.Join(dir, "manifest.json.tmp")
		return f, f.name, nil
	}
	if err := publishWith(ctx, path, bytes.NewReader([]byte("demo")), Options{}, o); !errors.Is(err, context.Canceled) {
		t.Fatalf("publish = %v, want context.Canceled", err)
	}
	if created {
		t.Fatal("publish created a temp file after the context was canceled")
	}
}

// TestPublishStopsOnCanceledContextBeforeTemp covers the context check that runs
// before the temp file exists: a context canceled while the destination
// directory is being created publishes nothing and leaves no scratch behind.
func TestPublishStopsOnCanceledContextBeforeTemp(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	path := filepath.Join(t.TempDir(), "state", "manifest.json")
	cancel()

	f := &failingFile{}
	o, _ := opsFor(f, nil, nil)
	created := false
	o.createTemp = func(dir string, _ os.FileMode) (file, string, error) {
		created = true
		f.name = filepath.Join(dir, "manifest.json.tmp")
		return f, f.name, nil
	}
	if err := publishWith(ctx, path, bytes.NewReader([]byte("demo")), Options{}, o); !errors.Is(err, context.Canceled) {
		t.Fatalf("publish with an early canceled context = %v, want context.Canceled", err)
	}
	if created {
		t.Fatal("publish created a temp file after the context was canceled")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a canceled publish wrote its target: %v", err)
	}
}

// TestPublishPublishesThroughRename is the success path of the seam: the
// written bytes reach the destination through the rename and the file is closed
// once.
func TestPublishPublishesThroughRename(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	f := &failingFile{}

	o, removed := opsFor(f, nil, nil)
	var target string
	o.rename = func(_, newpath string) error {
		target = newpath
		return nil
	}
	if err := publishWith(context.Background(), path, bytes.NewReader([]byte("demo")), Options{}, o); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if target != path {
		t.Fatalf("renamed target = %q, want %q", target, path)
	}
	if string(f.written) != "demo" {
		t.Fatalf("written = %q, want %q", f.written, "demo")
	}
	if f.closed != 1 {
		t.Fatalf("close calls = %d, want 1", f.closed)
	}
	if len(*removed) != 0 {
		t.Fatalf("a successful publish removed %v", *removed)
	}
}

// TestPublishExistingRegularFileIsSuccess pins the idempotent-rename rule: a
// rename that failed because the destination already holds a regular file is a
// successful publish, and the temp file is removed.
func TestPublishExistingRegularFileIsSuccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(path, []byte("already here"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &failingFile{}
	o, removed := opsFor(f, nil, errors.New("destination exists"))

	if err := publishWith(context.Background(), path, bytes.NewReader([]byte("demo")), Options{ExistingIsSuccess: true}, o); err != nil {
		t.Fatalf("publish over an existing regular file = %v, want nil", err)
	}
	if len(*removed) != 1 || (*removed)[0] != f.Name() {
		t.Fatalf("removed = %v, want the temp file %s", *removed, f.Name())
	}

	// Without the option the same rename failure is reported.
	f2 := &failingFile{}
	o2, _ := opsFor(f2, nil, errors.New("destination exists"))
	err := publishWith(context.Background(), path, bytes.NewReader([]byte("demo")), Options{}, o2)
	if phase, _, ok := FailedPhase(err); !ok || phase != PhasePublish {
		t.Fatalf("publish without ExistingIsSuccess = %v, want a PhasePublish error", err)
	}
}

// TestPublishExistingDirectoryStaysFailure pins the other half of the rule: a
// destination that is not a regular file is a real failure even with
// ExistingIsSuccess set.
func TestPublishExistingDirectoryStaysFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "occupied"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Publish(context.Background(), path, bytes.NewReader([]byte("demo")), Options{ExistingIsSuccess: true})
	if phase, _, ok := FailedPhase(err); !ok || phase != PhasePublish {
		t.Fatalf("publish onto a directory = %v, want a PhasePublish error", err)
	}
}

// TestPublishSyncDirPhase covers the durability option: the requested directory
// fsync runs after the rename, and its failure is reported as PhaseDirSync while
// the destination stays published.
func TestPublishSyncDirPhase(t *testing.T) {
	want := errors.New("directory fsync failed")
	for _, tc := range []struct {
		name    string
		syncErr error
		wantErr bool
	}{
		{"ok", nil, false},
		{"fails", want, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "manifest.json")
			f := &failingFile{}
			o, _ := opsFor(f, nil, nil)
			synced := 0
			o.syncDir = func(got string) error {
				synced++
				if got != path {
					t.Errorf("syncDir(%q), want %q", got, path)
				}
				return tc.syncErr
			}
			err := publishWith(context.Background(), path, bytes.NewReader([]byte("demo")),
				Options{SyncDir: true}, o)
			if synced != 1 {
				t.Fatalf("syncDir calls = %d, want 1", synced)
			}
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("publish = %v, want nil", err)
				}
				return
			}
			phase, cause, ok := FailedPhase(err)
			if !ok || phase != PhaseDirSync || !errors.Is(cause, want) {
				t.Fatalf("FailedPhase = (%v, %v, %v), want (%v, %v, true)", phase, cause, ok, PhaseDirSync, want)
			}
		})
	}
}

// TestPublishRealFile is the publish against a real filesystem: it creates the
// destination's parent directory, writes the exact bytes, and leaves no scratch
// file behind.
func TestPublishRealFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state", "manifest.json")
	if err := Publish(context.Background(), path, bytes.NewReader([]byte("demo")), Options{SyncDir: true}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "demo" {
		t.Fatalf("published %q, want %q", got, "demo")
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if IsTempFile(e.Name()) {
			t.Fatalf("a scratch file survived: %s", e.Name())
		}
	}
	if runtime.GOOS != "windows" {
		// The published file carries the mode Publish created the temp file
		// with, subject to the process umask — the rule every create follows —
		// so it is compared against a probe created the same way rather than
		// against a literal.
		probe := filepath.Join(dir, "mode-probe")
		pf, err := os.OpenFile(probe, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		probeInfo, err := pf.Stat()
		if err != nil {
			t.Fatal(err)
		}
		if err := pf.Close(); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm, want := info.Mode().Perm(), probeInfo.Mode().Perm(); perm != want {
			t.Fatalf("published mode = %o, want the created mode %o", perm, want)
		}
	}
}

// TestPublishReplacesExistingFile is the overwrite the callers rely on: a short
// publish fully replaces a longer destination, and the destination is never
// observed half-written (the temp file is a sibling, not the destination).
func TestPublishReplacesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := Publish(context.Background(), path, strings.NewReader(strings.Repeat("x", 4096)), Options{}); err != nil {
		t.Fatalf("first publish: %v", err)
	}
	if err := Publish(context.Background(), path, strings.NewReader("short"), Options{}); err != nil {
		t.Fatalf("second publish: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "short" {
		t.Fatalf("published %q, want the shorter replacement", got)
	}
}

// TestPublishMkdirFailure covers the destination directory phase against a real
// filesystem: a parent that is a regular file cannot hold the temp file.
func TestPublishMkdirFailure(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Publish(context.Background(), filepath.Join(file, "manifest.json"), bytes.NewReader(nil), Options{})
	phase, _, ok := FailedPhase(err)
	if !ok || phase != PhaseDir {
		t.Fatalf("publish under a regular file = %v, want a PhaseDir error", err)
	}
}

// TestPublishWriteFailureFromReader covers the write phase without a seam: a
// reader that fails mid-stream stops the publish and cleans the temp file up.
func TestPublishWriteFailureFromReader(t *testing.T) {
	want := errors.New("reader failed")
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")
	err := Publish(context.Background(), path, io.MultiReader(strings.NewReader("start"), errReader{want}), Options{})
	phase, cause, ok := FailedPhase(err)
	if !ok || phase != PhaseWrite || !errors.Is(cause, want) {
		t.Fatalf("publish with a failing reader = %v, want a PhaseWrite error wrapping %v", err, want)
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("a failed publish left %d entries behind", len(entries))
	}
}

// errReader fails every read with err.
type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

// TestCreateTempCollision verifies that when <path>.tmp already exists, the
// temp file falls back to <path>.tmp.1 — an existing name is never removed, so a
// concurrent writer's temp file is never destroyed.
func TestCreateTempCollision(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obj")
	base := path + ".tmp"
	if err := os.WriteFile(base, []byte("occupied"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, tmp, err := createTemp(path, 0o644)
	if err != nil {
		t.Fatalf("createTemp after collision: %v", err)
	}
	defer func() {
		_ = f.Close()
		_ = os.Remove(tmp)
	}()
	if tmp != base+".1" {
		t.Fatalf("createTemp returned %q, want %q", tmp, base+".1")
	}
	if got, err := os.ReadFile(base); err != nil || string(got) != "occupied" {
		t.Fatalf("the existing temp file was not left alone: (%q, %v)", got, err)
	}
}

// TestCreateTempNoParent verifies createTemp returns an error (not an ErrExist
// collision path) when the enclosing directory does not exist.
func TestCreateTempNoParent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "no-such-dir", "obj")
	if f, tmp, err := createTemp(p, 0o644); err == nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		t.Fatalf("createTemp in a missing dir succeeded (tmp=%q)", tmp)
	}
}

// TestCreateTempExhausted fills every candidate name and confirms the retry loop
// reports exhaustion instead of hanging or looping past its bound.
func TestCreateTempExhausted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obj")
	base := path + ".tmp"
	for i := range 10000 {
		name := base
		if i > 0 {
			name = base + "." + strconv.Itoa(i)
		}
		if err := os.WriteFile(name, nil, 0o644); err != nil {
			t.Fatalf("precreate %q: %v", name, err)
		}
	}
	if f, tmp, err := createTemp(path, 0o644); err == nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		t.Fatal("createTemp must report an exhausted temp namespace")
	}
}

// TestErrorNamesItsPhase pins the error text a caller wraps once: the phase
// name, then the underlying failure, with the cause still reachable for
// errors.Is. Every phase names itself, so a caller never needs a per-phase
// switch to tell an operator where a publish stopped.
func TestErrorNamesItsPhase(t *testing.T) {
	cause := errors.New("disk said no")
	cases := []struct {
		phase Phase
		want  string
	}{
		{PhaseDir, "create directory"},
		{PhaseTemp, "create temp file"},
		{PhaseWrite, "write temp file"},
		{PhaseSync, "sync temp file"},
		{PhaseClose, "close temp file"},
		{PhasePublish, "publish"},
		{PhaseDirSync, "sync directory"},
	}
	for _, tc := range cases {
		err := &Error{Phase: tc.phase, Err: cause}
		if got, want := err.Error(), tc.want+": disk said no"; got != want {
			t.Errorf("Error() = %q, want %q", got, want)
		}
		if !errors.Is(err, cause) {
			t.Errorf("Error(%v) does not unwrap to its cause", tc.phase)
		}
	}
	// The zero phase still renders a usable message rather than an empty one.
	if got := (&Error{Err: cause}).Error(); got == "" || got == cause.Error() {
		t.Errorf("Error() of the zero phase = %q, want a named phase", got)
	}
}

// TestFailedPhaseReportsForeignError covers the errors that are not a failed
// phase: a canceled context (Publish returns ctx.Err() itself) is reported
// unchanged, with ok false.
func TestFailedPhaseReportsForeignError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := Publish(ctx, filepath.Join(t.TempDir(), "x"), bytes.NewReader(nil), Options{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Publish(canceled) = %v, want context.Canceled", err)
	}
	phase, cause, ok := FailedPhase(err)
	if ok || phase != 0 || !errors.Is(cause, context.Canceled) {
		t.Fatalf("FailedPhase = (%v, %v, %v), want (0, context.Canceled, false)", phase, cause, ok)
	}
}

// TestSyncParentDirReportsUnopenableParent covers SyncParentDir's open failure:
// with no directory to fsync, the publish cannot claim durability and must
// report it. SyncParentDir is a documented no-op on Windows (a Windows
// directory cannot be opened for Sync), so the assertion is POSIX-only.
func TestSyncParentDirReportsUnopenableParent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SyncParentDir is a no-op on Windows")
	}
	if err := SyncParentDir(filepath.Join(t.TempDir(), "missing", "ref")); err == nil {
		t.Fatal("SyncParentDir with a missing parent = nil error, want error")
	}
	if err := SyncParentDir(filepath.Join(t.TempDir(), "ref")); err != nil {
		t.Fatalf("SyncParentDir of a real directory = %v, want nil", err)
	}
}

// TestIsTempFile pins the predicate the listing paths share: the temp names this
// package's publish leaves behind, and the names it must not claim.
func TestIsTempFile(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"abc123.tmp", true},
		{"abc123.tmp.1", true},
		{"abc123.tmp.9999", true},
		{"main.tmp", true},
		{"abc123", false},
		{"abc123.tmpfoo", false},
		{"abc123.tmp.", false},
		{"abc123.tmp.x", false},
		{"a.tmp.b.tmp", false},
	}
	for _, tc := range cases {
		if got := IsTempFile(tc.name); got != tc.want {
			t.Errorf("IsTempFile(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}
