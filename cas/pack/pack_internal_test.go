package pack

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// failingFile is a tempFile whose every write step can be made to fail, so each
// error branch of writeFileAtomic is covered without asking a real filesystem to
// fail on demand.
type failingFile struct {
	writeErr error
	chmodErr error
	syncErr  error
	closeErr error

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

func (f *failingFile) Chmod(os.FileMode) error { return f.chmodErr }
func (f *failingFile) Sync() error             { return f.syncErr }

func (f *failingFile) Close() error {
	f.closed++
	return f.closeErr
}

// opsFor builds the writeFileAtomic seam around f, with the given failures.
// remove is a no-op and rename always succeeds unless a test overrides them.
func opsFor(f *failingFile, createErr, renameErr error) fileOps {
	return fileOps{
		createTemp: func(dir, _ string) (tempFile, error) {
			if createErr != nil {
				return nil, createErr
			}
			f.name = filepath.Join(dir, "manifest.json.tmp")
			return f, nil
		},
		remove: func(string) error { return nil },
		rename: func(_, _ string) error { return renameErr },
	}
}

// TestWriteFileAtomicFailureBranches covers every publish step: a failure at any
// one of them reports an error, writes nothing at the target, and closes the
// temp file (#256).
func TestWriteFileAtomicFailureBranches(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state", "manifest.json")
	want := errors.New("step failed")

	cases := []struct {
		name      string
		file      *failingFile
		createErr error
		renameErr error
	}{
		{"create temp", &failingFile{}, want, nil},
		{"write", &failingFile{writeErr: want}, nil, nil},
		{"chmod", &failingFile{chmodErr: want}, nil, nil},
		{"sync", &failingFile{syncErr: want}, nil, nil},
		{"close", &failingFile{closeErr: want}, nil, nil},
		{"rename", &failingFile{}, nil, want},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := writeFileAtomic(ctx, path, []byte("demo"), opsFor(tc.file, tc.createErr, tc.renameErr))
			if !errors.Is(err, want) {
				t.Fatalf("writeFileAtomic = %v, want %v", err, want)
			}
			if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("a failed publish wrote its target: %v", statErr)
			}
		})
	}
}

// TestWriteFileAtomicRemovesTempOnFailure pins the cleanup itself: the temp file
// the write created is removed on failure.
func TestWriteFileAtomicRemovesTempOnFailure(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state", "manifest.json")
	file := &failingFile{syncErr: errors.New("sync failed")}

	ops := opsFor(file, nil, nil)
	var removed []string
	ops.remove = func(name string) error {
		removed = append(removed, name)
		return nil
	}
	if err := writeFileAtomic(ctx, path, []byte("demo"), ops); err == nil {
		t.Fatal("writeFileAtomic reported success")
	}
	if len(removed) != 1 || removed[0] != file.Name() {
		t.Fatalf("removed = %v, want the temp file %s", removed, file.Name())
	}
	if file.closed == 0 {
		t.Fatal("the failed write left the temp file open")
	}
}

// TestWriteFileAtomicStopsOnCanceledContextAfterWrite is the context check that
// runs after the bytes are on disk: a context canceled while the manifest is
// being written must not publish it.
func TestWriteFileAtomicStopsOnCanceledContextAfterWrite(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	path := filepath.Join(t.TempDir(), "state", "manifest.json")
	file := &failingFile{}

	ops := opsFor(file, nil, nil)
	ops.createTemp = func(dir, _ string) (tempFile, error) {
		file.name = filepath.Join(dir, "manifest.json.tmp")
		cancel() // canceled while the temp file exists, before the rename
		return file, nil
	}
	if err := writeFileAtomic(ctx, path, []byte("demo"), ops); !errors.Is(err, context.Canceled) {
		t.Fatalf("writeFileAtomic = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a canceled publish wrote its target: %v", err)
	}
}

// TestWriteFileAtomicStopsOnCanceledContextBeforeTemp covers the context check
// that runs before the temp file exists: a context canceled while the manifest
// directory is being created must publish nothing and leave no scratch behind.
func TestWriteFileAtomicStopsOnCanceledContextBeforeTemp(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	path := filepath.Join(t.TempDir(), "state", "manifest.json")
	cancel()

	file := &failingFile{}
	ops := opsFor(file, nil, nil)
	created := false
	ops.createTemp = func(dir, _ string) (tempFile, error) {
		created = true
		file.name = filepath.Join(dir, "manifest.json.tmp")
		return file, nil
	}
	if err := writeFileAtomic(ctx, path, []byte("demo"), ops); !errors.Is(err, context.Canceled) {
		t.Fatalf("writeFileAtomic with an early canceled context = %v, want context.Canceled", err)
	}
	if created {
		t.Fatal("writeFileAtomic created a temp file after the context was canceled")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a canceled publish wrote its target: %v", err)
	}
}

// TestWriteFileAtomicPublishesThroughRename is the success path of the seam: the
// written bytes reach the target through the rename and the file is closed once.
func TestWriteFileAtomicPublishesThroughRename(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "manifest.json")
	file := &failingFile{}

	ops := opsFor(file, nil, nil)
	var target string
	ops.rename = func(_, newpath string) error {
		target = newpath
		return nil
	}
	if err := writeFileAtomic(ctx, path, []byte("demo"), ops); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}
	if target != path {
		t.Fatalf("renamed target = %q, want %q", target, path)
	}
	if string(file.written) != "demo" {
		t.Fatalf("written = %q, want %q", file.written, "demo")
	}
	if file.closed != 1 {
		t.Fatalf("close calls = %d, want 1", file.closed)
	}
}
