package crc64_test

import (
	"bytes"
	"context"
	"errors"
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
