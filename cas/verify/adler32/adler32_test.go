package adler32_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/dmundt/go-cask/cas"
	backmem "github.com/dmundt/go-cask/cas/backend/mem"
	adler32 "github.com/dmundt/go-cask/cas/verify/adler32"
)

func TestHasherRoundTrip(t *testing.T) {
	data := []byte("hello world")
	d, err := adler32.New().Digest(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Digest = %v", err)
	}
	if len(d) != adler32.Size {
		t.Fatalf("Digest len = %d, want %d", len(d), adler32.Size)
	}
	if err := adler32.New().Validate(d); err != nil {
		t.Fatalf("Validate = %v, want nil", err)
	}
	if err := adler32.New().Validate(nil); !errors.Is(err, cas.ErrInvalidDigest) {
		t.Fatalf("Validate(nil) = %v, want ErrInvalidDigest", err)
	}
}

func TestFormatParse(t *testing.T) {
	data := []byte("hello world")
	d := adler32.Of(data)
	if got := adler32.Format(d); got != "adler32:"+d.String() {
		t.Fatalf("Format = %q, want %q", got, "adler32:"+d.String())
	}
	parsed, err := adler32.Parse(adler32.Format(d))
	if err != nil {
		t.Fatalf("Parse = %v", err)
	}
	if !parsed.Equal(d) {
		t.Fatalf("Parse = %x, want %x", parsed, d)
	}
	if _, err := adler32.Parse(d.String()); err != nil {
		t.Fatalf("Parse(bare digest) = %v", err)
	}
	for _, input := range []string{"sha256:abcd", "xyz", "abcd"} {
		if _, err := adler32.Parse(input); err == nil {
			t.Fatalf("Parse(%q) = nil, want error", input)
		}
	}
	if got := adler32.Format(nil); got != "" {
		t.Fatalf("Format(nil) = %q, want empty", got)
	}
}

func TestVerifyIntegration(t *testing.T) {
	ctx := context.Background()
	backend := backmem.New()
	data := []byte("hello world")
	d := adler32.Of(data)
	if err := backend.Put(ctx, d, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if err := cas.Verify(ctx, backend, d, adler32.New()); err != nil {
		t.Fatalf("Verify(valid) = %v, want nil", err)
	}
	if err := backend.Put(ctx, d, bytes.NewReader([]byte("tampered"))); err != nil {
		t.Fatal(err)
	}
	if err := cas.Verify(ctx, backend, d, adler32.New()); !errors.Is(err, cas.ErrDigestMismatch) {
		t.Fatalf("Verify(tampered) = %v, want ErrDigestMismatch", err)
	}
}

func TestHasherErrorAndHelpers(t *testing.T) {
	if _, err := adler32.New().Digest(errReader{err: io.ErrUnexpectedEOF}); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("Digest(read error) = %v, want wrapped read error", err)
	}
	for _, d := range []cas.Digest{nil, cas.NewDigest([]byte{1, 2, 3}), cas.NewDigest([]byte{1, 2, 3, 4, 5})} {
		if err := adler32.New().Validate(d); !errors.Is(err, cas.ErrInvalidDigest) {
			t.Fatalf("Validate(%x) = %v, want ErrInvalidDigest", d, err)
		}
	}
	h := adler32.NewHasher()
	if _, err := h.Write([]byte("helper")); err != nil {
		t.Fatal(err)
	}
	if h.Sum32() == 0 {
		t.Fatal("NewHasher returned an empty checksum")
	}
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

var _ io.Reader = errReader{}
