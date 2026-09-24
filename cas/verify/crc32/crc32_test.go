package crc32_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/dmundt/go-cask/cas"
	backmem "github.com/dmundt/go-cask/cas/backend/mem"
	crc32 "github.com/dmundt/go-cask/cas/verify/crc32"
)

func TestHasherDigestAndValidate(t *testing.T) {
	data := []byte("hello world")
	d, err := crc32.New().Digest(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Digest = %v", err)
	}
	if len(d) != crc32.Size {
		t.Fatalf("Digest len = %d, want %d", len(d), crc32.Size)
	}
	if err := crc32.New().Validate(d); err != nil {
		t.Fatalf("Validate = %v, want nil", err)
	}
	if err := crc32.New().Validate(nil); !errors.Is(err, cas.ErrInvalidDigest) {
		t.Fatalf("Validate(nil) = %v, want ErrInvalidDigest", err)
	}
}

func TestOfFormatParse(t *testing.T) {
	data := []byte("hello world")
	d := crc32.Of(data)
	if got := crc32.Format(d); got != "crc32:"+d.String() {
		t.Fatalf("Format = %q, want %q", got, "crc32:"+d.String())
	}
	parsed, err := crc32.Parse(crc32.Format(d))
	if err != nil {
		t.Fatalf("Parse(Format(d)) = %v", err)
	}
	if !parsed.Equal(d) {
		t.Fatalf("Parse = %x, want %x", parsed, d)
	}
	if _, err := crc32.Parse("sha256:abcd"); err == nil {
		t.Fatal("Parse(other algorithm prefix) = nil, want error")
	}
}

func TestVerifyIntegration(t *testing.T) {
	ctx := context.Background()
	backend := backmem.New()
	data := []byte("hello world")
	d := crc32.Of(data)
	if err := backend.Put(ctx, d, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if err := cas.Verify(ctx, backend, d, crc32.New()); err != nil {
		t.Fatalf("Verify(valid) = %v, want nil", err)
	}
	if err := backend.Put(ctx, d, strings.NewReader("tampered")); err != nil {
		t.Fatal(err)
	}
	if err := cas.Verify(ctx, backend, d, crc32.New()); !errors.Is(err, cas.ErrDigestMismatch) {
		t.Fatalf("Verify(tampered) = %v, want ErrDigestMismatch", err)
	}
}

func TestHasherDigestReadError(t *testing.T) {
	r := &errReader{err: errors.New("boom")}
	if _, err := crc32.New().Digest(r); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Digest(read error) = %v, want boom", err)
	}
}

type errReader struct{ err error }

func (e *errReader) Read([]byte) (int, error) { return 0, e.err }

func TestHasherValidateBadWidths(t *testing.T) {
	if err := crc32.New().Validate(cas.NewDigest([]byte{1, 2, 3})); !errors.Is(err, cas.ErrInvalidDigest) {
		t.Fatalf("Validate(short digest) = %v, want ErrInvalidDigest", err)
	}
	if err := crc32.New().Validate(cas.NewDigest([]byte{1, 2, 3, 4, 5})); !errors.Is(err, cas.ErrInvalidDigest) {
		t.Fatalf("Validate(long digest) = %v, want ErrInvalidDigest", err)
	}
}

var _ io.Reader = (*errReader)(nil)
