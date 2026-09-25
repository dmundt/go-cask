package fs

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas/hash/sha256"
	"github.com/dmundt/go-cask/internal/test"
)

// TestBackendBasePathNamesTheStoreRoot pins Backend.BasePath: it reports exactly
// the path the caller handed to New, and the backend's bytes really do live
// beneath it. The path is not decoration — cas/verify/sidecar stores its records
// in a subdirectory of whatever BasePath reports and refuses a base that
// disagrees with it — so a backend that reported anything else would place one
// store's records beside another store's bytes.
func TestBackendBasePathNamesTheStoreRoot(t *testing.T) {
	ctx := context.Background()

	t.Run("gitlike default layout", func(t *testing.T) {
		base := filepath.Join(t.TempDir(), "store")
		s, err := New(base)
		if err != nil {
			t.Fatal(err)
		}
		if got := s.BasePath(); got != base {
			t.Fatalf("BasePath() = %q, want the path passed to New (%q)", got, base)
		}
		fi, err := os.Stat(s.BasePath())
		if err != nil {
			t.Fatalf("stat BasePath(): %v", err)
		}
		if !fi.IsDir() {
			t.Fatalf("BasePath() = %q, want a directory", s.BasePath())
		}

		payload := []byte("addressed under the reported base")
		d := digestOf(payload)
		if err := s.Put(ctx, d, strings.NewReader(string(payload))); err != nil {
			t.Fatal(err)
		}
		// The object file is <BasePath>/<fan-out>/<hex digest>: the reported
		// directory is the root the fan-out tree hangs from, not the file's
		// directory and not the store's parent.
		objectPath := filepath.Join(s.BasePath(), d.String()[:2], d.String())
		info, err := os.Stat(objectPath)
		if err != nil {
			t.Fatalf("stored object is not beneath BasePath() (%q): %v", s.BasePath(), err)
		}
		if !info.Mode().IsRegular() {
			t.Fatalf("%s mode = %v, want a regular file", objectPath, info.Mode())
		}
		if info.Size() != int64(len(payload)) {
			t.Fatalf("stored object size = %d, want %d", info.Size(), len(payload))
		}
	})

	t.Run("flat layout", func(t *testing.T) {
		base := filepath.Join(t.TempDir(), "flat")
		s, err := New(base, WithFanOut(0), WithFanLevels(0))
		if err != nil {
			t.Fatal(err)
		}
		if got := s.BasePath(); got != base {
			t.Fatalf("BasePath() = %q, want %q", got, base)
		}
		d := digestOf([]byte("flat object"))
		if err := s.Put(ctx, d, strings.NewReader("flat object")); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(s.BasePath(), d.String())); err != nil {
			t.Fatalf("flat object is not directly under BasePath(): %v", err)
		}
	})

	t.Run("nested base is reported verbatim", func(t *testing.T) {
		base := filepath.Join(t.TempDir(), "nested", "store")
		s, err := New(base)
		if err != nil {
			t.Fatal(err)
		}
		if got := s.BasePath(); got != base {
			t.Fatalf("BasePath() = %q, want the nested path %q", got, base)
		}
		// Clean is a sweep over BasePath, so a wrong report would sweep the
		// parent instead of the store: a temp file beside the base must survive.
		parentTemp := filepath.Join(filepath.Dir(base), "sibling.tmp")
		if err := os.WriteFile(parentTemp, []byte("not ours"), 0o644); err != nil {
			t.Fatal(err)
		}
		if n, err := s.Clean(ctx, 0); err != nil || n != 0 {
			t.Fatalf("Clean() = (%d, %v), want (0, nil): the sweep must stay inside BasePath", n, err)
		}
		if _, err := os.Stat(parentTemp); err != nil {
			t.Fatalf("Clean swept a file outside BasePath(): %v", err)
		}
	})
}

// TestBackendBasePathSurvivesTheBackendValue pins that BasePath is a pure read of
// the backend's own configuration: it stays the same across a Put/Delete cycle,
// and it answers for a backend built by hand — the shape FuzzPathRoundTrip and
// the scripted-walk tests use — where no constructor set anything at all.
func TestBackendBasePathSurvivesTheBackendValue(t *testing.T) {
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), "stable")
	s, err := New(base)
	if err != nil {
		t.Fatal(err)
	}
	d := digestOf([]byte("stable base"))
	if err := s.Put(ctx, d, strings.NewReader("stable base")); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, d); err != nil {
		t.Fatal(err)
	}
	if got := s.BasePath(); got != base {
		t.Fatalf("BasePath() after Put/Delete = %q, want %q", got, base)
	}

	hand := &Backend{base: "hand-built/base", fanOut: 2, fanLevels: 1}
	if got := hand.BasePath(); got != "hand-built/base" {
		t.Fatalf("hand-built BasePath() = %q, want %q", got, "hand-built/base")
	}
}

// removeTempOnEOF yields payload and then deletes the temp file Put is writing
// into before reporting end of stream. It stages, deterministically and without a
// goroutine race, the POSIX shape of Put's rename-failure branch: a concurrent
// writer published the same content first, so the rename finds nothing to move
// while the object's final path is already a regular file.
type removeTempOnEOF struct {
	payload []byte
	tmp     string
	removed bool
}

func (r *removeTempOnEOF) Read(p []byte) (int, error) {
	if len(r.payload) > 0 {
		n := copy(p, r.payload)
		r.payload = r.payload[n:]
		return n, nil
	}
	if !r.removed {
		r.removed = true
		if err := os.Remove(r.tmp); err != nil {
			return 0, err
		}
	}
	return 0, io.EOF
}

// TestPutIsIdempotentWhenThePublishRenameFails pins the "already published"
// branch of Put's rename-failure recovery: the content address makes the object
// already stored, so a rename that fails while the object's final path is present
// as a regular file is a success — and the bytes served afterwards are the ones
// that were already there.
//
// The two platforms fail that rename for different reasons, so each gets the
// shape that reaches it: on Windows the destination is held open, which is the
// documented caveat (Go cannot replace a file another handle holds open); on
// POSIX, where a rename over a regular file always succeeds, the temp object is
// removed mid-write instead.
func TestPutIsIdempotentWhenThePublishRenameFails(t *testing.T) {
	ctx := context.Background()
	s := mustFS(t)
	payload := []byte("published by someone else first")
	d := digestOf(payload)
	if err := s.Put(ctx, d, bytes.NewReader(payload)); err != nil {
		t.Fatal(err)
	}

	if runtime.GOOS == "windows" {
		held, err := os.Open(s.digestPath(d))
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		if err := s.Put(ctx, d, bytes.NewReader(payload)); err != nil {
			t.Fatalf("Put over a held-open destination = %v, want nil (the object is already published)", err)
		}
	} else {
		// The temp file createTempExcl creates is <object path>.tmp.
		r := &removeTempOnEOF{payload: payload, tmp: s.digestPath(d) + ".tmp"}
		if err := s.Put(ctx, d, r); err != nil {
			t.Fatalf("Put with a vanished temp object = %v, want nil (the object is already published)", err)
		}
		if !r.removed {
			t.Fatal("the test reader never removed the temp file, so the rename branch was not reached")
		}
	}

	rc, err := s.Get(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	got, err := test.ReadAllAndClose(rc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("Get = %q, want the already published %q", got, payload)
	}
	if err := s.Verify(ctx, d, sha256.New()); err != nil {
		t.Fatalf("Verify after an idempotent Put = %v", err)
	}
	if leftovers := tmpFilesIn(s, d); len(leftovers) != 0 {
		t.Fatalf("Put left temp files behind: %v", leftovers)
	}
}

// Branches in this package that no deterministic test reaches, with the reason
// written down instead of papered over by a test that fakes the condition
// (testing-strategy.md §5):
//
//   - syncParentDir's `runtime.GOOS == "windows"` early return: the condition is
//     a compile-time platform constant and the gate measures this package on
//     Linux only (testing-strategy.md §5, "Platform-split packages"); the
//     Windows path is exercised by the platform-matrix `go test ./...` job.
//
//   - Put's two cleanup branches for a failing f.Sync() and a failing f.Close()
//     (the `cleanup()` helper and the `os.Remove(tmp)` before the close error):
//     createTempExcl opens the scratch file with O_CREATE|O_EXCL inside the
//     object's own directory, so the handle is always a freshly created regular
//     file on a filesystem that has already accepted the directory and the
//     writes. fsync or close on that handle fails only for a hardware or quota
//     fault (EIO, EDQUOT) or a filesystem that filled up between the copy and
//     the sync — no deterministic filesystem state produces it (a read-only
//     directory, a directory where a file is expected, a pre-existing temp name,
//     a vanished base and a short write all fail earlier, in branches the suite
//     covers). Both are best-effort cleanup whose failure cannot change the
//     outcome: the temp file is discarded and the primary error is what Put
//     reports.
//
//   - ValidateBase's volume-root check in policy.go: on Linux
//     filepath.VolumeName(clean) is always "", so the remainder is the cleaned
//     path itself, and the branch needs that to be "", "." or "/" — all three
//     are already rejected by the two checks above it. The branch exists for
//     Windows volume roots such as "C:", and is therefore unreachable on the
//     Linux-only coverage measurement.
