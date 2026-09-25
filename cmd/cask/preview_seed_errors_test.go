package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	fs "github.com/dmundt/go-cask/cas/backend/fs"
	backmem "github.com/dmundt/go-cask/cas/backend/mem"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

// Uncovered in preview_seed.go, with the reason each branch has no
// deterministic test (testing-strategy §5):
//
//   - previewObjectFor's envelope error (preview_seed.go:177-178) can only come
//     from cas.EncodeEnvelope, which the seed loop calls with a synthetic codec
//     tag, a versioned type name and a payload at least as long as the header —
//     exactly the shape that writer accepts for every ordinal the CLI can ask
//     for.
//   - the same envelope error as previewReferences rebuilds the graph
//     (preview_seed.go:294-295) is the identical call, so it is unreachable for
//     the same reason.
//   - previewReferences' cas.Reachable error (preview_seed.go:329-330) needs the
//     RefListerFunc it builds to fail, and that closure only reads a map.
//
// The two hasher failures (preview_seed.go:119-120 from seedPreview and
// 181-182 from previewObjectFor) ARE reachable through the injected-hasher seam
// and are covered by TestSeedPreviewReportsAFailingHasher.

// failingHasher computes through a real hasher and then starts failing, so a
// test can make digesting fail after the store is in a known state — the one
// seam through which preview object construction can be made to fail.
type failingHasher struct {
	inner cas.Hasher
	// failAfter is the number of digests to compute before failing.
	failAfter int
	calls     int
	err       error
}

// Digest computes through the inner hasher until the fault is armed.
func (h *failingHasher) Digest(r io.Reader) (cas.Digest, error) {
	h.calls++
	if h.calls > h.failAfter {
		return nil, h.err
	}
	return h.inner.Digest(r)
}

// Validate delegates to the inner hasher.
func (h *failingHasher) Validate(d cas.Digest) error { return h.inner.Validate(d) }

// TestSeedPreviewReportsAFailingHasher pins the digest seam of the seed path:
// when hashing fails, seedPreview reports the hasher's error wrapped with the
// ordinal it was building and does not continue past it. The store keeps the
// objects the earlier ordinals already wrote — the loop is not transactional and
// says so — but nothing is stored for the ordinal that failed to build
// (preview_seed.go).
func TestSeedPreviewReportsAFailingHasher(t *testing.T) {
	want := errors.New("digest failed")
	// The first digest belongs to ordinal 0, which is built and stored before
	// the second is computed, so the failure lands on ordinal 1.
	hasher := &failingHasher{inner: sha256.New(), failAfter: 1, err: want}
	backend := backmem.New()

	added, deduplicated, err := seedPreview(context.Background(), backend, hasher, 4)
	if !errors.Is(err, want) {
		t.Fatalf("seedPreview with a failing hasher = %v, want it to wrap %v", err, want)
	}
	if !strings.Contains(err.Error(), "build preview object 1") {
		t.Fatalf("error = %q, want it to name the ordinal being built", err)
	}
	// The counts describe what the completed iterations did, and the failing one
	// is reported through the error rather than counted.
	if added != 0 || deduplicated != 0 {
		t.Fatalf("a failed seed reported (added %d, deduplicated %d), want the error to carry the failure", added, deduplicated)
	}

	// Exactly ordinal 0 reached the store; ordinal 1 never did, and the loop
	// stopped rather than skipping it.
	digests, err := backend.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	first, err := previewObjectFor(0, nil, sha256.New())
	if err != nil {
		t.Fatal(err)
	}
	if len(digests) != 1 || !digests[0].Equal(first.digest) {
		t.Fatalf("a failed seed stored %v, want only the object of ordinal 0 (%s)", digests, first.digest)
	}
}

// TestSeedPreviewReportsBackendFaults pins the seeding loop's two backend
// faults (preview_seed.go): a failed existence probe and a failed write are both
// reported with the ordinal they happened at, so an operator can tell which
// preview object could not be seeded. A failed probe writes nothing, and a
// failed seed reports no objects added.
func TestSeedPreviewReportsBackendFaults(t *testing.T) {
	t.Run("existence probe fails at the first ordinal", func(t *testing.T) {
		want := errors.New("probe failed")
		backend := newErroringBackend(backmem.New(), "exists", want)
		_, _, err := seedPreview(context.Background(), backend, sha256.New(), 4)
		if !errors.Is(err, want) {
			t.Fatalf("seedPreview with a failing Exists = %v, want it to wrap %v", err, want)
		}
		if !strings.Contains(err.Error(), "check preview object 0") {
			t.Fatalf("error = %q, want it to name the failing ordinal", err)
		}
	})

	t.Run("write fails at the first ordinal", func(t *testing.T) {
		want := errors.New("write failed")
		backend := newErroringBackend(backmem.New(), "put", want)
		added, deduplicated, err := seedPreview(context.Background(), backend, sha256.New(), 4)
		if !errors.Is(err, want) {
			t.Fatalf("seedPreview with a failing Put = %v, want it to wrap %v", err, want)
		}
		if !strings.Contains(err.Error(), "seed preview object 0") {
			t.Fatalf("error = %q, want it to name the failing ordinal", err)
		}
		if added != 0 || deduplicated != 0 {
			t.Fatalf("a failed seed reported (added %d, deduplicated %d), want both 0", added, deduplicated)
		}
	})
}

// TestSeedPreviewRequiresFlagsNotOperands pins the seed command's argument
// grammar: it takes -count and -hash-algo only, so an unknown flag, a positional
// operand and an unknown algorithm are all usage errors (exit 2) — never a
// silently ignored argument (cli.md §2, §3).
func TestSeedPreviewRequiresFlagsNotOperands(t *testing.T) {
	mf := localMF(t)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"unknown flag", []string{"seed-preview", "-nope", "3"}},
		{"operand", []string{"seed-preview", "3"}},
		{"unknown algorithm", []string{"seed-preview", "-hash-algo", "sha1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, code := runBoth(t, mf, tc.args[0], tc.args[1:]...); code != 2 {
				t.Fatalf("%v exit = %d, want 2 (usage)", tc.args, code)
			}
		})
	}
}

// TestPreviewReferencesReportsAnUnreadableStore pins the preview walk's
// runtime failure: a store whose objects cannot be probed is reported as an
// error — the opposite of errNoPreviewGraph, which means "an ordinary store" —
// so the viewer refuses to start on an unreadable store rather than treating
// the failure as a reference-free one (cli.md §2, viewer-design §2).
func TestPreviewReferencesReportsAnUnreadableStore(t *testing.T) {
	// The filesystem shape below is POSIX-specific: on Windows a regular file
	// used as a directory reports ERROR_PATH_NOT_FOUND, which Go maps to
	// fs.ErrNotExist, so the probe answers "not stored" and the walk sees an
	// ordinary empty store instead of a failure.
	if runtime.GOOS != "windows" {
		storeDir := t.TempDir()
		first, err := previewObjectFor(0, nil, sha256.New())
		if err != nil {
			t.Fatal(err)
		}
		// The probe stats <store>/<first two hex chars>/<full hex>; a regular
		// file at the bucket path makes that stat fail with "not a directory".
		bucket := first.digest.String()[:2]
		if err := os.WriteFile(filepath.Join(storeDir, bucket), []byte("not a directory"), 0o644); err != nil {
			t.Fatal(err)
		}
		backend, err := fs.New(storeDir)
		if err != nil {
			t.Fatal(err)
		}

		index, err := previewReferences(context.Background(), backend, sha256.New())
		if err == nil {
			t.Fatal("previewReferences over an unprobeable store = nil error, want a failure")
		}
		if errors.Is(err, errNoPreviewGraph) {
			t.Fatalf("previewReferences reported an ordinary empty store (%v), want an unreadable-store failure", err)
		}
		if !strings.Contains(err.Error(), "check preview object 0") {
			t.Fatalf("error = %q, want it to name the failing probe", err)
		}
		if index != nil {
			t.Fatalf("previewReferences returned an index %v alongside its error, want nil", index)
		}
	}

	// The branch itself, on every platform: the same failure injected directly,
	// so the walk reports it wrapped with the ordinal it was probing, never as
	// errNoPreviewGraph.
	backend, err := fs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := errors.New("probe failed")
	_, err = previewReferences(context.Background(), newErroringBackend(backend, "exists", want), sha256.New())
	if !errors.Is(err, want) {
		t.Fatalf("previewReferences with a failing Exists = %v, want %v", err, want)
	}
	if !strings.Contains(err.Error(), "check preview object 0") {
		t.Fatalf("error = %q, want it to name the failing probe", err)
	}
}
