package cas_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	backmem "github.com/dmundt/go-cask/cas/backend/mem"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

// readerOnly hides every method but Read, so a test can hand PutStream a stream
// that cannot be rewound — the shape a request body or a pipe has, and the
// reason the helper spools at all.
type readerOnly struct{ r io.Reader }

// Read implements io.Reader.
func (o readerOnly) Read(p []byte) (int, error) { return o.r.Read(p) }

// streamTestBackend wraps a healthy backend and injects a failure into the
// named operation, so PutStream's error paths are reachable without a store on
// disk that cannot be arranged into them. A nil error is no fault.
type streamTestBackend struct {
	cas.Backend
	existsErr error
	putErr    error
}

var _ cas.Backend = (*streamTestBackend)(nil)

// Exists returns the injected error, or the healthy backend's answer.
func (b *streamTestBackend) Exists(ctx context.Context, d cas.Digest) (bool, error) {
	if b.existsErr != nil {
		return false, b.existsErr
	}
	return b.Backend.Exists(ctx, d)
}

// Put returns the injected error, or stores through the healthy backend.
func (b *streamTestBackend) Put(ctx context.Context, d cas.Digest, r io.Reader) error {
	if b.putErr != nil {
		return b.putErr
	}
	return b.Backend.Put(ctx, d, r)
}

// failingHasher fails Digest, so a test can drive the digest step's error path.
type failingHasher struct {
	err error
}

// Digest implements cas.Hasher.
func (h failingHasher) Digest(io.Reader) (cas.Digest, error) { return nil, h.err }

// Validate implements cas.Hasher.
func (h failingHasher) Validate(cas.Digest) error { return nil }

// TestPutStreamSpoolsHashesAndStores is the helper's contract: the bytes are
// addressed by their own digest, the size is the stream's byte count, the store
// holds exactly those bytes, and a second call of the same content reports the
// deduplication instead of writing again.
func TestPutStreamSpoolsHashesAndStores(t *testing.T) {
	ctx := context.Background()
	content := []byte("hash-on-write")
	for _, tc := range []struct {
		name string
		r    io.Reader
	}{
		{"seekable", bytes.NewReader(content)},
		{"not seekable", readerOnly{bytes.NewReader(content)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := backmem.New()
			d, dedup, err := cas.PutStream(ctx, backend, sha256.New(), tc.r)
			if err != nil {
				t.Fatalf("PutStream = %v, want success", err)
			}
			if !d.Equal(sha256.Of(content)) {
				t.Fatalf("PutStream digest = %s, want %s", d, sha256.Of(content))
			}
			if dedup {
				t.Fatal("first PutStream reported deduplication")
			}
			rc, err := backend.Get(ctx, d)
			if err != nil {
				t.Fatalf("Get after PutStream = %v", err)
			}
			stored, err := io.ReadAll(rc)
			if closeErr := rc.Close(); err == nil {
				err = closeErr
			}
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(stored, content) {
				t.Fatalf("stored %q, want %q", stored, content)
			}

			again, dedup, err := cas.PutStream(ctx, backend, sha256.New(), readerOnly{bytes.NewReader(content)})
			if err != nil || !dedup || !again.Equal(d) {
				t.Fatalf("second PutStream = (%s, %v, %v), want the same digest, deduplicated", again, dedup, err)
			}
		})
	}
}

// TestPutStreamStoresAnEmptyStream pins that the layer itself has no minimum
// size: an empty stream is the object the empty digest addresses, so it is
// stored like any other content. A caller that refuses an empty upload applies
// that rule itself, to the bytes it read.
func TestPutStreamStoresAnEmptyStream(t *testing.T) {
	ctx := context.Background()
	backend := backmem.New()
	d, dedup, err := cas.PutStream(ctx, backend, sha256.New(), strings.NewReader(""))
	if err != nil || dedup {
		t.Fatalf("PutStream(empty) = (%s, %v, %v), want the empty digest, no dedup", d, dedup, err)
	}
	if !d.Equal(sha256.Of(nil)) {
		t.Fatalf("PutStream(empty) digest = %s, want %s", d, sha256.Of(nil))
	}
	if ok, err := backend.Exists(ctx, d); err != nil || !ok {
		t.Fatalf("the empty stream was not stored: exists=%v err=%v", ok, err)
	}
}

// TestPutStreamReportsFailures pins every error path: a cancelled context is
// the caller's own error, a nil backend or hasher is rejected rather than
// panicking, and the spool, digest, existence probe and write steps each report
// their cause.
func TestPutStreamReportsFailures(t *testing.T) {
	content := []byte("payload")
	ctx := context.Background()

	t.Run("cancelled context", func(t *testing.T) {
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if _, _, err := cas.PutStream(canceled, backmem.New(), sha256.New(), bytes.NewReader(content)); !errors.Is(err, context.Canceled) {
			t.Fatalf("PutStream on a cancelled context = %v, want context.Canceled", err)
		}
	})

	t.Run("nil backend", func(t *testing.T) {
		if _, _, err := cas.PutStream(ctx, nil, sha256.New(), bytes.NewReader(content)); err == nil {
			t.Fatal("PutStream with a nil backend succeeded")
		}
	})

	t.Run("nil hasher", func(t *testing.T) {
		if _, _, err := cas.PutStream(ctx, backmem.New(), nil, bytes.NewReader(content)); err == nil {
			t.Fatal("PutStream with a nil hasher succeeded")
		}
	})

	t.Run("unreadable stream", func(t *testing.T) {
		want := errors.New("read failed")
		_, _, err := cas.PutStream(ctx, backmem.New(), sha256.New(), readerOnly{errReader{want}})
		if !errors.Is(err, want) {
			t.Fatalf("PutStream over a failing reader = %v, want %v", err, want)
		}
	})

	t.Run("digest failure", func(t *testing.T) {
		want := errors.New("digest failed")
		_, _, err := cas.PutStream(ctx, backmem.New(), failingHasher{err: want}, bytes.NewReader(content))
		if !errors.Is(err, want) {
			t.Fatalf("PutStream with a failing hasher = %v, want %v", err, want)
		}
	})

	t.Run("existence probe failure", func(t *testing.T) {
		want := errors.New("exists failed")
		backend := &streamTestBackend{Backend: backmem.New(), existsErr: want}
		if _, _, err := cas.PutStream(ctx, backend, sha256.New(), bytes.NewReader(content)); !errors.Is(err, want) {
			t.Fatalf("PutStream with a failing Exists = %v, want %v", err, want)
		}
	})

	t.Run("write failure", func(t *testing.T) {
		want := errors.New("put failed")
		backend := &streamTestBackend{Backend: backmem.New(), putErr: want}
		if _, _, err := cas.PutStream(ctx, backend, sha256.New(), bytes.NewReader(content)); !errors.Is(err, want) {
			t.Fatalf("PutStream with a failing Put = %v, want %v", err, want)
		}
	})

	t.Run("spool failure", func(t *testing.T) {
		// An unusable temp directory is the spool's one real failure mode, and
		// it must be reported rather than panicking on a nil spool. On Windows
		// TMP is what os.TempDir reads; on POSIX it is TMPDIR.
		blocked := filepath.Join(t.TempDir(), "not-a-directory")
		if err := os.WriteFile(blocked, []byte("occupied"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("TMP", blocked)
		t.Setenv("TMPDIR", blocked)
		if _, _, err := cas.PutStream(ctx, backmem.New(), sha256.New(), bytes.NewReader(content)); err == nil {
			t.Fatal("PutStream with an unusable temp directory succeeded")
		}
	})
}

// errReader always fails, so the spool step's read error is reachable.
type errReader struct{ err error }

// Read implements io.Reader.
func (r errReader) Read([]byte) (int, error) { return 0, r.err }
