package backend_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"testing"

	"github.com/dmundt/go-cask/cas"
	fsbackend "github.com/dmundt/go-cask/cas/backend/fs"
	memory "github.com/dmundt/go-cask/cas/backend/mem"
	"github.com/dmundt/go-cask/cas/backend/packfs"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

// backendCase is one Backend implementation under conformance test. open builds
// a fresh backend over dir; reopen builds a second one over the same directory
// and is nil when the backend keeps no state to reopen (the in-memory
// reference implementation).
type backendCase struct {
	name   string
	open   func(t *testing.T, dir string) cas.Backend
	reopen func(t *testing.T, dir string) cas.Backend
}

// conformanceBackends is the suite's backend matrix: the loose filesystem
// backend, the pack backend with packing both on and off, and the in-memory
// reference implementation. Every backend promises the same guarantees
// (cas/backend.go), so the same assertions run over all of them.
func conformanceBackends() []backendCase {
	fsCase := backendCase{
		name: "fs",
		open: func(t *testing.T, dir string) cas.Backend {
			t.Helper()
			backend, err := fsbackend.New(dir)
			if err != nil {
				t.Fatalf("fs.New(%q): %v", dir, err)
			}
			return backend
		},
	}
	fsCase.reopen = fsCase.open

	packedCase := backendCase{
		name: "packfs",
		open: func(t *testing.T, dir string) cas.Backend {
			t.Helper()
			backend, err := packfs.New(dir, packfs.WithEnabled())
			if err != nil {
				t.Fatalf("packfs.New(%q): %v", dir, err)
			}
			return backend
		},
	}
	packedCase.reopen = packedCase.open

	looseCase := backendCase{
		name: "packfs-unpacked",
		open: func(t *testing.T, dir string) cas.Backend {
			t.Helper()
			backend, err := packfs.New(dir)
			if err != nil {
				t.Fatalf("packfs.New(%q): %v", dir, err)
			}
			return backend
		},
	}
	looseCase.reopen = looseCase.open

	return []backendCase{
		fsCase,
		packedCase,
		looseCase,
		{
			name: "memory",
			open: func(t *testing.T, dir string) cas.Backend {
				t.Helper()
				return memory.New()
			},
		},
	}
}

// conformancePayloads are the objects every check stores: the empty payload, a
// short one, and one large enough to span several fan-out directories and pack
// records.
func conformancePayloads() [][]byte {
	return [][]byte{
		{},
		[]byte("cask"),
		bytes.Repeat([]byte("0123456789abcdef"), 4096),
	}
}

// putObject stores payload under its own digest and returns that digest.
func putObject(t *testing.T, backend cas.Backend, payload []byte) cas.Digest {
	t.Helper()
	d := sha256.Of(payload)
	if err := backend.Put(context.Background(), d, bytes.NewReader(payload)); err != nil {
		t.Fatalf("Put(%s): %v", d, err)
	}
	return d
}

// mustRead requires the stored object at d to read back as exactly want.
func mustRead(t *testing.T, backend cas.Backend, d cas.Digest, want []byte) {
	t.Helper()
	rc, err := backend.Get(context.Background(), d)
	if err != nil {
		t.Fatalf("Get(%s): %v", d, err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		_ = rc.Close()
		t.Fatalf("read %s: %v", d, err)
	}
	if err := rc.Close(); err != nil {
		t.Fatalf("close %s: %v", d, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("Get(%s) = %d bytes, want %d bytes", d, len(got), len(want))
	}
}

// closeBackend releases a backend that holds an open handle (packfs keeps its
// active pack file open until Close).
func closeBackend(t *testing.T, backend cas.Backend) {
	t.Helper()
	closer, ok := backend.(io.Closer)
	if !ok {
		return
	}
	if err := closer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// digestKeys renders digests as a sorted key set, so two List results compare
// equal regardless of each backend's own ordering.
func digestKeys(digests []cas.Digest) []string {
	keys := make([]string, 0, len(digests))
	for _, d := range digests {
		keys = append(keys, d.String())
	}
	slices.Sort(keys)
	return keys
}

// TestBackendConformance asserts the cas.Backend guarantees over every backend
// in conformanceBackends: an object reads back immediately after Put, a
// repeated Put is idempotent, List reports every stored digest exactly once,
// Stats totals match what was stored, and a missing object follows the
// ErrNotFound/no-op contract.
func TestBackendConformance(t *testing.T) {
	for _, bc := range conformanceBackends() {
		t.Run(bc.name, func(t *testing.T) {
			t.Run("immediate read-back", func(t *testing.T) {
				ctx := context.Background()
				backend := bc.open(t, t.TempDir())
				defer closeBackend(t, backend)
				for _, payload := range conformancePayloads() {
					d := putObject(t, backend, payload)
					exists, err := backend.Exists(ctx, d)
					if err != nil {
						t.Fatalf("Exists(%s): %v", d, err)
					}
					if !exists {
						t.Fatalf("Exists(%s) = false immediately after Put", d)
					}
					mustRead(t, backend, d, payload)
				}
			})

			t.Run("idempotent put", func(t *testing.T) {
				ctx := context.Background()
				backend := bc.open(t, t.TempDir())
				defer closeBackend(t, backend)
				payload := []byte("idempotent put")
				d := sha256.Of(payload)
				for range 3 {
					putObject(t, backend, payload)
				}
				mustRead(t, backend, d, payload)

				list, err := backend.List(ctx)
				if err != nil {
					t.Fatalf("List: %v", err)
				}
				if len(list) != 1 || !list[0].Equal(d) {
					t.Fatalf("List after three identical Puts = %v, want exactly [%s]", list, d)
				}
				stats, err := backend.Stats(ctx)
				if err != nil {
					t.Fatalf("Stats: %v", err)
				}
				if stats.ObjectCount != 1 || stats.TotalSize != int64(len(payload)) {
					t.Fatalf("Stats after three identical Puts = %+v, want 1 object of %d bytes", stats, len(payload))
				}
			})

			t.Run("list completeness", func(t *testing.T) {
				ctx := context.Background()
				backend := bc.open(t, t.TempDir())
				defer closeBackend(t, backend)
				want := map[string]bool{}
				for _, payload := range conformancePayloads() {
					want[putObject(t, backend, payload).String()] = true
				}
				list, err := backend.List(ctx)
				if err != nil {
					t.Fatalf("List: %v", err)
				}
				if len(list) != len(want) {
					t.Fatalf("List returned %d digests, want %d", len(list), len(want))
				}
				seen := map[string]int{}
				for _, d := range list {
					seen[d.String()]++
				}
				for key := range want {
					if seen[key] != 1 {
						t.Errorf("List reports %s %d times, want exactly once", key, seen[key])
					}
				}
			})

			t.Run("delete removes and missing delete is a no-op", func(t *testing.T) {
				ctx := context.Background()
				backend := bc.open(t, t.TempDir())
				defer closeBackend(t, backend)
				payloads := conformancePayloads()
				digests := make([]cas.Digest, 0, len(payloads))
				for _, payload := range payloads {
					digests = append(digests, putObject(t, backend, payload))
				}

				if err := backend.Delete(ctx, digests[0]); err != nil {
					t.Fatalf("Delete(%s): %v", digests[0], err)
				}
				exists, err := backend.Exists(ctx, digests[0])
				if err != nil {
					t.Fatalf("Exists(%s): %v", digests[0], err)
				}
				if exists {
					t.Fatalf("Exists(%s) = true after Delete", digests[0])
				}
				list, err := backend.List(ctx)
				if err != nil {
					t.Fatalf("List: %v", err)
				}
				if len(list) != len(digests)-1 {
					t.Fatalf("List returned %d digests after one Delete, want %d", len(list), len(digests)-1)
				}
				for _, key := range digestKeys(list) {
					if key == digests[0].String() {
						t.Fatalf("List still reports the deleted digest %s", digests[0])
					}
				}

				// Delete of a digest that is not stored is a documented no-op.
				if err := backend.Delete(ctx, digests[0]); err != nil {
					t.Fatalf("Delete of a missing object = %v, want nil", err)
				}
			})

			t.Run("stats totals", func(t *testing.T) {
				ctx := context.Background()
				backend := bc.open(t, t.TempDir())
				defer closeBackend(t, backend)
				var total int64
				payloads := conformancePayloads()
				for _, payload := range payloads {
					putObject(t, backend, payload)
					total += int64(len(payload))
				}
				stats, err := backend.Stats(ctx)
				if err != nil {
					t.Fatalf("Stats: %v", err)
				}
				if stats.ObjectCount != int64(len(payloads)) || stats.TotalSize != total {
					t.Fatalf("Stats = %+v, want %d objects and %d bytes", stats, len(payloads), total)
				}
			})

			t.Run("missing object contract", func(t *testing.T) {
				ctx := context.Background()
				backend := bc.open(t, t.TempDir())
				defer closeBackend(t, backend)
				missing := sha256.Of([]byte("never stored in this backend"))

				exists, err := backend.Exists(ctx, missing)
				if err != nil {
					t.Fatalf("Exists(%s): %v", missing, err)
				}
				if exists {
					t.Fatalf("Exists(%s) = true for an object that was never stored", missing)
				}
				if _, err := backend.Get(ctx, missing); !errors.Is(err, cas.ErrNotFound) {
					t.Fatalf("Get(%s) error = %v, want ErrNotFound", missing, err)
				}
				if err := backend.Delete(ctx, missing); err != nil {
					t.Fatalf("Delete(%s) = %v, want a no-op", missing, err)
				}
			})

			t.Run("identical list after reopen", func(t *testing.T) {
				if bc.reopen == nil {
					t.Skip("backend keeps no state to reopen")
				}
				ctx := context.Background()
				dir := t.TempDir()
				payloads := conformancePayloads()
				backend := bc.open(t, dir)
				for _, payload := range payloads {
					putObject(t, backend, payload)
				}
				before, err := backend.List(ctx)
				if err != nil {
					t.Fatalf("List: %v", err)
				}
				closeBackend(t, backend)

				reopened := bc.reopen(t, dir)
				defer closeBackend(t, reopened)
				after, err := reopened.List(ctx)
				if err != nil {
					t.Fatalf("List after reopen: %v", err)
				}
				if !slices.Equal(digestKeys(before), digestKeys(after)) {
					t.Fatalf("List changed across reopen: %v, want %v", digestKeys(after), digestKeys(before))
				}
				for _, payload := range payloads {
					mustRead(t, reopened, sha256.Of(payload), payload)
				}
			})
		})
	}
}
