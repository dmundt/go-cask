package packfs

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dmundt/go-cask/cas"
)

// TestBackendBasePathPointsAtTheLooseTree pins packfs.Backend.BasePath: it
// reports the loose tree at <basePath>/loose — the directory Get/Put/List/Stats
// act on and the one Clean delegates to — not the pack root passed to New and
// not the pack directory. A maintenance layer above the backend (cas/verify/
// sidecar stores its records there) therefore sits beside the durable object
// bytes, where the same Clean reclaims its scratch.
func TestBackendBasePathPointsAtTheLooseTree(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		opts []Option
	}{
		{"packing enabled", []Option{WithEnabled()}},
		{"packing disabled", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := filepath.Join(t.TempDir(), "store")
			backend, err := New(base, tc.opts...)
			if err != nil {
				t.Fatal(err)
			}
			defer backend.Close()

			want := filepath.Join(base, "loose")
			if got := backend.BasePath(); got != want {
				t.Fatalf("BasePath() = %q, want the loose tree %q", got, want)
			}
			if backend.BasePath() == backend.packDir {
				t.Fatalf("BasePath() = %q, want the loose tree, not the pack directory", backend.BasePath())
			}
			fi, err := os.Stat(backend.BasePath())
			if err != nil {
				t.Fatalf("stat BasePath(): %v", err)
			}
			if !fi.IsDir() {
				t.Fatalf("BasePath() = %q, want a directory", backend.BasePath())
			}

			// The loose copy of every object lives beneath the reported path:
			// the fs backend's default fan-out is (2,1), so the file is
			// <BasePath>/<2 hex>/<full hex>.
			payload := []byte("object bytes under the reported base")
			d := cas.NewDigest(payload)
			if err := backend.Put(ctx, d, bytesReader(payload)); err != nil {
				t.Fatal(err)
			}
			objectPath := filepath.Join(backend.BasePath(), d.String()[:2], d.String())
			info, err := os.Stat(objectPath)
			if err != nil {
				t.Fatalf("loose object is not beneath BasePath() (%q): %v", backend.BasePath(), err)
			}
			if !info.Mode().IsRegular() || info.Size() != int64(len(payload)) {
				t.Fatalf("%s = mode %v, size %d; want a regular file of %d bytes", objectPath, info.Mode(), info.Size(), len(payload))
			}

			// Clean is a sweep over the loose tree plus the pack directory, so a
			// crash leftover under BasePath is reclaimed by the sweep a caller
			// already runs — the reason BasePath, not the pack root, is what the
			// backend reports.
			looseTemp := filepath.Join(backend.BasePath(), "crashed.tmp")
			if err := os.WriteFile(looseTemp, []byte("scratch"), 0o644); err != nil {
				t.Fatal(err)
			}
			removed, err := backend.Clean(ctx, 0)
			if err != nil {
				t.Fatalf("Clean() = %v", err)
			}
			if removed != 1 {
				t.Fatalf("Clean() removed %d files, want the one crash leftover under BasePath", removed)
			}
			if _, err := os.Stat(looseTemp); !os.IsNotExist(err) {
				t.Fatalf("Clean left the crash leftover under BasePath: %v", err)
			}
		})
	}
}
