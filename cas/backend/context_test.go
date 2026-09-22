package backend

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestContextReaderReadsAndStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := ContextReader{Ctx: ctx, R: strings.NewReader("abc")}

	buf := make([]byte, 2)
	n, err := r.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("Read() error = %v, want nil or EOF", err)
	}
	if n != 2 || string(buf) != "ab" {
		t.Fatalf("Read() = (%d, %q), want (2, \"ab\")", n, string(buf))
	}

	cancel()
	_, err = r.Read(buf)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Read() after cancel = %v, want context.Canceled", err)
	}
	if err == nil {
		t.Fatal("Read() after cancel returned nil error")
	}
}

func TestContextReaderCancelledBeforeRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	r := ContextReader{Ctx: ctx, R: strings.NewReader("hello")}
	buf := make([]byte, 1)
	_, err := r.Read(buf)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Read() after cancel = %v, want context.Canceled", err)
	}
}

// TestContextReaderZeroValue pins the harmless zero value: a nil Ctx means no
// cancellation and a nil R reads as an exhausted reader instead of panicking.
func TestContextReaderZeroValue(t *testing.T) {
	var r ContextReader
	if n, err := r.Read(make([]byte, 4)); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("zero ContextReader.Read() = (%d, %v), want (0, io.EOF)", n, err)
	}
	noCtx := ContextReader{R: strings.NewReader("hi")}
	buf := make([]byte, 2)
	if n, err := noCtx.Read(buf); err != nil || n != 2 || string(buf) != "hi" {
		t.Fatalf("nil-Ctx Read() = (%d, %q, %v), want (2, \"hi\", nil)", n, buf, err)
	}
}

func TestReadPayloadAndWriteAll(t *testing.T) {
	ctx := context.Background()
	if got, err := ReadPayload(ctx, strings.NewReader("abc"), 0); err != nil || len(got) != 0 {
		t.Fatalf("ReadPayload(size 0) = (%q, %v), want empty payload and nil", got, err)
	}
	if got, err := ReadPayload(ctx, strings.NewReader("abc"), 3); err != nil || string(got) != "abc" {
		t.Fatalf("ReadPayload(3) = (%q, %v), want %q and nil", got, err, "abc")
	}
	if _, err := ReadPayload(ctx, strings.NewReader("ab"), 3); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("ReadPayload(short) error = %v, want io.ErrUnexpectedEOF", err)
	}
	// A huge declared size must not allocate it: the read fails on the bytes
	// that are actually missing.
	if _, err := ReadPayload(ctx, strings.NewReader("ab"), 1<<40); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("ReadPayload(huge) error = %v, want io.ErrUnexpectedEOF", err)
	}

	var buf []byte
	if err := WriteAll(ctx, &shortWriter{}, []byte("xyz")); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("WriteAll(zero-progress writer) error = %v, want io.ErrShortWrite", err)
	}
	if err := WriteAll(ctx, writerFunc(func(p []byte) (int, error) {
		buf = append(buf, p...)
		return len(p), nil
	}), []byte("xyz")); err != nil || string(buf) != "xyz" {
		t.Fatalf("WriteAll = (%q, %v), want %q and nil", buf, err, "xyz")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := WriteAll(canceled, &shortWriter{}, []byte("x")); !errors.Is(err, context.Canceled) {
		t.Fatalf("WriteAll(canceled) error = %v, want context.Canceled", err)
	}
}

// shortWriter reports success without consuming anything, the zero-progress
// case WriteAll must reject instead of looping forever.
type shortWriter struct{}

func (*shortWriter) Write(p []byte) (int, error) { return 0, nil }

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
