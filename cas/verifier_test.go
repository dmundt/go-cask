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
	fs "github.com/dmundt/go-cask/cas/backend/fs"
	mem "github.com/dmundt/go-cask/cas/backend/mem"
	sha256 "github.com/dmundt/go-cask/cas/hash/sha256"
)

type getErrorBackend struct{ err error }

func (b getErrorBackend) Put(context.Context, cas.Digest, io.Reader) error { return nil }
func (b getErrorBackend) Get(context.Context, cas.Digest) (io.ReadCloser, error) {
	return nil, b.err
}
func (b getErrorBackend) Exists(context.Context, cas.Digest) (bool, error) { return false, nil }
func (b getErrorBackend) Delete(context.Context, cas.Digest) error         { return nil }
func (b getErrorBackend) List(context.Context) ([]cas.Digest, error)       { return nil, nil }
func (b getErrorBackend) Stats(context.Context) (*cas.Stats, error)        { return &cas.Stats{}, nil }

type closeErrorBackend struct{ reader io.ReadCloser }

func (b closeErrorBackend) Put(context.Context, cas.Digest, io.Reader) error { return nil }
func (b closeErrorBackend) Get(context.Context, cas.Digest) (io.ReadCloser, error) {
	return b.reader, nil
}
func (b closeErrorBackend) Exists(context.Context, cas.Digest) (bool, error) { return true, nil }
func (b closeErrorBackend) Delete(context.Context, cas.Digest) error         { return nil }
func (b closeErrorBackend) List(context.Context) ([]cas.Digest, error)       { return nil, nil }
func (b closeErrorBackend) Stats(context.Context) (*cas.Stats, error)        { return &cas.Stats{}, nil }

type failingCloseReader struct {
	io.Reader
	err error
}

func (r failingCloseReader) Close() error { return r.err }

type validateErrorHasher struct{}

func (validateErrorHasher) Digest(io.Reader) (cas.Digest, error) { return nil, nil }
func (validateErrorHasher) Validate(cas.Digest) error            { return errors.New("validate failed") }

type digestErrorHasher struct{}

func (digestErrorHasher) Digest(io.Reader) (cas.Digest, error) {
	return nil, errors.New("digest failed")
}
func (digestErrorHasher) Validate(cas.Digest) error { return nil }

func TestVerifierDetectsCorruption(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	raw, err := fs.New(base)
	if err != nil {
		t.Fatal(err)
	}

	data := []byte("hello world")
	d := sha256.Of(data)
	if err := raw.Put(ctx, d, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if err := cas.NewVerifier(raw, sha256.New()).Verify(ctx, d); err != nil {
		t.Fatalf("Verify(intact) = %v, want nil", err)
	}

	path := filepath.Join(base, d.String()[:2], d.String())
	if err := os.WriteFile(path, []byte("tampered bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cas.NewVerifier(raw, sha256.New()).Verify(ctx, d); !errors.Is(err, cas.ErrDigestMismatch) {
		t.Fatalf("Verify(tampered) = %v, want ErrDigestMismatch", err)
	}
	if err := cas.Verify(ctx, raw, d, sha256.New()); !errors.Is(err, cas.ErrDigestMismatch) {
		t.Fatalf("cas.Verify(tampered) = %v, want ErrDigestMismatch", err)
	}
}

// TestVerifierHasNoNilReceiver pins the receiver contract: NewVerifier always
// returns a usable Verifier, so Verify deliberately has no nil-receiver branch
// to report. A nil *Verifier is a programming error, not a state the method
// handles, and callers therefore never need a nil check.
func TestVerifierHasNoNilReceiver(t *testing.T) {
	if v := cas.NewVerifier(mem.New(), sha256.New()); v == nil {
		t.Fatal("NewVerifier returned nil; callers would need a nil check Verify does not implement")
	}
}

func TestVerifyRejectsInvalidInputs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := cas.Verify(ctx, nil, sha256.Of([]byte("x")), sha256.New()); !errors.Is(err, context.Canceled) {
		t.Fatalf("Verify(canceled ctx) = %v, want context.Canceled", err)
	}
	if err := cas.Verify(context.Background(), nil, sha256.Of([]byte("x")), sha256.New()); err == nil || !strings.Contains(err.Error(), "nil backend") {
		t.Fatalf("Verify(nil backend) = %v, want nil-backend error", err)
	}
	if err := cas.Verify(context.Background(), mem.New(), nil, sha256.New()); err == nil || !errors.Is(err, cas.ErrInvalidDigest) {
		t.Fatalf("Verify(absent digest) = %v, want ErrInvalidDigest", err)
	}
	if err := cas.Verify(context.Background(), mem.New(), sha256.Of([]byte("x")), nil); err == nil || !strings.Contains(err.Error(), "nil hasher") {
		t.Fatalf("Verify(nil hasher) = %v, want nil-hasher error", err)
	}
}

func TestVerifyPropagatesHasherErrors(t *testing.T) {
	ctx := context.Background()
	raw := mem.New()
	d := sha256.Of([]byte("hello"))
	if err := raw.Put(ctx, d, bytes.NewReader([]byte("hello"))); err != nil {
		t.Fatal(err)
	}
	if err := cas.Verify(ctx, raw, d, validateErrorHasher{}); err == nil || !strings.Contains(err.Error(), "validate failed") {
		t.Fatalf("Verify(validate error) = %v, want validate error", err)
	}
	if err := cas.Verify(ctx, raw, d, digestErrorHasher{}); err == nil || !strings.Contains(err.Error(), "digest failed") {
		t.Fatalf("Verify(digest error) = %v, want digest error", err)
	}
}

func TestVerifyReportsBackendReadFailure(t *testing.T) {
	ctx := context.Background()
	d := sha256.Of([]byte("hello"))
	want := errors.New("get failed")
	if err := cas.Verify(ctx, getErrorBackend{err: want}, d, sha256.New()); !errors.Is(err, want) {
		t.Fatalf("Verify(get error) = %v, want %v", err, want)
	}
}

func TestVerifyReportsBackendCloseFailure(t *testing.T) {
	digest := sha256.Of([]byte("hello"))
	want := errors.New("close failed")
	raw := closeErrorBackend{
		reader: failingCloseReader{Reader: bytes.NewReader([]byte("hello")), err: want},
	}
	if err := cas.Verify(context.Background(), raw, digest, sha256.New()); !errors.Is(err, want) {
		t.Fatalf("Verify(close error) = %v, want %v", err, want)
	}
}
