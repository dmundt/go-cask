package fs

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestPutRefusesASymlinkedFanOutDirectory pins the write path's link refusal
// (cas-core §4.4, go-cask#352): a symbolic link planted where the next fan-out
// directory belongs is not followed. os.MkdirAll and the publishing rename both
// follow it, so without the refusal the object would be published outside the
// base the store owns — a write that crosses the one-base-one-store boundary.
//
// The assertion is on the link's target, not on the error text: nothing may be
// created there, and the planted link must still be a link.
func TestPutRefusesASymlinkedFanOutDirectory(t *testing.T) {
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), "store")
	s, err := New(base)
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()

	payload := []byte("published through a fan-out link")
	d := digestOf(payload)
	link := filepath.Join(base, d.String()[:DefaultFanOut])
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	err = s.Put(ctx, d, bytes.NewReader(payload))
	if !errors.Is(err, ErrUnsafeTarget) {
		t.Fatalf("Put through a symlinked fan-out directory = %v, want ErrUnsafeTarget", err)
	}
	entries, readErr := os.ReadDir(outside)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("Put published %d entries outside the base through the link: %v", len(entries), entries)
	}
	if fi, lstatErr := os.Lstat(link); lstatErr != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the planted fan-out link was replaced instead of refused: (%v, %v)", fi, lstatErr)
	}
	if ok, existsErr := s.Exists(ctx, d); existsErr != nil || ok {
		t.Fatalf("Exists() after a refused Put = (%v, %v), want (false, nil)", ok, existsErr)
	}
}

// TestPutRefusesASymlinkedObjectTarget pins the second half of the same rule: a
// link standing in for the object itself is refused before the temp file is
// created, so the write never truncates or replaces whatever it points at. The
// target's bytes are asserted byte for byte — a redirect that reached it would
// change them.
func TestPutRefusesASymlinkedObjectTarget(t *testing.T) {
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), "store")
	s, err := New(base)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "planted-target.bin")
	original := []byte("bytes the store must not touch")
	if err := os.WriteFile(target, original, 0o644); err != nil {
		t.Fatal(err)
	}

	payload := []byte("published over a link")
	d := digestOf(payload)
	objectPath := s.digestPath(d)
	if err := os.MkdirAll(filepath.Dir(objectPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, objectPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	err = s.Put(ctx, d, bytes.NewReader(payload))
	if !errors.Is(err, ErrUnsafeTarget) {
		t.Fatalf("Put over a symlinked object path = %v, want ErrUnsafeTarget", err)
	}
	after, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(after, original) {
		t.Fatalf("the link's target changed: %q, want %q", after, original)
	}
	if leftovers := tmpFilesIn(s, d); len(leftovers) != 0 {
		t.Fatalf("a refused Put left temp files behind: %v", leftovers)
	}
}

// TestValidateDirAndValidateFileRefuseLinksAndNonRegularEntries pins the policy
// helpers the write paths call: every component below base must be a real
// directory, and an entry at the path itself must be a regular file. A missing
// tail is accepted — nothing that is not there can be followed — while the base
// itself and anything outside it are refused, so a caller cannot use the helper
// to walk out of its own store.
func TestValidateDirAndValidateFileRefuseLinksAndNonRegularEntries(t *testing.T) {
	base := t.TempDir()
	nested := filepath.Join(base, "aa", "bb")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(nested, "object")
	if err := os.WriteFile(file, []byte("stored"), 0o644); err != nil {
		t.Fatal(err)
	}
	otherBase := t.TempDir()

	for _, tc := range []struct {
		name    string
		call    func() error
		wantErr bool
	}{
		{
			name: "existing directory chain",
			call: func() error { return ValidateDir(base, nested) },
		},
		{
			name: "missing directory tail",
			call: func() error { return ValidateDir(base, filepath.Join(nested, "absent", "deeper")) },
		},
		{
			name: "existing file",
			call: func() error { return ValidateFile(base, file) },
		},
		{
			name: "missing file tail",
			call: func() error { return ValidateFile(base, filepath.Join(nested, "absent")) },
		},
		{
			name:    "a file where a directory is expected",
			call:    func() error { return ValidateDir(base, file) },
			wantErr: true,
		},
		{
			// A directory at a file path is the caller's own failure to report:
			// the atomic rename cannot replace a directory, and Put already
			// reports that as a publish error (cas/verify/sidecar pins it).
			name: "a directory where a file is expected",
			call: func() error { return ValidateFile(base, nested) },
		},
		{
			name:    "the base itself",
			call:    func() error { return ValidateFile(base, base) },
			wantErr: true,
		},
		{
			name:    "above the base",
			call:    func() error { return ValidateFile(base, filepath.Join(base, "..", "elsewhere")) },
			wantErr: true,
		},
		{
			name:    "another base entirely",
			call:    func() error { return ValidateFile(base, filepath.Join(otherBase, "object")) },
			wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			switch {
			case tc.wantErr && !errors.Is(err, ErrUnsafeTarget):
				t.Fatalf("= %v, want ErrUnsafeTarget", err)
			case !tc.wantErr && err != nil:
				t.Fatalf("= %v, want nil", err)
			}
		})
	}

	t.Run("symlinked directory component", func(t *testing.T) {
		outside := t.TempDir()
		link := filepath.Join(base, "link")
		if err := os.Symlink(outside, link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		target := filepath.Join(link, "object")
		for _, call := range []func() error{
			func() error { return ValidateDir(base, link) },
			func() error { return ValidateFile(base, target) },
		} {
			err := call()
			if !errors.Is(err, ErrUnsafeTarget) {
				t.Fatalf("validate through a symlinked component = %v, want ErrUnsafeTarget", err)
			}
			if err == nil || !bytes.Contains([]byte(err.Error()), []byte(outside)) {
				t.Fatalf("error %v does not name the link's target %s", err, outside)
			}
		}
	})

	t.Run("symlinked file", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "planted.bin")
		if err := os.WriteFile(target, []byte("elsewhere"), 0o644); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(nested, "linked-object")
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if err := ValidateFile(base, link); !errors.Is(err, ErrUnsafeTarget) {
			t.Fatalf("ValidateFile over a symlink = %v, want ErrUnsafeTarget", err)
		}
	})
}
