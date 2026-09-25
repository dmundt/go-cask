package store

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dmundt/go-cask/cas"
	backmem "github.com/dmundt/go-cask/cas/backend/mem"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

// selectableKinds are the backends -backend accepts: every one of them must
// support the full capability set the CLI consults.
var selectableKinds = []Kind{KindFS, KindPackFS}

// TestParseKind pins the accepted -backend values and the documented default.
func TestParseKind(t *testing.T) {
	for _, tc := range []struct {
		name string
		want Kind
		bad  bool
	}{
		{name: "", want: DefaultKind},
		{name: "fs", want: KindFS},
		{name: "packfs", want: KindPackFS},
		{name: "sqlite", bad: true},
		{name: "FS", bad: true},
	} {
		got, err := ParseKind(tc.name)
		if tc.bad {
			if err == nil {
				t.Errorf("ParseKind(%q) error = nil, want a usage error", tc.name)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("ParseKind(%q) = (%q, %v), want %q", tc.name, got, err, tc.want)
		}
	}
}

// TestOpenSelectableBackends: every -backend value opens a working store whose
// full maintenance capability set is reported, and Close is idempotent.
func TestOpenSelectableBackends(t *testing.T) {
	ctx := context.Background()
	for _, kind := range selectableKinds {
		t.Run(string(kind), func(t *testing.T) {
			st, err := Open(ctx, Options{Kind: kind, Path: t.TempDir()})
			if err != nil {
				t.Fatalf("Open(%q): %v", kind, err)
			}
			defer st.Close()
			if st.Kind != kind {
				t.Fatalf("Store.Kind = %q, want %q", st.Kind, kind)
			}
			want := cas.Capabilities{Verify: true, Sweep: true, Clean: true, Stat: true}
			if st.Capabilities != want {
				t.Fatalf("Capabilities = %+v, want %+v", st.Capabilities, want)
			}

			payload := []byte("opened through the shared constructor")
			digest := sha256.Of(payload)
			if err := st.Put(ctx, digest, bytes.NewReader(payload)); err != nil {
				t.Fatalf("Put: %v", err)
			}
			size, err := st.Size(ctx, digest)
			if err != nil || size != int64(len(payload)) {
				t.Fatalf("Size = (%d, %v), want %d", size, err, len(payload))
			}
			if modified, err := st.ModTime(ctx, digest); err != nil || modified.IsZero() {
				t.Fatalf("ModTime = (%v, %v), want a real timestamp", modified, err)
			}
			if err := st.Verify(ctx, digest, sha256.New()); err != nil {
				t.Fatalf("Verify: %v", err)
			}
			report, err := st.VerifyAll(ctx, sha256.New())
			if err != nil || report.Checked != 1 || len(report.Bad) != 0 {
				t.Fatalf("VerifyAll = (%+v, %v), want one clean object", report, err)
			}
			if err := st.Close(); err != nil {
				t.Fatalf("second Close: %v", err)
			}
		})
	}
}

// TestOpenRejectsBadOptions: a missing path and an unknown kind never reach a
// backend, and a canceled context is honored.
func TestOpenRejectsBadOptions(t *testing.T) {
	ctx := context.Background()
	if _, err := Open(ctx, Options{Kind: KindFS}); err == nil {
		t.Fatal("Open with an empty path succeeded")
	}
	if _, err := Open(ctx, Options{Kind: Kind("sqlite"), Path: t.TempDir()}); err == nil {
		t.Fatal("Open with an unknown kind succeeded")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := Open(canceled, Options{Kind: KindFS, Path: t.TempDir()}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Open with a canceled context = %v, want context.Canceled", err)
	}
}

// TestOpenViewer: the viewer opens the loose filesystem backend, and a backend
// with no filesystem view is refused with an error naming the operation, the
// backend and the remedy (cli.md §1, §2).
func TestOpenViewer(t *testing.T) {
	ctx := context.Background()
	backend, err := OpenViewer(ctx, Options{Kind: KindFS, Path: t.TempDir()})
	if err != nil || backend == nil {
		t.Fatalf("OpenViewer(fs) = (%v, %v), want a backend", backend, err)
	}
	if backend, err := OpenViewer(ctx, Options{Path: t.TempDir()}); err != nil || backend == nil {
		t.Fatalf("OpenViewer(default) = (%v, %v), want the fs backend", backend, err)
	}
	if _, err := OpenViewer(ctx, Options{Kind: KindPackFS, Path: t.TempDir()}); !errors.Is(err, cas.ErrUnsupported) {
		t.Fatalf("OpenViewer(packfs) = %v, want ErrUnsupported", err)
	} else if !strings.Contains(err.Error(), "web") || !strings.Contains(err.Error(), string(KindPackFS)) {
		t.Fatalf("OpenViewer(packfs) = %v, want it to name the operation and the backend", err)
	} else if !strings.Contains(err.Error(), "-backend "+string(KindFS)) {
		t.Fatalf("OpenViewer(packfs) = %v, want it to name the remedy (%s)", err, KindFS)
	}
	if _, err := OpenViewer(ctx, Options{Kind: Kind("sqlite"), Path: t.TempDir()}); err == nil {
		t.Fatal("OpenViewer with an unknown kind succeeded")
	}
	if _, err := OpenViewer(ctx, Options{Kind: KindFS}); err == nil {
		t.Fatal("OpenViewer with an empty path succeeded")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := OpenViewer(canceled, Options{Kind: KindFS, Path: t.TempDir()}); !errors.Is(err, context.Canceled) {
		t.Fatalf("OpenViewer with a canceled context = %v, want context.Canceled", err)
	}
}

// TestVerifyAllReportsCorruption: the portable sweep reports a digest whose
// stored bytes no longer match its address, on every selectable backend.
func TestVerifyAllReportsCorruption(t *testing.T) {
	ctx := context.Background()
	for _, kind := range selectableKinds {
		t.Run(string(kind), func(t *testing.T) {
			st, err := Open(ctx, Options{Kind: kind, Path: t.TempDir()})
			if err != nil {
				t.Fatalf("Open(%q): %v", kind, err)
			}
			defer st.Close()

			good := []byte("intact")
			goodDigest := sha256.Of(good)
			if err := st.Put(ctx, goodDigest, bytes.NewReader(good)); err != nil {
				t.Fatalf("Put: %v", err)
			}
			corrupt := []byte("tampered")
			corruptDigest := sha256.Of([]byte("the original bytes"))
			if err := st.Put(ctx, corruptDigest, bytes.NewReader(corrupt)); err != nil {
				t.Fatalf("Put corrupt: %v", err)
			}

			if err := st.Verify(ctx, corruptDigest, sha256.New()); !errors.Is(err, cas.ErrDigestMismatch) {
				t.Fatalf("Verify(corrupt) = %v, want ErrDigestMismatch", err)
			}
			report, err := st.VerifyAll(ctx, sha256.New())
			if err != nil {
				t.Fatalf("VerifyAll: %v", err)
			}
			if report.Checked != 2 || len(report.Bad) != 1 || !report.Bad[0].Equal(corruptDigest) {
				t.Fatalf("VerifyAll = %+v, want 2 checked and %s bad", report, corruptDigest)
			}
		})
	}
}

// TestSweepOnSelectableBackends: mark-and-sweep reclaims exactly the digests
// absent from the reachable set, through the backend's native Prune (fs) or the
// portable cas.Sweep (packfs), and honors the dry run.
func TestSweepOnSelectableBackends(t *testing.T) {
	ctx := context.Background()
	for _, kind := range selectableKinds {
		t.Run(string(kind), func(t *testing.T) {
			st, err := Open(ctx, Options{Kind: kind, Path: t.TempDir()})
			if err != nil {
				t.Fatalf("Open(%q): %v", kind, err)
			}
			defer st.Close()

			keep := []byte("keep")
			drop := []byte("drop")
			keepDigest, dropDigest := sha256.Of(keep), sha256.Of(drop)
			for _, object := range []struct {
				digest  cas.Digest
				payload []byte
			}{{keepDigest, keep}, {dropDigest, drop}} {
				if err := st.Put(ctx, object.digest, bytes.NewReader(object.payload)); err != nil {
					t.Fatalf("Put: %v", err)
				}
			}
			reachable := map[string]bool{keepDigest.String(): true}

			doomed, err := st.Sweep(ctx, "gc", reachable, 0, true)
			if err != nil {
				t.Fatalf("Sweep dry run: %v", err)
			}
			if len(doomed) != 1 || !doomed[0].Equal(dropDigest) {
				t.Fatalf("Sweep dry run = %v, want [%s]", doomed, dropDigest)
			}
			if ok, err := st.Exists(ctx, dropDigest); err != nil || !ok {
				t.Fatalf("dry run deleted %s: exists = (%v, %v)", dropDigest, ok, err)
			}

			doomed, err = st.Sweep(ctx, "prune", reachable, 0, false)
			if err != nil {
				t.Fatalf("Sweep: %v", err)
			}
			if len(doomed) != 1 || !doomed[0].Equal(dropDigest) {
				t.Fatalf("Sweep = %v, want [%s]", doomed, dropDigest)
			}
			if ok, err := st.Exists(ctx, dropDigest); err != nil || ok {
				t.Fatalf("swept object %s still exists: (%v, %v)", dropDigest, ok, err)
			}
			if ok, err := st.Exists(ctx, keepDigest); err != nil || !ok {
				t.Fatalf("reachable object %s did not survive: (%v, %v)", keepDigest, ok, err)
			}
		})
	}
}

// TestCleanOnSelectableBackends: clean reclaims each backend's own orphaned
// scratch files.
func TestCleanOnSelectableBackends(t *testing.T) {
	ctx := context.Background()
	for _, kind := range selectableKinds {
		t.Run(string(kind), func(t *testing.T) {
			dir := t.TempDir()
			st, err := Open(ctx, Options{Kind: kind, Path: dir})
			if err != nil {
				t.Fatalf("Open(%q): %v", kind, err)
			}
			defer st.Close()

			if removed, err := st.Clean(ctx, 0); err != nil || removed != 0 {
				t.Fatalf("Clean on a fresh store = (%d, %v), want (0, nil)", removed, err)
			}
			// A crash leftover that each backend owns: a loose-object temp
			// file, plus one in the pack directory for packfs (the pack
			// backend's loose tree lives under its own base).
			stale := []string{filepath.Join(dir, "leftover.tmp")}
			if kind == KindPackFS {
				stale = []string{
					filepath.Join(dir, "loose", "leftover.tmp"),
					filepath.Join(dir, "packs", "leftover.tmp"),
				}
			}
			for _, path := range stale {
				if err := os.WriteFile(path, []byte("scratch"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			removed, err := st.Clean(ctx, 0)
			if err != nil || removed != len(stale) {
				t.Fatalf("Clean = (%d, %v), want (%d, nil)", removed, err, len(stale))
			}
			for _, path := range stale {
				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("%s survived Clean: %v", path, err)
				}
			}
			// A grace period keeps a fresh leftover in place.
			fresh := filepath.Join(dir, "fresh.tmp")
			if err := os.WriteFile(fresh, []byte("scratch"), 0o644); err != nil {
				t.Fatal(err)
			}
			if removed, err := st.Clean(ctx, time.Hour); err != nil || removed != 0 {
				t.Fatalf("Clean with a grace period = (%d, %v), want (0, nil)", removed, err)
			}
		})
	}
}

// TestUnsupportedNamesOperationAndBackend: an operation the backend cannot
// perform fails with ErrUnsupported naming both, instead of silently doing
// nothing or reporting success. The in-memory backend keeps no scratch state
// and has no physical metadata, so it exercises every unsupported branch even
// though the CLI does not expose it.
func TestUnsupportedNamesOperationAndBackend(t *testing.T) {
	ctx := context.Background()
	st := &Store{Backend: backmem.New(), Kind: Kind("memory")}
	object := sha256.Of([]byte("unsupported"))

	for _, tc := range []struct {
		name string
		op   string
		run  func() error
	}{
		{name: "size", op: "size", run: func() error { _, err := st.Size(ctx, object); return err }},
		{name: "mod time", op: "mod time", run: func() error { _, err := st.ModTime(ctx, object); return err }},
		{name: "clean", op: "clean", run: func() error { _, err := st.Clean(ctx, time.Hour); return err }},
		{name: "age-based gc", op: "gc", run: func() error {
			_, err := st.Sweep(ctx, "gc", map[string]bool{}, time.Hour, false)
			return err
		}},
		{name: "age-based prune", op: "prune", run: func() error {
			_, err := st.Sweep(ctx, "prune", map[string]bool{}, time.Hour, false)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			if !errors.Is(err, cas.ErrUnsupported) {
				t.Fatalf("%s = %v, want ErrUnsupported", tc.name, err)
			}
			if !strings.Contains(err.Error(), tc.op) || !strings.Contains(err.Error(), "memory") {
				t.Fatalf("%s = %v, want it to name %q and the backend", tc.name, err, tc.op)
			}
		})
	}

	// An unconditional sweep needs nothing but List/Delete, so it still works.
	if _, err := st.Sweep(ctx, "gc", map[string]bool{}, 0, false); err != nil {
		t.Fatalf("unconditional Sweep = %v, want a portable sweep", err)
	}
}

// closingBackend records the Close calls the store must make exactly once.
type closingBackend struct {
	cas.Backend
	closes int
	err    error
}

// Close records the call and returns the configured error.
func (b *closingBackend) Close() error {
	b.closes++
	return b.err
}

// TestStoreClose: Close releases a backend that holds resources, is idempotent,
// returns the backend's error, and tolerates a backend (or store) that holds
// nothing.
func TestStoreClose(t *testing.T) {
	backend := &closingBackend{Backend: backmem.New(), err: errors.New("close exploded")}
	st := &Store{Backend: backend}
	for range 3 {
		if err := st.Close(); err == nil || err.Error() != "close exploded" {
			t.Fatalf("Close = %v, want the backend's close error", err)
		}
	}
	if backend.closes != 1 {
		t.Fatalf("backend closed %d times, want exactly 1", backend.closes)
	}

	// fs and mem hold no resources: Close reports nil for them.
	opened, err := Open(context.Background(), Options{Kind: KindFS, Path: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := opened.Close(); err != nil {
		t.Fatalf("Close(fs) = %v, want nil", err)
	}

	var nilStore *Store
	if err := nilStore.Close(); err != nil {
		t.Fatalf("nil Store.Close = %v, want nil", err)
	}
	if err := (&Store{}).Close(); err != nil {
		t.Fatalf("empty Store.Close = %v, want nil", err)
	}
}

// TestStoreEmbedsBackend: the store is itself a cas.Backend, so callers can pass
// it wherever the minimal contract is expected.
func TestStoreEmbedsBackend(t *testing.T) {
	var _ cas.Backend = (*Store)(nil)
	var _ cas.Statter = (*Store)(nil)
	var _ cas.Cleaner = (*Store)(nil)
	var _ io.Closer = (*Store)(nil)
}
