package backend

import (
	"bytes"
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

// TestContextReaderWriteToCopies pins the copy itself: io.Copy takes the
// WriterTo path this type now offers, reports the byte count, and keeps the
// zero value harmless.
func TestContextReaderWriteToCopies(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		r    ContextReader
		want string
	}{
		{"ctx", ContextReader{Ctx: ctx, R: strings.NewReader("hello")}, "hello"},
		{"nil-ctx", ContextReader{R: strings.NewReader("hello")}, "hello"},
		{"nil-r", ContextReader{Ctx: ctx}, ""},
		{"zero-value", ContextReader{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var dst bytes.Buffer
			n, err := io.Copy(&dst, tc.r)
			if err != nil {
				t.Fatalf("io.Copy() error = %v", err)
			}
			if dst.String() != tc.want || n != int64(len(tc.want)) {
				t.Fatalf("io.Copy() = (%d, %q), want (%d, %q)", n, dst.String(), len(tc.want), tc.want)
			}
		})
	}
}

// TestContextReaderWriteToCancelsMidStream pins the contract the fast path must
// not weaken: Ctx is checked before every read, so a cancel lands mid-copy, the
// bytes already written are reported, and nothing further is read.
func TestContextReaderWriteToCancelsMidStream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var dst bytes.Buffer
	src := &cancelAfterFirstRead{cancel: cancel, data: "abc"}
	n, err := io.Copy(&dst, ContextReader{Ctx: ctx, R: src})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("io.Copy() after cancel = %v, want context.Canceled", err)
	}
	if dst.String() != "abc" || n != 3 {
		t.Fatalf("io.Copy() after cancel = (%d, %q), want (3, \"abc\")", n, dst.String())
	}
	if src.reads != 1 {
		t.Fatalf("source read %d times, want 1: the ctx check must stop the copy", src.reads)
	}

	canceled, cancelNow := context.WithCancel(ctx)
	cancelNow()
	dst.Reset()
	if n, err := io.Copy(&dst, ContextReader{Ctx: canceled, R: strings.NewReader("abc")}); !errors.Is(err, context.Canceled) || n != 0 || dst.Len() != 0 {
		t.Fatalf("io.Copy(canceled before read) = (%d, %q, %v), want (0, \"\", context.Canceled)", n, dst.String(), err)
	}
}

// TestContextReaderWriteToDoesNotDelegate pins the reason the loop exists: a
// source that offers its own WriterTo must not be handed the destination. It
// would stream to completion without a single Ctx check, so a canceled Put
// would publish a whole object instead of aborting (cas-core §4.4).
func TestContextReaderWriteToDoesNotDelegate(t *testing.T) {
	t.Run("canceled ctx aborts", func(t *testing.T) {
		spy := &writerToSpy{src: strings.NewReader("payload")}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		var dst bytes.Buffer
		if _, err := io.Copy(&dst, ContextReader{Ctx: ctx, R: spy}); !errors.Is(err, context.Canceled) {
			t.Fatalf("io.Copy() with a canceled ctx = %v, want context.Canceled", err)
		}
		if spy.writeTo {
			t.Fatal("the copy was delegated to the source's WriterTo, bypassing the ctx check")
		}
		if dst.Len() != 0 {
			t.Fatalf("a canceled copy wrote %q, want nothing", dst.String())
		}
	})

	t.Run("live ctx still copies", func(t *testing.T) {
		spy := &writerToSpy{src: strings.NewReader("payload")}
		var dst bytes.Buffer
		n, err := io.Copy(&dst, ContextReader{Ctx: context.Background(), R: spy})
		if err != nil || n != 7 || dst.String() != "payload" {
			t.Fatalf("io.Copy() = (%d, %q, %v), want (7, %q, nil)", n, dst.String(), err, "payload")
		}
		if spy.writeTo {
			t.Fatal("the copy was delegated to the source's WriterTo instead of the ctx-checked loop")
		}
	})
}

// TestContextReaderWriteToPropagatesErrors pins the copy's failure shapes: a
// read that returns bytes and an error still writes those bytes, a bare read
// error stops the loop, and a zero-progress writer is io.ErrShortWrite.
func TestContextReaderWriteToPropagatesErrors(t *testing.T) {
	ctx := context.Background()
	readErr := errors.New("read boom")

	var dst bytes.Buffer
	n, err := io.Copy(&dst, ContextReader{Ctx: ctx, R: &dataErrReader{data: "xy", err: readErr}})
	if !errors.Is(err, readErr) || n != 2 || dst.String() != "xy" {
		t.Fatalf("io.Copy(data+error reader) = (%d, %q, %v), want (2, \"xy\", %v)", n, dst.String(), err, readErr)
	}
	if _, err := io.Copy(io.Discard, ContextReader{Ctx: ctx, R: &dataErrReader{err: readErr}}); !errors.Is(err, readErr) {
		t.Fatalf("io.Copy(error reader) = %v, want %v", err, readErr)
	}
	if _, err := io.Copy(&shortWriter{}, ContextReader{Ctx: ctx, R: strings.NewReader("xyz")}); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("io.Copy(zero-progress writer) = %v, want io.ErrShortWrite", err)
	}
	writeErr := errors.New("write boom")
	failingWriter := writerFunc(func([]byte) (int, error) { return 0, writeErr })
	if _, err := io.Copy(failingWriter, ContextReader{Ctx: ctx, R: strings.NewReader("xyz")}); !errors.Is(err, writeErr) {
		t.Fatalf("io.Copy(failing writer) = %v, want %v", err, writeErr)
	}
}

// cancelAfterFirstRead serves its data once, cancels, and counts its reads, so
// a test can tell a ctx-checked loop from a delegated stream.
type cancelAfterFirstRead struct {
	cancel context.CancelFunc
	data   string
	reads  int
}

func (r *cancelAfterFirstRead) Read(p []byte) (int, error) {
	r.reads++
	r.cancel()
	return copy(p, r.data), nil
}

// writerToSpy offers a WriterTo, which a ContextReader must not use: doing so
// would skip the per-read ctx check.
type writerToSpy struct {
	src     *strings.Reader
	writeTo bool
}

func (s *writerToSpy) Read(p []byte) (int, error) { return s.src.Read(p) }

func (s *writerToSpy) WriteTo(w io.Writer) (int64, error) {
	s.writeTo = true
	return s.src.WriteTo(w)
}

// dataErrReader reports its error on the same call that serves data, the
// (n > 0, err != nil) shape a copy must not lose bytes on.
type dataErrReader struct {
	data string
	err  error
	done bool
}

func (r *dataErrReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	return copy(p, r.data), r.err
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
