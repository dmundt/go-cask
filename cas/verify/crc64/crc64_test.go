package crc64_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/dmundt/go-cask/cas"
	mem "github.com/dmundt/go-cask/cas/backend/mem"
	crc64 "github.com/dmundt/go-cask/cas/verify/crc64"
)

func TestHasherRoundTrip(t *testing.T) {
	data := []byte("hello world")
	d, err := crc64.New().Digest(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Digest = %v", err)
	}
	if len(d) != crc64.Size {
		t.Fatalf("Digest len = %d, want %d", len(d), crc64.Size)
	}
	if err := crc64.New().Validate(d); err != nil {
		t.Fatalf("Validate = %v, want nil", err)
	}
	if err := crc64.New().Validate(nil); !errors.Is(err, cas.ErrInvalidDigest) {
		t.Fatalf("Validate(nil) = %v, want ErrInvalidDigest", err)
	}
}

func TestFormatParse(t *testing.T) {
	data := []byte("hello world")
	d := crc64.Of(data)
	if got := crc64.Format(d); got != "crc64:"+d.String() {
		t.Fatalf("Format = %q, want %q", got, "crc64:"+d.String())
	}
	parsed, err := crc64.Parse(crc64.Format(d))
	if err != nil {
		t.Fatalf("Parse = %v", err)
	}
	if !parsed.Equal(d) {
		t.Fatalf("Parse = %x, want %x", parsed, d)
	}
	if _, err := crc64.Parse(d.String()); err != nil {
		t.Fatalf("Parse(bare digest) = %v", err)
	}
	for _, input := range []string{"sha256:abcd", "xyz", "abcd"} {
		if _, err := crc64.Parse(input); err == nil {
			t.Fatalf("Parse(%q) = nil, want error", input)
		}
	}
	if got := crc64.Format(nil); got != "" {
		t.Fatalf("Format(nil) = %q, want empty", got)
	}
}

func TestVerifyIntegration(t *testing.T) {
	ctx := context.Background()
	raw := mem.New()
	data := []byte("hello world")
	d := crc64.Of(data)
	if err := raw.Put(ctx, d, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if err := cas.Verify(ctx, raw, d, crc64.New()); err != nil {
		t.Fatalf("Verify(valid) = %v, want nil", err)
	}
	if err := raw.Put(ctx, d, bytes.NewReader([]byte("tampered"))); err != nil {
		t.Fatal(err)
	}
	if err := cas.Verify(ctx, raw, d, crc64.New()); !errors.Is(err, cas.ErrDigestMismatch) {
		t.Fatalf("Verify(tampered) = %v, want ErrDigestMismatch", err)
	}
}

func TestHasherErrorAndHelpers(t *testing.T) {
	if _, err := crc64.New().Digest(errReader{err: io.ErrUnexpectedEOF}); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("Digest(read error) = %v, want wrapped read error", err)
	}
	for _, d := range []cas.Digest{nil, cas.NewDigest([]byte{1, 2, 3, 4, 5, 6, 7}), cas.NewDigest([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9})} {
		if err := crc64.New().Validate(d); !errors.Is(err, cas.ErrInvalidDigest) {
			t.Fatalf("Validate(%x) = %v, want ErrInvalidDigest", d, err)
		}
	}
	h := crc64.NewHasher()
	if _, err := h.Write([]byte("helper")); err != nil {
		t.Fatal(err)
	}
	if h.Sum64() == 0 {
		t.Fatal("NewHasher returned an empty checksum")
	}
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

var _ io.Reader = errReader{}
