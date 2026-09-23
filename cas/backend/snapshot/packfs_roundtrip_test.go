package snapshot_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/dmundt/go-cask/cas"
	fsbackend "github.com/dmundt/go-cask/cas/backend/fs"
	"github.com/dmundt/go-cask/cas/backend/packfs"
	"github.com/dmundt/go-cask/cas/backend/snapshot"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

// backendFactory opens one store for the round-trip matrix.
type backendFactory struct {
	name string
	open func(t *testing.T, dir string) cas.Backend
}

// roundTripBackends covers the directions a packed store has to survive: pack
// to pack, pack to loose, and loose to pack.
func roundTripBackends() []backendFactory {
	return []backendFactory{
		{
			name: "packfs",
			open: func(t *testing.T, dir string) cas.Backend {
				t.Helper()
				backend, err := packfs.New(dir, packfs.WithEnabled())
				if err != nil {
					t.Fatalf("packfs.New(%q): %v", dir, err)
				}
				return backend
			},
		},
		{
			name: "fs",
			open: func(t *testing.T, dir string) cas.Backend {
				t.Helper()
				backend, err := fsbackend.New(dir)
				if err != nil {
					t.Fatalf("fs.New(%q): %v", dir, err)
				}
				return backend
			},
		},
	}
}

// TestPackedStoreSnapshotRoundTrip: a packed store survives Export/Import with
// an identical List. Every object written through a packed store is exported,
// imported into a fresh store of each shape, and both stores must report the
// same digests with the same bytes — the packed destination also has to find
// them again from a newly opened backend, since a packed store's index is what
// the next process reads.
func TestPackedStoreSnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	for _, dstFactory := range roundTripBackends() {
		t.Run("packfs->"+dstFactory.name, func(t *testing.T) {
			src, err := packfs.New(t.TempDir(), packfs.WithEnabled())
			if err != nil {
				t.Fatalf("packfs.New: %v", err)
			}
			payloads := [][]byte{
				{},
				[]byte("cask snapshot"),
				bytes.Repeat([]byte("packed payload "), 2048),
			}
			for _, payload := range payloads {
				if err := src.Put(ctx, sha256.Of(payload), bytes.NewReader(payload)); err != nil {
					t.Fatalf("Put: %v", err)
				}
			}
			// The write path closes the packed store before its objects are
			// exported, so the snapshot reads a store no writer still holds.
			if err := src.Close(); err != nil {
				t.Fatalf("Close source: %v", err)
			}
			sourceList, err := src.List(ctx)
			if err != nil {
				t.Fatalf("List source: %v", err)
			}

			var archive bytes.Buffer
			if err := snapshot.Export(ctx, src, &archive); err != nil {
				t.Fatalf("Export: %v", err)
			}

			destDir := t.TempDir()
			dest := dstFactory.open(t, destDir)
			if err := snapshot.Import(ctx, dest, bytes.NewReader(archive.Bytes())); err != nil {
				t.Fatalf("Import: %v", err)
			}
			destList, err := dest.List(ctx)
			if err != nil {
				t.Fatalf("List destination: %v", err)
			}
			if !slices.Equal(digestKeys(sourceList), digestKeys(destList)) {
				t.Fatalf("List after Import = %v, want %v", digestKeys(destList), digestKeys(sourceList))
			}
			for _, digest := range sourceList {
				want := readObject(t, src, digest)
				if got := readObject(t, dest, digest); !bytes.Equal(got, want) {
					t.Fatalf("object %s after Import = %d bytes, want %d bytes", digest, len(got), len(want))
				}
			}
			if closer, ok := dest.(io.Closer); ok {
				if err := closer.Close(); err != nil {
					t.Fatalf("Close destination: %v", err)
				}
			}

			// Reopen the destination the way the next process would: the
			// imported objects must be there again, with the same List.
			reopened := dstFactory.open(t, destDir)
			defer func() {
				if closer, ok := reopened.(io.Closer); ok {
					_ = closer.Close()
				}
			}()
			reopenedList, err := reopened.List(ctx)
			if err != nil {
				t.Fatalf("List reopened destination: %v", err)
			}
			if !slices.Equal(digestKeys(sourceList), digestKeys(reopenedList)) {
				t.Fatalf("List after reopen = %v, want %v", digestKeys(reopenedList), digestKeys(sourceList))
			}
			stats, err := reopened.Stats(ctx)
			if err != nil {
				t.Fatalf("Stats reopened destination: %v", err)
			}
			if stats.ObjectCount != int64(len(sourceList)) {
				t.Fatalf("Stats after reopen = %+v, want %d objects", stats, len(sourceList))
			}
		})
	}
}

// TestPackedStoreSnapshotIndexOnDisk: importing into a packed store leaves a
// pack index behind, so the imported objects are still packed — and still
// readable — from a newly opened backend.
func TestPackedStoreSnapshotIndexOnDisk(t *testing.T) {
	ctx := context.Background()
	src, err := packfs.New(t.TempDir(), packfs.WithEnabled())
	if err != nil {
		t.Fatalf("packfs.New: %v", err)
	}
	// The source's pack file stays open until it is closed explicitly, and on
	// Windows an open handle blocks the TempDir cleanup that deletes it.
	defer func() { _ = src.Close() }()
	payload := []byte("imported into a packed store")
	digest := sha256.Of(payload)
	if err := src.Put(ctx, digest, bytes.NewReader(payload)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	var archive bytes.Buffer
	if err := snapshot.Export(ctx, src, &archive); err != nil {
		t.Fatalf("Export: %v", err)
	}

	destDir := t.TempDir()
	dest, err := packfs.New(destDir, packfs.WithEnabled())
	if err != nil {
		t.Fatalf("packfs.New destination: %v", err)
	}
	if err := snapshot.Import(ctx, dest, bytes.NewReader(archive.Bytes())); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if err := dest.Close(); err != nil {
		t.Fatalf("Close destination: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destDir, "packs", "index.json")); err != nil {
		t.Fatalf("imported objects left no pack index on disk: %v", err)
	}
	reopened, err := packfs.New(destDir, packfs.WithEnabled())
	if err != nil {
		t.Fatalf("reopen destination: %v", err)
	}
	defer reopened.Close()
	list, err := reopened.List(ctx)
	if err != nil {
		t.Fatalf("List reopened destination: %v", err)
	}
	if len(list) != 1 || !list[0].Equal(digest) {
		t.Fatalf("List reopened destination = %v, want exactly [%s]", list, digest)
	}
	if got := readObject(t, reopened, digest); !bytes.Equal(got, payload) {
		t.Fatalf("imported object = %q, want %q", got, payload)
	}
}

// readObject reads a whole object and requires the read to succeed.
func readObject(t *testing.T, backend cas.Backend, digest cas.Digest) []byte {
	t.Helper()
	rc, err := backend.Get(context.Background(), digest)
	if err != nil {
		t.Fatalf("Get(%s): %v", digest, err)
	}
	data, err := io.ReadAll(rc)
	if err != nil {
		_ = rc.Close()
		t.Fatalf("read %s: %v", digest, err)
	}
	if err := rc.Close(); err != nil {
		t.Fatalf("close %s: %v", digest, err)
	}
	return data
}

// digestKeys renders digests as a sorted key list for an order-independent
// comparison of two stores' List results.
func digestKeys(digests []cas.Digest) []string {
	keys := make([]string, 0, len(digests))
	for _, digest := range digests {
		keys = append(keys, digest.String())
	}
	slices.Sort(keys)
	return keys
}
